package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/kayiik/compa/pkg/compute"
	"github.com/kayiik/compa/pkg/config"
)

const objectiveTurnTimeout = 30 * time.Minute

var objectivePause = 2 * time.Second

// objectiveRuns tracks the loops working on Compita objectives in this process.
var objectiveRuns = struct {
	mu   sync.Mutex
	runs map[string]*objectiveRun
	ctx  context.Context
	stop context.CancelFunc
}{runs: map[string]*objectiveRun{}}

type objectiveRun struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func init() {
	objectiveRuns.ctx, objectiveRuns.stop = context.WithCancel(context.Background())
}

// stopObjectiveRuns ends every loop without changing any objective, so each
// active one resumes at the next start.
func stopObjectiveRuns() { objectiveRuns.stop() }

// haltObjective cancels the loop for a Compita and waits for it to end.
func haltObjective(id string) {
	objectiveRuns.mu.Lock()
	run := objectiveRuns.runs[id]
	objectiveRuns.mu.Unlock()
	if run != nil {
		run.cancel()
		<-run.done
	}
}

// startObjectiveLoop (re)starts the loop that drives a Compita's objective.
func (h *Handler) startObjectiveLoop(id string) {
	haltObjective(id)
	ctx, cancel := context.WithCancel(objectiveRuns.ctx)
	run := &objectiveRun{cancel: cancel, done: make(chan struct{})}
	objectiveRuns.mu.Lock()
	objectiveRuns.runs[id] = run
	objectiveRuns.mu.Unlock()
	go func() {
		defer func() {
			objectiveRuns.mu.Lock()
			if objectiveRuns.runs[id] == run {
				delete(objectiveRuns.runs, id)
			}
			objectiveRuns.mu.Unlock()
			close(run.done)
		}()
		home := config.GetHome()
		compute.Drive(ctx, home, id, compute.DriveOptions{
			Pause: objectivePause,
			Colleagues: func() []string {
				st, err := h.computeStore().Load()
				if err != nil {
					return nil
				}
				var names []string
				for _, p := range st.Compitas {
					if p.ID != id {
						names = append(names, p.Name)
					}
				}
				return names
			},
			Say: func(text string, isErr bool) {
				_ = compute.AppendChat(home, id, compute.ChatMessage{Role: "compita", Text: text, Error: isErr, At: time.Now()})
			},
			Turn: func(ctx context.Context, prompt string) (string, error) {
				p, c, err := h.findCompita(id)
				if err != nil {
					return "", err
				}
				o, _ := compute.LoadObjective(home, id)
				base := ""
				if o != nil {
					base = o.BaseURL
				}
				mu, _ := compitaLocks.LoadOrStore(id, &sync.Mutex{})
				mu.(*sync.Mutex).Lock()
				defer mu.(*sync.Mutex).Unlock()
				tctx, tcancel := context.WithTimeout(ctx, objectiveTurnTimeout)
				defer tcancel()
				return h.runTurn(tctx, p, c, prompt, 0, base)
			},
		})
	}()
}

// ResumeObjectives restarts the loops of objectives that were active when the
// launcher last stopped.
func (h *Handler) ResumeObjectives() {
	st, err := h.computeStore().Load()
	if err != nil || !st.Enabled {
		return
	}
	home := config.GetHome()
	for _, p := range st.Compitas {
		if o, _ := compute.LoadObjective(home, p.ID); o != nil && o.Status == compute.ObjActive {
			h.startObjectiveLoop(p.ID)
		}
	}
}

type objectiveView struct {
	Objective *compute.Objective `json:"objective"`
	Running   bool               `json:"running"`
	// AwaitingApproval counts the owner's answers the Compita waits for.
	AwaitingApproval int `json:"awaiting_approval"`
}

func objectiveState(id string) (objectiveView, error) {
	o, err := compute.LoadObjective(config.GetHome(), id)
	objectiveRuns.mu.Lock()
	_, running := objectiveRuns.runs[id]
	objectiveRuns.mu.Unlock()
	if o != nil {
		o.BaseURL = ""
	}
	return objectiveView{Objective: o, Running: running, AwaitingApproval: len(approvalInbox.Pending(id))}, err
}

func (h *Handler) registerObjectiveRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/compitas/{id}/objective", h.handleObjectiveGet)
	mux.HandleFunc("POST /api/compitas/{id}/objective", h.handleObjectiveSet)
	mux.HandleFunc("POST /api/compitas/{id}/objective/{action}", h.handleObjectiveAction)
}

func (h *Handler) handleObjectiveGet(w http.ResponseWriter, r *http.Request) {
	p, _, err := h.findCompita(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, computeErrorStatus(err), err.Error())
		return
	}
	v, err := objectiveState(p.ID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) handleObjectiveSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Goal       string `json:"goal"`
		MaxTurns   int    `json:"max_turns"`
		MaxMinutes int    `json:"max_minutes"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	p, _, err := h.findCompita(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, computeErrorStatus(err), err.Error())
		return
	}
	home := config.GetHome()
	if _, err := compute.StartObjective(home, p.ID, req.Goal, requestBase(r), req.MaxTurns, req.MaxMinutes); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	_ = compute.AppendChat(home, p.ID, compute.ChatMessage{Role: "objective", Text: req.Goal, At: time.Now()})
	h.startObjectiveLoop(p.ID)
	v, _ := objectiveState(p.ID)
	writeJSON(w, http.StatusCreated, v)
}

func (h *Handler) handleObjectiveAction(w http.ResponseWriter, r *http.Request) {
	p, _, err := h.findCompita(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, computeErrorStatus(err), err.Error())
		return
	}
	home := config.GetHome()
	action := r.PathValue("action")
	var target compute.ObjectiveStatus
	switch action {
	case "pause":
		target = compute.ObjPaused
	case "stop":
		target = compute.ObjStopped
	case "resume":
		target = compute.ObjActive
	default:
		writeJSONError(w, http.StatusNotFound, "unknown action")
		return
	}
	_, err = compute.UpdateObjective(home, p.ID, func(o *compute.Objective) error {
		switch {
		case target == compute.ObjActive && o.Status == compute.ObjActive:
			return nil
		case target == compute.ObjActive:
			if o.Status == compute.ObjDone || o.Status == compute.ObjStopped {
				return errString("this objective has ended; set a new one")
			}
			if o.Status == compute.ObjExhausted {
				o.MaxTurns += compute.DefaultMaxTurns
				o.MaxMinutes += compute.DefaultMaxMinutes
			}
			o.Status, o.Reason = compute.ObjActive, ""
			o.BaseURL = requestBase(r)
		case o.Status == compute.ObjActive:
			o.Status = target
		default:
			return errString("this objective is not running")
		}
		return nil
	})
	if err != nil {
		code := http.StatusConflict
		if err == compute.ErrNotFound {
			code = http.StatusNotFound
		}
		writeJSONError(w, code, err.Error())
		return
	}
	if target == compute.ObjActive {
		h.startObjectiveLoop(p.ID)
	} else {
		haltObjective(p.ID)
	}
	v, _ := objectiveState(p.ID)
	writeJSON(w, http.StatusOK, v)
}
