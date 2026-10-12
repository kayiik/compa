package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/kayiik/compa/pkg/config"
	"github.com/kayiik/compa/pkg/modelservice"
)

// ProviderCatalogSyncInput is the complete server-owned connection description
// supplied to a catalog driver. It is never populated from request fields.
type ProviderCatalogSyncInput = modelservice.ProviderCatalogSyncInput

// providerInstanceResponse is the public view of a provider instance: its
// credential reference, headers and settings are write-only, so only whether
// they are set shows. Runtime is the instance's request settings, omitted
// when it runs with the provider defaults.
type providerInstanceResponse struct {
	ID string `json:"id"`
	// DisplayName is the name to show for the instance: an extension
	// instance's provider as the daemon names it, the core registry's label
	// of a registry provider kind, else the id.
	DisplayName string `json:"display_name"`
	// ManagedBy is "extension" for an instance that mirrors a provider the
	// extension daemon serves, else "".
	ManagedBy    string `json:"managed_by"`
	ProviderKind string `json:"provider_kind"`
	Adapter      string `json:"adapter"`
	Protocol     string `json:"protocol"`
	Endpoint     string `json:"endpoint,omitempty"`
	// CredentialKind is what the instance signs in with: "none", "api_key",
	// "token" or "oauth".
	CredentialKind string `json:"credential_kind"`
	// CredentialReady reports that the credential is in place - stored or
	// signed in - or that none is needed.
	CredentialReady bool                            `json:"credential_ready"`
	AuthConfigured  bool                            `json:"auth_configured"`
	HeaderNames     []string                        `json:"header_names"`
	SettingNames    []string                        `json:"setting_names"`
	Runtime         *config.ProviderInstanceRuntime `json:"runtime,omitempty"`
	State           config.ProviderInstanceState    `json:"state"`
}

type providerInstanceWriteOnly struct {
	AuthConnectionRef *string            `json:"auth_connection_ref,omitempty"`
	APIKey            *string            `json:"api_key,omitempty"`
	Headers           *map[string]string `json:"headers,omitempty"`
	Settings          *map[string]any    `json:"settings,omitempty"`
}

// providerInstanceRequestBody creates or updates a provider instance.
// Runtime sets the instance's request settings: {} resets them to the
// provider defaults, and an update that omits runtime keeps them.
type providerInstanceRequestBody struct {
	ID            string                          `json:"id"`
	ProviderKind  string                          `json:"provider_kind"`
	Adapter       string                          `json:"adapter"`
	Protocol      string                          `json:"protocol"`
	Endpoint      string                          `json:"endpoint,omitempty"`
	Runtime       *config.ProviderInstanceRuntime `json:"runtime,omitempty"`
	State         config.ProviderInstanceState    `json:"state"`
	WriteOnly     *providerInstanceWriteOnly      `json:"write_only,omitempty"`
	ClearAuth     bool                            `json:"clear_auth_connection,omitempty"`
	ClearHeaders  bool                            `json:"clear_headers,omitempty"`
	ClearSettings bool                            `json:"clear_settings,omitempty"`
}

func (h *Handler) registerProviderInstanceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/provider-instances", h.handleListProviderInstances)
	mux.HandleFunc("POST /api/provider-instances", h.handleCreateProviderInstance)
	mux.HandleFunc("POST /api/provider-instances/auto-connect-free", h.handleAutoConnectFreeProviders)
	mux.HandleFunc("PUT /api/provider-instances/{id}", h.handleUpdateProviderInstance)
	mux.HandleFunc("DELETE /api/provider-instances/{id}", h.handleDeleteProviderInstance)
	mux.HandleFunc("POST /api/provider-instances/{id}/catalog/sync", h.handleSyncProviderInstanceCatalog)
	mux.HandleFunc("POST /api/provider-instances/{id}/ping", h.handlePingProviderInstance)
	mux.HandleFunc("GET /api/provider-instances/catalogs", h.handleListProviderInstanceCatalogs)
	mux.HandleFunc("GET /api/provider-targets", h.handleListProviderTargets)
	mux.HandleFunc("GET /api/provider-roster", h.handleListProviderRoster)
	h.registerProviderRouteRoutes(mux)
}

func decodeStrictJSON(r *http.Request, dst any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("request must contain one JSON value")
		}
		return err
	}
	return nil
}

func (h *Handler) handleListProviderInstances(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	instances := make([]providerInstanceResponse, 0, len(cfg.ProviderInstances))
	for _, instance := range cfg.ProviderInstances {
		if instance != nil {
			instances = append(instances, h.providerInstanceView(instance))
		}
	}
	sort.Slice(instances, func(i, j int) bool { return instances[i].ID < instances[j].ID })
	writeJSON(w, http.StatusOK, map[string]any{"instances": instances, "total": len(instances)})
}

// input is the modelservice.InstanceInput the request describes.
func (request providerInstanceRequestBody) input() modelservice.InstanceInput {
	input := modelservice.InstanceInput{
		ID: request.ID, ProviderKind: request.ProviderKind, Adapter: request.Adapter,
		Protocol: request.Protocol, Endpoint: request.Endpoint, Runtime: request.Runtime, State: request.State,
		ClearAuth: request.ClearAuth, ClearHeaders: request.ClearHeaders, ClearSettings: request.ClearSettings,
	}
	if request.WriteOnly != nil {
		input.AuthConnectionRef = request.WriteOnly.AuthConnectionRef
		input.APIKey = request.WriteOnly.APIKey
		input.Headers = request.WriteOnly.Headers
		input.Settings = request.WriteOnly.Settings
	}
	return input
}

// instanceDeps are the effects of the instance operations: the auth store,
// and the handler's credential resolver and catalog sync driver.
func (h *Handler) instanceDeps() modelservice.InstanceDeps {
	return modelservice.InstanceDeps{ResolveCredential: h.providerCredentialResolver, SyncCatalog: h.providerCatalogSync}
}

// writeInstanceError answers with the status of a modelservice instance
// error, and its message.
func writeInstanceError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, modelservice.ErrInstanceInvalid), errors.Is(err, modelservice.ErrInstanceUnsupported):
		status = http.StatusBadRequest
	case errors.Is(err, modelservice.ErrInstanceNotFound):
		status = http.StatusNotFound
	case errors.Is(err, modelservice.ErrInstanceConflict), errors.Is(err, modelservice.ErrInstanceDisabled):
		status = http.StatusConflict
	case errors.Is(err, modelservice.ErrInstanceUpstream):
		status = http.StatusBadGateway
	case errors.Is(err, modelservice.ErrInstanceNotConfigured):
		status = http.StatusNotImplemented
	}
	http.Error(w, err.Error(), status)
}

func (h *Handler) handleCreateProviderInstance(w http.ResponseWriter, r *http.Request) {
	var request providerInstanceRequestBody
	if err := decodeStrictJSON(r, &request); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}
	h.configMu.Lock()
	defer h.configMu.Unlock()
	instance, err := modelservice.CreateInstance(h.configPath, request.input(), h.instanceDeps())
	if err != nil {
		writeInstanceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "instance": h.providerInstanceView(instance)})
}

func (h *Handler) handleUpdateProviderInstance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var request providerInstanceRequestBody
	if err := decodeStrictJSON(r, &request); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}
	h.configMu.Lock()
	defer h.configMu.Unlock()
	instance, err := modelservice.UpdateInstance(h.configPath, id, request.input(), h.instanceDeps())
	if err != nil {
		writeInstanceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "instance": h.providerInstanceView(instance)})
}

func (h *Handler) handleDeleteProviderInstance(w http.ResponseWriter, r *http.Request) {
	h.configMu.Lock()
	defer h.configMu.Unlock()
	if err := modelservice.DeleteInstance(h.configPath, r.PathValue("id"), h.instanceDeps()); err != nil {
		writeInstanceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleSyncProviderInstanceCatalog(w http.ResponseWriter, r *http.Request) {
	h.configMu.Lock()
	defer h.configMu.Unlock()
	var request struct{}
	if err := decodeStrictJSON(r, &request); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}
	result, err := modelservice.SyncInstanceCatalog(r.Context(), h.configPath, r.PathValue("id"), h.instanceDeps())
	if err != nil {
		writeInstanceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) handlePingProviderInstance(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	index := providerInstanceIndex(cfg, r.PathValue("id"))
	if index < 0 {
		http.Error(w, "provider instance not found", http.StatusNotFound)
		return
	}
	instance := cfg.ProviderInstances[index]

	secret := ""
	if h.providerCredentialResolver != nil && instance.AuthConnectionRef != "" {
		secret, _ = h.providerCredentialResolver(instance.AuthConnectionRef)
	}
	res := modelservice.PingWithSync(r.Context(), instance, secret, h.providerCatalogHTTPClient, h.providerCatalogSync)
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) handleAutoConnectFreeProviders(w http.ResponseWriter, r *http.Request) {
	h.configMu.Lock()
	defer h.configMu.Unlock()
	result, err := modelservice.AutoConnectFreeAndSave(r.Context(), h.configPath, h.providerAnonymousVerify)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to auto-connect free providers: %v", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                 result.OK,
		"total":              result.Total,
		"catalog_discovered": result.CatalogDiscovered,
		"verified":           result.Verified,
		"instances":          result.Instances,
		"outcomes":           result.Outcomes,
		"default_model":      result.DefaultModel,
	})
}

func providerInstanceIndex(cfg *config.Config, id string) int {
	return modelservice.InstanceIndex(cfg, id)
}

func catalogSyncInputFromInstance(instance *config.ProviderInstanceConfig) ProviderCatalogSyncInput {
	return modelservice.CatalogSyncInputFromInstance(instance)
}

func cloneStringValues(source map[string]string) map[string]string {
	return modelservice.CloneStringMap(source)
}

func cloneAnyValues(source map[string]any) map[string]any {
	return modelservice.CloneAnyMap(source)
}

func (h *Handler) providerInstanceView(instance *config.ProviderInstanceConfig) providerInstanceResponse {
	headerNames := modelservice.SortedKeys(instance.Headers)
	settingNames := modelservice.SortedKeys(instance.Settings)
	credentialKind, credentialReady := h.providerInstanceCredential(instance)
	return providerInstanceResponse{
		ID:              instance.ID,
		DisplayName:     providerInstanceDisplayName(instance),
		ManagedBy:       providerInstanceManagedBy(instance),
		ProviderKind:    instance.ProviderKind,
		Adapter:         instance.Adapter,
		Protocol:        instance.Protocol,
		Endpoint:        instance.Endpoint,
		CredentialKind:  credentialKind,
		CredentialReady: credentialReady,
		AuthConfigured:  strings.TrimSpace(instance.AuthConnectionRef) != "",
		HeaderNames:     headerNames,
		SettingNames:    settingNames,
		Runtime:         redactProviderInstanceRuntime(instance.Runtime),
		State:           instance.State,
	}
}
