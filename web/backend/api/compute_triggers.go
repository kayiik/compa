package api

import (
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kayiik/compa/pkg/compute"
	"github.com/kayiik/compa/pkg/config"
)

// schedulerTick is how often the host looks for scheduled and waiting work.
var schedulerTick = 5 * time.Second

func (h *Handler) registerTriggerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/compitas/{id}/triggers", h.handleTriggersGet)
	mux.HandleFunc("POST /api/compitas/{id}/schedules", h.handleScheduleAdd)
	mux.HandleFunc("DELETE /api/compitas/{id}/schedules/{sid}", h.handleScheduleRemove)
	mux.HandleFunc("POST /api/compitas/{id}/schedules/{sid}", h.handleScheduleToggle)
	mux.HandleFunc("POST /api/compitas/{id}/trigger/token", h.handleTriggerToken)
	mux.HandleFunc("DELETE /api/compitas/{id}/trigger", h.handleTriggerRevoke)
	mux.HandleFunc("POST /api/compitas/{id}/trigger", h.handleTriggerEvent)
}

// StartScheduler starts the loop that starts scheduled and incoming work on
// Compitas that are free. It stops with Shutdown.
func (h *Handler) StartScheduler() {
	go func() {
		t := time.NewTicker(schedulerTick)
		defer t.Stop()
		for {
			select {
			case <-objectiveRuns.ctx.Done():
				return
			case now := <-t.C:
				h.schedulerTick(now)
			}
		}
	}()
}

// schedulerTick starts the next waiting event or due schedule on every free
// Compita.
func (h *Handler) schedulerTick(now time.Time) {
	st, err := h.computeStore().Load()
	if err != nil || !st.Enabled {
		return
	}
	home := config.GetHome()
	for _, p := range st.Compitas {
		o, _ := compute.LoadObjective(home, p.ID)
		if !compute.Free(o) {
			continue
		}
		goal, kind, err := compute.NextWork(home, p.ID, now)
		if err != nil || goal == "" {
			continue
		}
		if !h.startWork(p.ID, goal, kind) && kind == "event" {
			_ = compute.QueueEvent(home, p.ID, goal)
		}
	}
}

// startWork makes goal the Compita's objective and sets it going.
func (h *Handler) startWork(id, goal, kind string) bool {
	home := config.GetHome()
	tr, _ := compute.LoadTriggers(home, id)
	if _, err := compute.StartObjective(home, id, goal, tr.BaseURL, 0, 0); err != nil {
		return false
	}
	label := "Started by a schedule: "
	if kind == "event" {
		label = "Started by an event: "
	}
	_ = compute.AppendChat(home, id, compute.ChatMessage{Role: "objective", Text: label + firstLine(goal), At: time.Now()})
	h.startObjectiveLoop(id)
	return true
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	if len([]rune(line)) > 200 {
		return string([]rune(line)[:200]) + "…"
	}
	return line
}

type triggersView struct {
	Schedules []compute.Schedule `json:"schedules"`
	Trigger   struct {
		Enabled     bool   `json:"enabled"`
		Instruction string `json:"instruction,omitempty"`
		Pending     int    `json:"pending"`
	} `json:"trigger"`
}

func (h *Handler) handleTriggersGet(w http.ResponseWriter, r *http.Request) {
	p, _, err := h.findCompita(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, computeErrorStatus(err), err.Error())
		return
	}
	tr, err := compute.LoadTriggers(config.GetHome(), p.ID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	v := triggersView{Schedules: tr.Schedules}
	v.Trigger.Enabled, v.Trigger.Instruction, v.Trigger.Pending = tr.Token != "", tr.Instruction, len(tr.Pending)
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) handleScheduleAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Goal         string `json:"goal"`
		EveryMinutes int    `json:"every_minutes"`
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
	s, err := compute.AddSchedule(config.GetHome(), p.ID, req.Goal, req.EveryMinutes, requestBase(r), time.Now())
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, s)
}

func (h *Handler) handleScheduleRemove(w http.ResponseWriter, r *http.Request) {
	p, _, err := h.findCompita(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, computeErrorStatus(err), err.Error())
		return
	}
	if err := compute.RemoveSchedule(config.GetHome(), p.ID, r.PathValue("sid")); err != nil {
		writeJSONError(w, computeErrorStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) handleScheduleToggle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
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
	if err := compute.SetScheduleEnabled(config.GetHome(), p.ID, r.PathValue("sid"), req.Enabled, time.Now()); err != nil {
		writeJSONError(w, computeErrorStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) handleTriggerToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Instruction string `json:"instruction"`
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
	token, err := compute.NewTriggerToken(config.GetHome(), p.ID, req.Instruction, requestBase(r))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"token": token,
		"url":   requestBase(r) + "/api/compitas/" + p.ID + "/trigger",
	})
}

func (h *Handler) handleTriggerRevoke(w http.ResponseWriter, r *http.Request) {
	p, _, err := h.findCompita(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, computeErrorStatus(err), err.Error())
		return
	}
	if err := compute.RevokeTrigger(config.GetHome(), p.ID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleTriggerEvent takes an event from outside (a webhook, a script) and
// puts the Compita to work on it, or queues it if the Compita is busy.
func (h *Handler) handleTriggerEvent(w http.ResponseWriter, r *http.Request) {
	p, _, err := h.findCompita(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, compute.MaxEventBytes))
	if err != nil {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "the event is too large")
		return
	}
	home := config.GetHome()
	tr, err := compute.LoadTriggers(home, p.ID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not read the triggers")
		return
	}
	bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !compute.VerifyTrigger(tr, bearer, r.Header.Get("X-Hub-Signature-256"), body) {
		writeJSONError(w, http.StatusUnauthorized, "invalid token or signature")
		return
	}
	source := strings.Map(func(r rune) rune {
		if r < 32 {
			return -1
		}
		return r
	}, r.Header.Get("X-GitHub-Event"))
	if len(source) > 80 {
		source = source[:80]
	}
	if source != "" {
		source = "GitHub event: " + source
	}
	goal := compute.EventGoal(tr.Instruction, source, string(body))

	o, _ := compute.LoadObjective(home, p.ID)
	if compute.Free(o) && h.startWork(p.ID, goal, "event") {
		writeJSON(w, http.StatusAccepted, map[string]any{"status": "started"})
		return
	}
	if err := compute.QueueEvent(home, p.ID, goal); err != nil {
		writeJSONError(w, http.StatusTooManyRequests, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "queued"})
}
