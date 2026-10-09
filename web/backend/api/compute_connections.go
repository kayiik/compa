package api

import (
	"net/http"
	"sort"

	"github.com/kayiik/compa/pkg/compute"
)

func redactAll(ps []compute.Compita) []compute.Compita {
	out := make([]compute.Compita, len(ps))
	for i, p := range ps {
		out[i] = p.Redacted()
	}
	return out
}

func (h *Handler) registerConnectionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/compitas/{id}/connections", h.handleConnectionsGet)
	mux.HandleFunc("PUT /api/compitas/{id}/connections/{name}", h.handleConnectionSet)
	mux.HandleFunc("DELETE /api/compitas/{id}/connections/{name}", h.handleConnectionRemove)
}

type connectionView struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	// EnvKeys names the variables set; their values are never sent back.
	EnvKeys []string `json:"env_keys"`
}

func (h *Handler) handleConnectionsGet(w http.ResponseWriter, r *http.Request) {
	p, _, err := h.findCompita(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, computeErrorStatus(err), err.Error())
		return
	}
	out := []connectionView{}
	for name, c := range p.Connections {
		keys := []string{}
		for k := range c.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		args := c.Args
		if args == nil {
			args = []string{}
		}
		out = append(out, connectionView{Name: name, Command: c.Command, Args: args, EnvKeys: keys})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{"connections": out})
}

func (h *Handler) handleConnectionSet(w http.ResponseWriter, r *http.Request) {
	var req compute.Connection
	if err := decodeJSONBody(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	p, _, err := h.findCompita(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, computeErrorStatus(err), err.Error())
		return
	}
	if err := h.computeStore().SetConnection(p.ID, r.PathValue("name"), req); err != nil {
		code := http.StatusBadRequest
		if err == compute.ErrNotFound {
			code = http.StatusNotFound
		}
		writeJSONError(w, code, err.Error())
		return
	}
	h.handleConnectionsGet(w, r)
}

func (h *Handler) handleConnectionRemove(w http.ResponseWriter, r *http.Request) {
	p, _, err := h.findCompita(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, computeErrorStatus(err), err.Error())
		return
	}
	if err := h.computeStore().RemoveConnection(p.ID, r.PathValue("name")); err != nil {
		writeJSONError(w, computeErrorStatus(err), err.Error())
		return
	}
	h.handleConnectionsGet(w, r)
}
