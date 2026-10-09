package api

import (
	"net/http"

	"github.com/kayiik/compa/pkg/config"
	"github.com/kayiik/compa/pkg/modelservice"
)

func (h *Handler) handleListProviderRoster(w http.ResponseWriter, r *http.Request) {
	cfg, _ := config.LoadConfig(h.configPath)
	roster := modelservice.ListRoster(cfg)
	writeJSON(w, http.StatusOK, map[string]any{"providers": roster, "total": len(roster)})
}
