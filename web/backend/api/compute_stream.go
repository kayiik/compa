package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/kayiik/compa/pkg/compute"
)

// streams holds what each Compita is doing right now, so a dashboard can show
// the answer as it is written and the tools it uses.
var streams = &streamHub{byID: map[string]*liveStream{}}

const (
	maxLiveText  = 200 << 10
	maxLiveTools = 30
)

type liveTool struct {
	Tool   string `json:"tool"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

type streamHub struct {
	mu   sync.Mutex
	byID map[string]*liveStream
}

type liveStream struct {
	active   bool
	text     string
	segStart int
	tools    []liveTool
	subs     map[chan []byte]struct{}
}

func (h *streamHub) stream(id string) *liveStream {
	s := h.byID[id]
	if s == nil {
		s = &liveStream{subs: map[chan []byte]struct{}{}}
		h.byID[id] = s
	}
	return s
}

// broadcast sends a message to every subscriber. One that cannot keep up is
// dropped; its client reconnects and starts from a snapshot.
func (s *liveStream) broadcast(msg any) {
	raw, err := json.Marshal(msg)
	if err != nil {
		return
	}
	for ch := range s.subs {
		select {
		case ch <- raw:
		default:
			delete(s.subs, ch)
			close(ch)
		}
	}
}

// begin starts a fresh live view for a turn of the Compita.
func (h *streamHub) begin(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.stream(id)
	s.active, s.text, s.segStart, s.tools = true, "", 0, nil
	s.broadcast(compute.StreamEvent{Type: "begin"})
}

// end closes the live view: the answer is in the chat now.
func (h *streamHub) end(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.stream(id)
	s.active = false
	s.broadcast(compute.StreamEvent{Type: "end"})
}

// apply folds one event of the running turn into the live view.
func (h *streamHub) apply(id string, ev compute.StreamEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.stream(id)
	switch ev.Type {
	case "segment":
		if s.text != "" {
			s.text += "\n\n"
		}
		s.segStart = len(s.text)
	case "reset":
		s.text = s.text[:s.segStart]
	case "delta":
		if len(s.text)+len(ev.Text) <= maxLiveText {
			s.text += ev.Text
		}
	case "tool":
		if ev.State == "start" {
			s.tools = append(s.tools, liveTool{Tool: ev.Tool, State: "start", Detail: ev.Detail})
			if len(s.tools) > maxLiveTools {
				s.tools = s.tools[len(s.tools)-maxLiveTools:]
			}
		} else {
			for i := len(s.tools) - 1; i >= 0; i-- {
				if s.tools[i].Tool == ev.Tool && s.tools[i].State == "start" {
					s.tools[i].State = ev.State
					break
				}
			}
		}
	default:
		return
	}
	s.broadcast(ev)
}

type snapshot struct {
	Type   string     `json:"type"`
	Active bool       `json:"active"`
	Text   string     `json:"text"`
	Tools  []liveTool `json:"tools"`
}

// subscribe returns the current view and a channel of what follows it.
func (h *streamHub) subscribe(id string) (snap []byte, ch chan []byte, cancel func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.stream(id)
	tools := s.tools
	if tools == nil {
		tools = []liveTool{}
	}
	snap, _ = json.Marshal(snapshot{Type: "snapshot", Active: s.active, Text: s.text, Tools: tools})
	ch = make(chan []byte, 512)
	s.subs[ch] = struct{}{}
	return snap, ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if _, ok := s.subs[ch]; ok {
			delete(s.subs, ch)
			close(ch)
		}
	}
}

func (h *Handler) registerStreamRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/compitas/{id}/stream", h.handleCompitaStream)
	mux.HandleFunc("POST "+compute.EventsPath, h.handleComputeEvents)
}

// handleCompitaStream streams what the Compita is doing (server-sent events):
// a snapshot, then each event of its turns as it happens.
func (h *Handler) handleCompitaStream(w http.ResponseWriter, r *http.Request) {
	p, _, err := h.findCompita(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, computeErrorStatus(err), err.Error())
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "streaming is not supported here")
		return
	}
	snap, ch, cancel := streams.subscribe(p.ID)
	defer cancel()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	_, _ = fmt.Fprintf(w, "data: %s\n\n", snap)
	flusher.Flush()
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg, open := <-ch:
			if !open {
				return
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		case <-ping.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// handleComputeEvents takes the events a dial-out compute reports for a job
// it is running.
func (h *Handler) handleComputeEvents(w http.ResponseWriter, r *http.Request) {
	cid, ok := h.computeAuthenticated(r)
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "invalid compute credential")
		return
	}
	var rep compute.EventsReport
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&rep); err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad events report")
		return
	}
	if !computeBroker.Events(cid, rep.JobID, rep.Events) {
		writeJSONError(w, http.StatusNotFound, "no such job")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
