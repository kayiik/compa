package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/kayiik/compa/pkg/compute"
	"github.com/kayiik/compa/pkg/config"
)

// registerComputeRoutes binds the Compitas control-plane endpoints. Enroll is
// public by design: a machine that has no dashboard session proves itself
// with a one-time token (see isPublicLauncherDashboardPath).
func (h *Handler) registerComputeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/compute", h.handleComputeState)
	mux.HandleFunc("PUT /api/compute/enabled", h.handleComputeEnabled)
	mux.HandleFunc("POST /api/compute/refresh-local", h.handleComputeRefreshLocal)
	mux.HandleFunc("POST /api/compute/enroll", h.handleComputeEnroll)
	mux.HandleFunc("POST /api/computes", h.handleComputeAdd)
	mux.HandleFunc("POST /api/computes/{id}/token", h.handleComputeToken)
	mux.HandleFunc("DELETE /api/computes/{id}", h.handleComputeRemove)
	h.registerComputeRunRoutes(mux)
	mux.HandleFunc("POST /api/compitas", h.handleCompitaAdd)
	mux.HandleFunc("DELETE /api/compitas/{id}", h.handleCompitaRemove)
}

func (h *Handler) computeStore() *compute.Store {
	return compute.NewStore(config.GetHome())
}

// computeView is a compute as the dashboard sees it: no credential hash.
type computeView struct {
	ID           string               `json:"id"`
	Name         string               `json:"name"`
	Kind         compute.Kind         `json:"kind"`
	Mode         compute.Mode         `json:"mode,omitempty"`
	URL          string               `json:"url,omitempty"`
	Status       compute.Status       `json:"status"`
	Capabilities compute.Capabilities `json:"capabilities"`
	Isolations   []compute.Isolation  `json:"isolations"`
	CompitaCount int                  `json:"compita_count"`
	Online       bool                 `json:"online"`
}

type computeStateView struct {
	Enabled  bool              `json:"enabled"`
	Computes []computeView     `json:"computes"`
	Compitas []compute.Compita `json:"compitas"`
}

func buildComputeState(st compute.State) computeStateView {
	out := computeStateView{Enabled: st.Enabled, Computes: []computeView{}, Compitas: redactAll(st.Compitas)}
	for _, c := range st.Computes {
		v := computeView{
			ID: c.ID, Name: c.Name, Kind: c.Kind, Mode: c.Mode, URL: c.URL,
			Status: c.Status, Capabilities: c.Capabilities, Isolations: []compute.Isolation{},
		}
		v.Online = c.Kind == compute.KindLocal || computeBroker.Online(c.ID)
		for _, iso := range compute.Isolations {
			if c.Allows(iso) == nil {
				v.Isolations = append(v.Isolations, iso)
			}
		}
		for _, cp := range st.Compitas {
			if cp.ComputeID == c.ID {
				v.CompitaCount++
			}
		}
		out.Computes = append(out.Computes, v)
	}
	return out
}

func (h *Handler) writeComputeState(w http.ResponseWriter, status int) {
	st, err := h.computeStore().Load()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, status, buildComputeState(st))
}

func computeErrorStatus(err error) int {
	switch {
	case errors.Is(err, compute.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, compute.ErrInUse):
		return http.StatusConflict
	default:
		return http.StatusBadRequest
	}
}

func (h *Handler) handleComputeState(w http.ResponseWriter, _ *http.Request) {
	h.writeComputeState(w, http.StatusOK)
}

func (h *Handler) handleComputeEnabled(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.computeStore().SetEnabled(req.Enabled); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.writeComputeState(w, http.StatusOK)
}

func (h *Handler) handleComputeRefreshLocal(w http.ResponseWriter, _ *http.Request) {
	if err := h.computeStore().RefreshLocal(); err != nil {
		writeJSONError(w, computeErrorStatus(err), err.Error())
		return
	}
	h.writeComputeState(w, http.StatusOK)
}

// handleComputeEnroll redeems a one-time token. It is unauthenticated apart
// from the token, so every failure answers alike.
func (h *Handler) handleComputeEnroll(w http.ResponseWriter, r *http.Request) {
	var req compute.EnrollRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, credential, err := h.computeStore().Redeem(req.Token, req.Capabilities)
	if err != nil {
		if errors.Is(err, compute.ErrInvalidToken) {
			writeJSONError(w, http.StatusUnauthorized, compute.ErrInvalidToken.Error())
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "enrollment failed")
		return
	}
	writeJSON(w, http.StatusOK, compute.EnrollResponse{ComputeID: id, Credential: credential})
}

type computeAddResponse struct {
	Compute          computeView `json:"compute"`
	Token            string      `json:"token"`
	EnrollCommand    string      `json:"enroll_command"`
	ManualCommand    string      `json:"manual_command,omitempty"`
	ExpiresInMinutes int         `json:"expires_in_minutes"`
}

// enrollBase is the address the other machine reaches this Compa on: the one
// this request came in on unless the dashboard names another, such as a
// public hostname.
func enrollBase(r *http.Request, publicURL string) string {
	if base := strings.TrimSpace(publicURL); base != "" {
		return base
	}
	return requestBase(r)
}

// enrollCommand is the one line to run on the other machine: it fetches the
// kernel from this Compa, enrolls and keeps serving.
func enrollCommand(r *http.Request, publicURL, token string) string {
	return compute.InstallCommand(enrollBase(r, publicURL), token)
}

// manualCommand enrolls a machine that already has compa-kernel.
func manualCommand(r *http.Request, publicURL, token string) string {
	return fmt.Sprintf("compa-kernel compute enroll --url %s --token %s", shellQuote(enrollBase(r, publicURL)), shellQuote(token))
}

func (h *Handler) handleComputeAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string       `json:"name"`
		Mode      compute.Mode `json:"mode"`
		URL       string       `json:"url"`
		PublicURL string       `json:"public_url"`
		Pin       string       `json:"pin"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.PublicURL != "" {
		if u, err := url.Parse(req.PublicURL); err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			writeJSONError(w, http.StatusBadRequest, "public_url must be an http(s) address")
			return
		}
	}
	c, token, err := h.computeStore().AddRemote(req.Name, req.Mode, req.URL)
	if err != nil {
		writeJSONError(w, computeErrorStatus(err), err.Error())
		return
	}
	if req.Pin != "" {
		if err := h.computeStore().SetPin(c.ID, req.Pin); err != nil {
			_ = h.computeStore().RemoveCompute(c.ID)
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	st, _ := h.computeStore().Load()
	view := computeView{}
	for _, v := range buildComputeState(st).Computes {
		if v.ID == c.ID {
			view = v
		}
	}
	resp := computeAddResponse{
		Compute: view, Token: token,
		EnrollCommand:    enrollCommand(r, req.PublicURL, token),
		ManualCommand:    manualCommand(r, req.PublicURL, token),
		ExpiresInMinutes: int(compute.TokenTTL.Minutes()),
	}
	if c.Mode == compute.ModeDialIn {
		// Nothing to redeem: the machine just serves, and Compa dials it.
		resp.EnrollCommand = "compa-kernel compute serve --listen " + dialListenAddr(c.URL) + " --secret " + shellQuote(token) + tlsFlag(req.Pin)
		resp.ManualCommand = ""
		resp.ExpiresInMinutes = 0
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *Handler) handleComputeToken(w http.ResponseWriter, r *http.Request) {
	token, err := h.computeStore().ReissueToken(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, computeErrorStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":              token,
		"enroll_command":     enrollCommand(r, r.URL.Query().Get("public_url"), token),
		"manual_command":     manualCommand(r, r.URL.Query().Get("public_url"), token),
		"expires_in_minutes": int(compute.TokenTTL.Minutes()),
	})
}

func (h *Handler) handleComputeRemove(w http.ResponseWriter, r *http.Request) {
	if err := h.computeStore().RemoveCompute(r.PathValue("id")); err != nil {
		writeJSONError(w, computeErrorStatus(err), err.Error())
		return
	}
	h.writeComputeState(w, http.StatusOK)
}

func (h *Handler) handleCompitaAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string            `json:"name"`
		Description string            `json:"description"`
		ComputeID   string            `json:"compute_id"`
		Isolation   compute.Isolation `json:"isolation"`
		Approvals   string            `json:"approvals"`
		Browser     bool              `json:"browser"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !compute.ValidApprovals(req.Approvals) {
		writeJSONError(w, http.StatusBadRequest, "approvals must be careful or open")
		return
	}
	if req.Browser {
		// Refuse before creating anything, so a refused Compita is not left behind.
		if err := h.computeStore().CheckBrowser(req.ComputeID, req.Isolation); err != nil {
			writeJSONError(w, computeErrorStatus(err), err.Error())
			return
		}
	}
	added, err := h.computeStore().AddCompita(req.Name, req.Description, req.ComputeID, req.Isolation)
	if err != nil {
		writeJSONError(w, computeErrorStatus(err), err.Error())
		return
	}
	if req.Approvals != "" {
		if err := h.computeStore().SetCompitaApprovals(added.ID, req.Approvals); err != nil {
			writeJSONError(w, computeErrorStatus(err), err.Error())
			return
		}
	}
	if req.Browser {
		if err := h.computeStore().SetCompitaBrowser(added.ID, true); err != nil {
			writeJSONError(w, computeErrorStatus(err), err.Error())
			return
		}
	}
	h.writeComputeState(w, http.StatusCreated)
}

func (h *Handler) handleCompitaRemove(w http.ResponseWriter, r *http.Request) {
	if err := h.computeStore().RemoveCompita(r.PathValue("id")); err != nil {
		writeJSONError(w, computeErrorStatus(err), err.Error())
		return
	}
	h.writeComputeState(w, http.StatusOK)
}

// dialListenAddr turns the URL Compa will call into a --listen address.
func dialListenAddr(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Port() == "" {
		return ":8787"
	}
	return ":" + u.Port()
}

func tlsFlag(pin string) string {
	if pin == "" {
		return ""
	}
	return " --tls"
}
