package api

import (
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/kayiik/compa/pkg/config"
	"github.com/kayiik/compa/pkg/modelservice"
)

type providerInstanceCatalogResponse = modelservice.InstanceCatalog

type providerTargetResponse = modelservice.ProviderTarget

func (h *Handler) registerProviderRouteRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/model-routes", h.handleListProviderRoutes)
	mux.HandleFunc("POST /api/model-routes", h.handleCreateProviderRoute)
	mux.HandleFunc("PUT /api/model-routes/{name}", h.handleUpdateProviderRoute)
	mux.HandleFunc("DELETE /api/model-routes/{name}", h.handleDeleteProviderRoute)
	mux.HandleFunc("GET /api/active-models", h.handleListActiveModels)
	mux.HandleFunc("POST /api/active-models", h.handleSetActiveModels)
	mux.HandleFunc("POST /api/active-models/add", h.handleAddActiveModel)
	mux.HandleFunc("POST /api/active-models/remove", h.handleRemoveActiveModel)
}

func (h *Handler) handleListProviderInstanceCatalogs(w http.ResponseWriter, r *http.Request) {
	cfg, store, err := h.loadProviderProjectionState()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	catalogs := instanceCatalogResponses(cfg, store)
	writeJSON(w, http.StatusOK, map[string]any{"catalogs": catalogs, "total": len(catalogs)})
}

func (h *Handler) handleListProviderTargets(w http.ResponseWriter, r *http.Request) {
	cfg, store, err := h.loadProviderProjectionState()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	allTargets := providerTargetResponses(cfg, store)

	// Chat receives the active shortlist by default. Model-management callers
	// use all=true to enumerate every discovered target.
	if r.URL.Query().Get("all") == "true" {
		writeJSON(w, http.StatusOK, map[string]any{"targets": allTargets, "total": len(allTargets)})
		return
	}
	filtered := modelservice.ChatShortlistTargets(cfg, allTargets)
	writeJSON(w, http.StatusOK, map[string]any{"targets": filtered, "total": len(filtered)})
}

func (h *Handler) handleListActiveModels(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	active := canonicalActiveModels(cfg.ActiveModels)
	writeJSON(w, http.StatusOK, map[string]any{"active_models": active, "total": len(active)})
}

func (h *Handler) handleSetActiveModels(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Models []string `json:"models"`
	}
	if err := decodeStrictJSON(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	models, err := validateActiveModels(req.Models)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.configMu.Lock()
	defer h.configMu.Unlock()
	cfg, store, err := h.loadProviderProjectionState()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := validateActiveModelsAgainstTargets(cfg, store, models); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// The first model the shortlist gains becomes the default model when
	// none is set.
	previous := canonicalActiveModels(cfg.ActiveModels)
	for _, model := range models {
		if !slices.Contains(previous, model) {
			modelservice.AdoptDefaultModel(cfg, model)
			break
		}
	}
	cfg.ActiveModels = models
	if err := config.SaveConfig(h.configPath, cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"active_models": cfg.ActiveModels, "default_model": cfg.Agents.Defaults.ModelName, "status": "ok",
	})
}

func (h *Handler) handleAddActiveModel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Target string `json:"target"`
	}
	if err := decodeStrictJSON(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	normalized, err := validateActiveModels([]string{req.Target})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(normalized) != 1 {
		http.Error(w, "target is required", http.StatusBadRequest)
		return
	}
	target := normalized[0]
	h.configMu.Lock()
	defer h.configMu.Unlock()
	cfg, store, err := h.loadProviderProjectionState()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := validateActiveModelsAgainstTargets(cfg, store, normalized); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	current := canonicalActiveModels(cfg.ActiveModels)
	for _, m := range current {
		if m == target {
			writeJSON(w, http.StatusOK, map[string]any{
				"active_models": current, "default_model": cfg.Agents.Defaults.ModelName, "status": "ok",
			})
			return
		}
	}
	cfg.ActiveModels = append(current, target)
	// The model added becomes the default model when none is set.
	modelservice.AdoptDefaultModel(cfg, target)
	if err := config.SaveConfig(h.configPath, cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"active_models": cfg.ActiveModels, "default_model": cfg.Agents.Defaults.ModelName, "status": "ok",
	})
}

func (h *Handler) handleRemoveActiveModel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Target string `json:"target"`
	}
	if err := decodeStrictJSON(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	target := strings.TrimSpace(req.Target)
	if target == "" {
		http.Error(w, "target is required", http.StatusBadRequest)
		return
	}
	h.configMu.Lock()
	defer h.configMu.Unlock()
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	updated := make([]string, 0, len(cfg.ActiveModels))
	current := canonicalActiveModels(cfg.ActiveModels)
	for _, m := range current {
		if m != target {
			updated = append(updated, m)
		}
	}
	cfg.ActiveModels = updated
	if err := config.SaveConfig(h.configPath, cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"active_models": cfg.ActiveModels, "status": "ok"})
}

func canonicalActiveModels(models []string) []string {
	normalized := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, raw := range models {
		target := strings.TrimSpace(raw)
		if target == "" {
			continue
		}
		if _, err := config.ParseExactModelTarget(target); err != nil {
			continue
		}
		if _, exists := seen[target]; exists {
			continue
		}
		seen[target] = struct{}{}
		normalized = append(normalized, target)
	}
	return normalized
}

func validateActiveModels(models []string) ([]string, error) {
	for _, raw := range models {
		target := strings.TrimSpace(raw)
		if target == "" {
			continue
		}
		if _, err := config.ParseExactModelTarget(target); err != nil {
			return nil, fmt.Errorf("invalid active model %q: %w", target, err)
		}
	}
	return canonicalActiveModels(models), nil
}

func validateActiveModelsAgainstTargets(cfg *config.Config, store *CatalogStore, models []string) error {
	available := make(map[string]providerTargetResponse)
	for _, target := range providerTargetResponses(cfg, store) {
		available[target.Target] = target
	}
	for _, target := range models {
		found, ok := available[target]
		if !ok {
			return fmt.Errorf("active model %q is not present in an enabled instance-owned catalog", target)
		}
		if !modelservice.ServesChat(found.Surfaces) {
			return fmt.Errorf("active model %q does not serve chat (its catalog lists %s)", target, strings.Join(found.Surfaces, ", "))
		}
	}
	return nil
}

func (h *Handler) loadProviderProjectionState() (*config.Config, *CatalogStore, error) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to load config: %w", err)
	}
	store, err := loadCatalogs()
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to load catalogs: %w", err)
	}
	return cfg, store, nil
}

func instanceCatalogResponses(cfg *config.Config, store *CatalogStore) []providerInstanceCatalogResponse {
	return modelservice.ListInstanceCatalogs(cfg, store)
}

func providerTargetResponses(cfg *config.Config, store *CatalogStore) []providerTargetResponse {
	return modelservice.ListProviderTargets(cfg, store)
}

func providerInstancesByID(cfg *config.Config) map[string]*config.ProviderInstanceConfig {
	instances := make(map[string]*config.ProviderInstanceConfig, len(cfg.ProviderInstances))
	for _, instance := range cfg.ProviderInstances {
		if instance != nil {
			instances[instance.ID] = instance
		}
	}
	return instances
}

func (h *Handler) handleListProviderRoutes(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	routes := cloneSortedRoutes(cfg.ModelRoutes)
	writeJSON(w, http.StatusOK, map[string]any{"routes": routes, "total": len(routes)})
}

func (h *Handler) handleCreateProviderRoute(w http.ResponseWriter, r *http.Request) {
	var route config.ModelRouteConfig
	if err := decodeStrictJSON(r, &route); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}
	h.configMu.Lock()
	defer h.configMu.Unlock()
	cfg, store, err := h.loadProviderProjectionState()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if providerRouteIndex(cfg.ModelRoutes, route.Name) >= 0 {
		http.Error(w, "model route already exists", http.StatusConflict)
		return
	}
	if err := validateProviderRouteAgainstTargets(cfg, store, &route); err != nil {
		http.Error(w, fmt.Sprintf("Validation error: %v", err), http.StatusBadRequest)
		return
	}
	cfg.ModelRoutes = append(cfg.ModelRoutes, &route)
	if err := config.SaveConfig(h.configPath, cfg); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save config: %v", err), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "route": &route})
}

func (h *Handler) handleUpdateProviderRoute(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var route config.ModelRouteConfig
	if err := decodeStrictJSON(r, &route); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}
	if route.Name != name {
		http.Error(w, "model route name is immutable and must match the request path", http.StatusBadRequest)
		return
	}
	h.configMu.Lock()
	defer h.configMu.Unlock()
	cfg, store, err := h.loadProviderProjectionState()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	index := providerRouteIndex(cfg.ModelRoutes, name)
	if index < 0 {
		http.Error(w, "model route not found", http.StatusNotFound)
		return
	}
	if err := validateProviderRouteAgainstTargets(cfg, store, &route); err != nil {
		http.Error(w, fmt.Sprintf("Validation error: %v", err), http.StatusBadRequest)
		return
	}
	cfg.ModelRoutes[index] = &route
	if err := config.SaveConfig(h.configPath, cfg); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save config: %v", err), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "route": &route})
}

func (h *Handler) handleDeleteProviderRoute(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	h.configMu.Lock()
	defer h.configMu.Unlock()
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	index := providerRouteIndex(cfg.ModelRoutes, name)
	if index < 0 {
		http.Error(w, "model route not found", http.StatusNotFound)
		return
	}
	cfg.ModelRoutes = append(cfg.ModelRoutes[:index], cfg.ModelRoutes[index+1:]...)
	clearRouteSelections(cfg, name)
	if err := config.SaveConfig(h.configPath, cfg); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save config: %v", err), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// clearRouteSelections clears each model selection that names the route —
// the default, image and light models and each agent's model — as
// modelservice.DropTargets clears one naming a route it removes.
func clearRouteSelections(cfg *config.Config, route string) {
	clearIfRoute := func(selection *string) {
		if strings.TrimSpace(*selection) == route {
			*selection = ""
		}
	}
	defaults := &cfg.Agents.Defaults
	clearIfRoute(&defaults.ModelName)
	clearIfRoute(&defaults.ImageModel)
	if defaults.Routing != nil {
		clearIfRoute(&defaults.Routing.LightModel)
	}
	for i := range cfg.Agents.List {
		clearIfRoute(&cfg.Agents.List[i].Model)
	}
}

func validateProviderRouteAgainstTargets(cfg *config.Config, store *CatalogStore, route *config.ModelRouteConfig) error {
	clone := *cfg
	clone.ModelRoutes = []*config.ModelRouteConfig{route}
	if err := clone.ValidateProviderInstances(); err != nil {
		return err
	}
	validTargets := make(map[string]providerTargetResponse)
	for _, target := range providerTargetResponses(cfg, store) {
		validTargets[target.Target] = target
	}
	for _, target := range route.Targets {
		found, ok := validTargets[target]
		if !ok {
			return fmt.Errorf("target %q is not present in an enabled instance-owned catalog", target)
		}
		if !modelservice.ServesChat(found.Surfaces) {
			return fmt.Errorf("target %q does not serve chat (its catalog lists %s)", target, strings.Join(found.Surfaces, ", "))
		}
	}
	return nil
}

func providerRouteIndex(routes []*config.ModelRouteConfig, name string) int {
	for i, route := range routes {
		if route != nil && route.Name == name {
			return i
		}
	}
	return -1
}

func cloneSortedRoutes(routes []*config.ModelRouteConfig) []*config.ModelRouteConfig {
	clones := make([]*config.ModelRouteConfig, 0, len(routes))
	for _, route := range routes {
		if route == nil {
			continue
		}
		clone := *route
		clone.Targets = append([]string(nil), route.Targets...)
		clones = append(clones, &clone)
	}
	sort.Slice(clones, func(i, j int) bool { return clones[i].Name < clones[j].Name })
	return clones
}
