package compute

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// In a container the Compita's directory is /data, a path of the container's
// own: it must not take the separator of the machine that starts the container,
// as it would on Windows.
func TestContainerCompitasAreConfiguredWithPathsInsideTheContainer(t *testing.T) {
	r := Runner{Home: t.TempDir()}
	if err := os.WriteFile(filepath.Join(r.Home, "config.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dir, err := r.prepare(Turn{CompitaID: "ana", Isolation: IsolationContainer, Session: "s", Message: "hi", Browser: true})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Agents struct {
			Defaults struct {
				Workspace string `json:"workspace"`
			} `json:"defaults"`
		} `json:"agents"`
		Tools struct {
			MCP struct {
				Servers map[string]struct {
					Args []string          `json:"args"`
					Env  map[string]string `json:"env"`
				} `json:"servers"`
			} `json:"mcp"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if got := cfg.Agents.Defaults.Workspace; got != "/data/workspace" {
		t.Fatalf("workspace = %q, want /data/workspace", got)
	}
	browser := cfg.Tools.MCP.Servers[BrowserServerName]
	args := strings.Join(browser.Args, " ")
	if !strings.Contains(args, "--user-data-dir /data/browser-profile") || !strings.Contains(args, "--output-dir /data/browser-output") {
		t.Fatalf("browser paths = %s", args)
	}
	if browser.Env["HOME"] != "/data/browser-home" {
		t.Fatalf("browser HOME = %q", browser.Env["HOME"])
	}
}
