package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/kayiik/compa/pkg/compute"
	"github.com/kayiik/compa/pkg/config"
)

// approvalInbox holds what Compitas wait to ask the owner. It lives in this
// process: an approval belongs to a turn in flight, which a restart ends.
var approvalInbox = compute.NewInbox()

func (h *Handler) registerApprovalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/compute/approval", h.handleComputeApproval)
	mux.HandleFunc("GET /api/compute/approvals", h.handleApprovalList)
	mux.HandleFunc("POST /api/compute/approvals/{id}", h.handleApprovalDecide)
}

// handleComputeApproval is where a Compita's approver hook asks the owner. It
// answers once the owner does, so the Compita's turn waits in place.
func (h *Handler) handleComputeApproval(w http.ResponseWriter, r *http.Request) {
	var ask compute.ApprovalAsk
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&ask); err != nil || ask.Tool == "" {
		writeJSONError(w, http.StatusBadRequest, "from and tool are required")
		return
	}
	cid, ok := h.computeCaller(r, ask.From)
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "invalid compute credential")
		return
	}
	p, _, err := h.findCompita(ask.From)
	if err != nil || p.ComputeID != cid {
		writeJSONError(w, http.StatusForbidden, "unknown sender")
		return
	}
	home := config.GetHome()
	summary := compute.Summarize(ask.Tool, ask.Arguments)
	_ = compute.AppendChat(home, p.ID, compute.ChatMessage{Role: "approval", Text: "Waiting for your approval: " + summary, At: time.Now()})
	approved, reason := approvalInbox.Ask(r.Context(), ask, compute.ApprovalWait)
	verdict := "Denied"
	if approved {
		verdict = "Approved"
	}
	_ = compute.AppendChat(home, p.ID, compute.ChatMessage{Role: "approval", Text: verdict + ": " + summary, At: time.Now()})
	writeJSON(w, http.StatusOK, map[string]any{"approved": approved, "reason": reason})
}

func (h *Handler) handleApprovalList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"approvals": approvalInbox.Pending(r.URL.Query().Get("compita"))})
}

func (h *Handler) handleApprovalDecide(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Approved bool `json:"approved"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := approvalInbox.Decide(r.PathValue("id"), req.Approved); err != nil {
		writeJSONError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
