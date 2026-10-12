package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/kayiik/compa/web/backend/api"
	"github.com/kayiik/compa/web/backend/launcherconfig"
)

// Options for running Compa without anyone at its console, as in a
// container: the first dashboard password from a file, such as a Docker or
// Kubernetes secret, and the extra names the dashboard answers to.
const (
	envDashboardPasswordFile = "COMPA_DASHBOARD_PASSWORD_FILE"
	envAllowedHosts          = "COMPA_ALLOWED_HOSTS"
)

// initialPasswordFromFile sets the first dashboard password from the file
// named by path, while no password is set. It reports whether it set one. A
// password set already, in the browser or with -password, is left as it is,
// so the file only ever seeds the first one.
func initialPasswordFromFile(ctx context.Context, store api.PasswordStore, path string) (bool, error) {
	path = strings.TrimSpace(path)
	if path == "" || store == nil {
		return false, nil
	}
	initialized, err := store.IsInitialized(ctx)
	if err != nil {
		return false, fmt.Errorf("check the dashboard password: %w", err)
	}
	if initialized {
		return false, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", envDashboardPasswordFile, err)
	}
	password := strings.TrimSpace(string(raw))
	if err := api.ValidateDashboardPassword(password); err != nil {
		return false, fmt.Errorf("the password in %s: %w", envDashboardPasswordFile, err)
	}
	return store.InitializePassword(ctx, password)
}

// allowedHosts joins the saved allowed hosts with those given with
// -allowed-hosts and COMPA_ALLOWED_HOSTS, comma-separated lists. The added
// ones hold only while Compa runs; they are never saved.
func allowedHosts(saved []string, extra ...string) []string {
	hosts := append([]string(nil), saved...)
	for _, list := range extra {
		hosts = append(hosts, strings.Split(list, ",")...)
	}
	return launcherconfig.NormalizeHosts(hosts)
}
