package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kayiik/compa/pkg/compute"
	"github.com/kayiik/compa/pkg/config"
	"github.com/kayiik/compa/web/backend/utils"
)

var (
	computeBroker = compute.NewBroker()
	compitaLocks  sync.Map // compita id -> *sync.Mutex; one turn at a time per Compita
	// turnBases holds, for each Compita with a turn running, the address that
	// turn was told to reach Compa at (a string). A message to a colleague
	// passes it on to the colleague's turn, in place of the Host header of the
	// request, which a Compita can set to anything.
	turnBases sync.Map // compita id -> string
)

const compitaTurnTimeout = 15 * time.Minute

func (h *Handler) registerComputeRunRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/compute/poll", h.handleComputePoll)
	mux.HandleFunc("POST /api/compute/result", h.handleComputeResult)
	mux.HandleFunc("POST /api/compute/peer", h.handleComputePeer)
	h.registerObjectiveRoutes(mux)
	h.registerStreamRoutes(mux)
	h.registerConnectionRoutes(mux)
	h.registerInstallRoutes(mux)
	h.registerLiveRoutes(mux)
	h.registerApprovalRoutes(mux)
	h.registerTriggerRoutes(mux)
	mux.HandleFunc("POST /api/computes/{id}/refresh", h.handleComputeRefresh)
	mux.HandleFunc("GET /api/compitas/{id}/chat", h.handleCompitaChatHistory)
	mux.HandleFunc("POST /api/compitas/{id}/chat", h.handleCompitaChatSend)
}

// authCompute checks the credential a dial-out compute presents.
func (h *Handler) authCompute(r *http.Request) (string, bool) {
	id := r.Header.Get("X-Compute-ID")
	cred := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if id == "" || cred == "" || !h.computeStore().Authenticate(id, cred) {
		return "", false
	}
	return id, true
}

func (h *Handler) handleComputePoll(w http.ResponseWriter, r *http.Request) {
	id, ok := h.authCompute(r)
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "invalid compute credential")
		return
	}
	// Reading the (empty) body lets the server notice a worker that went away
	// instead of holding its poll for the whole wait.
	_, _ = io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, 1<<16))
	job, got := computeBroker.Next(r.Context(), id, 25*time.Second)
	if !got {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (h *Handler) handleComputeResult(w http.ResponseWriter, r *http.Request) {
	id, ok := h.authCompute(r)
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "invalid compute credential")
		return
	}
	var res compute.Result
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&res); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid result")
		return
	}
	computeBroker.Complete(id, res)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) findCompita(id string) (compute.Compita, compute.Compute, error) {
	st, err := h.computeStore().Load()
	if err != nil {
		return compute.Compita{}, compute.Compute{}, err
	}
	if !st.Enabled {
		return compute.Compita{}, compute.Compute{}, compute.ErrDisabled
	}
	for _, p := range st.Compitas {
		if p.ID != id {
			continue
		}
		for _, c := range st.Computes {
			if c.ID == p.ComputeID {
				return p, c, nil
			}
		}
	}
	return compute.Compita{}, compute.Compute{}, compute.ErrNotFound
}

func (h *Handler) handleCompitaChatHistory(w http.ResponseWriter, r *http.Request) {
	p, _, err := h.findCompita(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, computeErrorStatus(err), err.Error())
		return
	}
	msgs, err := compute.LoadChat(config.GetHome(), p.ID, 200)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": msgs})
}

const maxPeerDepth = 3

// localPeerSecret is the root of the credentials of Compitas on this computer.
// It lives only in memory.
var localPeerSecret = func() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}()

// localCredential is what a Compita on this computer presents to the host. It
// is derived from the Compita's own id, so one Compita's credential cannot
// speak for another, even on the same computer.
func localCredential(compitaID string) string {
	mac := hmac.New(sha256.New, []byte(localPeerSecret))
	mac.Write([]byte(compitaID))
	return hex.EncodeToString(mac.Sum(nil))
}

func requestBase(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// runTurn sends one message to a Compita wherever it runs.
func (h *Handler) runTurn(ctx context.Context, p compute.Compita, c compute.Compute, message string, depth int, baseURL string) (string, error) {
	session := "compita:" + p.ID
	turnBases.Store(p.ID, baseURL)
	defer turnBases.Delete(p.ID)
	// The turn shows live while it runs, wherever it runs.
	streams.begin(p.ID)
	defer streams.end(p.ID)
	sink := func(ev compute.StreamEvent) { streams.apply(p.ID, ev) }
	switch {
	case c.Kind == compute.KindLocal:
		home := config.GetHome()
		return compute.Runner{
			Home: home, Kernel: utils.FindKernelBinary(),
			Peer: &compute.Peer{URL: baseURL, ComputeID: c.ID, Credential: localCredential(p.ID)},
		}.RunStream(ctx, compute.Turn{CompitaID: p.ID, Isolation: p.Isolation, Session: session, Message: message, Depth: depth, Approvals: p.Approvals, Browser: p.Browser, Connections: p.Connections}, sink)
	case c.Mode == compute.ModeDialOut:
		res, err := computeBroker.SubmitStream(ctx, c.ID, compute.Job{
			CompitaID: p.ID, Isolation: p.Isolation, Session: session, Message: message, Depth: depth, Approvals: p.Approvals, Browser: p.Browser, Connections: p.Connections,
		}, sink)
		if err == nil && res.Error != "" {
			err = errString(res.Error)
		}
		return res.Reply, err
	default:
		res, err := compute.DialRun(ctx, c, compute.Turn{
			CompitaID: p.ID, Isolation: p.Isolation, Session: session, Message: message, Depth: depth, Approvals: p.Approvals, Browser: p.Browser, Connections: p.Connections,
			PeerURL: baseURL, PeerCompute: c.ID, PeerToken: c.DialSecret,
		}, sink)
		if err == nil && res.Error != "" {
			err = errString(res.Error)
		}
		return res.Reply, err
	}
}

func (h *Handler) handleCompitaChatSend(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Message string `json:"message"`
	}
	if err := decodeJSONBody(r, &req); err != nil || strings.TrimSpace(req.Message) == "" {
		writeJSONError(w, http.StatusBadRequest, "message is required")
		return
	}
	p, c, err := h.findCompita(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, computeErrorStatus(err), err.Error())
		return
	}
	mu, _ := compitaLocks.LoadOrStore(p.ID, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()

	home := config.GetHome()
	_ = compute.AppendChat(home, p.ID, compute.ChatMessage{Role: "user", Text: req.Message, At: time.Now()})
	ctx, cancel := context.WithTimeout(r.Context(), compitaTurnTimeout)
	defer cancel()
	reply, err := h.runTurn(ctx, p, c, req.Message, 0, requestBase(r))

	msg := compute.ChatMessage{Role: "compita", Text: reply, At: time.Now()}
	if err != nil {
		msg.Text, msg.Error = err.Error(), true
	}
	_ = compute.AppendChat(home, p.ID, msg)
	writeJSON(w, http.StatusOK, msg)
}

// computeCaller authenticates a request made for one of a compute's Compitas
// (from): a Compita on this computer with its own derived credential, a
// dial-out compute with the credential it enrolled with, a dial-in compute
// with its dial secret.
func (h *Handler) computeCaller(r *http.Request, from string) (cid string, ok bool) {
	cid = r.Header.Get("X-Compute-ID")
	cred := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if cid == compute.LocalID {
		return cid, from != "" && subtle.ConstantTimeCompare([]byte(cred), []byte(localCredential(from))) == 1
	}
	st := h.computeStore()
	return cid, cid != "" && cred != "" && (st.Authenticate(cid, cred) || st.AuthenticateDial(cid, cred))
}

// computeAuthenticated checks the credential of a remote compute itself, for
// requests about its jobs rather than for one of its Compitas.
func (h *Handler) computeAuthenticated(r *http.Request) (cid string, ok bool) {
	cid = r.Header.Get("X-Compute-ID")
	cred := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if cid == "" || cid == compute.LocalID || cred == "" {
		return cid, false
	}
	st := h.computeStore()
	return cid, st.Authenticate(cid, cred) || st.AuthenticateDial(cid, cred)
}

// handleComputePeer routes a message from one Compita to another. The caller
// proves itself with its credential.
func (h *Handler) handleComputePeer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		From, To, Message string
		Depth             int
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil || strings.TrimSpace(req.Message) == "" {
		writeJSONError(w, http.StatusBadRequest, "to and message are required")
		return
	}
	cid, ok := h.computeCaller(r, req.From)
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "invalid compute credential")
		return
	}
	if req.Depth >= maxPeerDepth {
		writeJSONError(w, http.StatusUnprocessableEntity, "too many Compita-to-Compita hops")
		return
	}
	st, err := h.computeStore().Load()
	if err != nil || !st.Enabled {
		writeJSONError(w, http.StatusBadRequest, "Compitas support is off")
		return
	}
	var from, target *compute.Compita
	var names []string
	for i := range st.Compitas {
		p := &st.Compitas[i]
		names = append(names, p.Name)
		if p.ID == req.From {
			from = p
		}
		if strings.EqualFold(p.ID, req.To) || strings.EqualFold(p.Name, req.To) {
			target = p
		}
	}
	// A compute may only speak for its own Compitas.
	if from == nil || from.ComputeID != cid {
		writeJSONError(w, http.StatusForbidden, "unknown sender")
		return
	}
	if target == nil || target.ID == from.ID {
		writeJSONError(w, http.StatusNotFound, "no such peer; available: "+strings.Join(names, ", "))
		return
	}
	// The colleague reaches Compa where the sender's turn does.
	base, running := turnBases.Load(from.ID)
	if !running {
		writeJSONError(w, http.StatusConflict, "the sender has no turn running")
		return
	}
	mu, _ := compitaLocks.LoadOrStore(target.ID, &sync.Mutex{})
	if !mu.(*sync.Mutex).TryLock() {
		writeJSONError(w, http.StatusConflict, target.Name+" is busy right now. If "+target.Name+" is waiting for your answer to something they asked, "+
			"just give that answer as your reply instead of messaging them; otherwise try again shortly")
		return
	}
	defer mu.(*sync.Mutex).Unlock()
	var tc compute.Compute
	for _, c := range st.Computes {
		if c.ID == target.ComputeID {
			tc = c
		}
	}
	home := config.GetHome()
	_ = compute.AppendChat(home, target.ID, compute.ChatMessage{Role: "peer", Text: from.Name + ": " + req.Message, At: time.Now()})
	ctx, cancel := context.WithTimeout(r.Context(), compitaTurnTimeout)
	defer cancel()
	reply, err := h.runTurn(ctx, *target, tc, peerPrompt(from.Name, req.Message), req.Depth+1, base.(string))
	msg := compute.ChatMessage{Role: "compita", Text: reply, At: time.Now()}
	if err != nil {
		msg.Text, msg.Error = err.Error(), true
	}
	_ = compute.AppendChat(home, target.ID, msg)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"reply": reply})
}

// peerPrompt is what a Compita is told when a colleague messages it. Its reply
// goes back to the colleague by itself: answering through message_peer instead
// would find the colleague busy waiting for exactly that reply.
func peerPrompt(from, message string) string {
	return "[Message from your colleague " + from + "]\n" + message +
		"\n\n(Your reply to this message is returned to " + from + " automatically. Just answer it; " +
		"do not use message_peer to reply.)"
}

type errString string

func (e errString) Error() string { return string(e) }

// handleComputeRefresh re-reads what a dial-in compute can do (Docker).
func (h *Handler) handleComputeRefresh(w http.ResponseWriter, r *http.Request) {
	st, err := h.computeStore().Load()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, c := range st.Computes {
		if c.ID != r.PathValue("id") || c.Mode != compute.ModeDialIn {
			continue
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		caps, err := compute.DialInfo(ctx, c)
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, err.Error())
			return
		}
		if err := h.computeStore().SetCapabilities(c.ID, caps); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		h.writeComputeState(w, http.StatusOK)
		return
	}
	writeJSONError(w, http.StatusNotFound, "no such dial-in compute")
}
