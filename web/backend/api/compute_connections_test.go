package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kayiik/compa/pkg/compute"
	"github.com/kayiik/compa/pkg/config"
)

func TestConnectionsAreMadeForOneCompitaAndTheirSecretsStayOnTheHost(t *testing.T) {
	skipWithoutShell(t)
	mux := computeMux(t)
	home := config.GetHome()
	// The kernel only has to run: it records the config it was given.
	dir := t.TempDir()
	kernel := filepath.Join(dir, "kernel")
	if err := os.WriteFile(kernel, []byte("#!/bin/sh\ncp \"$COMPA_HOME/config.json\" "+dir+"/seen.json\necho ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvBinary, kernel)
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(`{"agents":{"defaults":{"model_name":"x/y"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	computeCall(mux, "PUT", "/api/compute/enabled", `{"enabled":true}`)
	computeCall(mux, "POST", "/api/compitas", `{"name":"Ana","compute_id":"this-computer","isolation":"shared","approvals":"open"}`)
	computeCall(mux, "POST", "/api/compitas", `{"name":"Bo","compute_id":"this-computer","isolation":"shared","approvals":"open"}`)

	if rec := computeCall(mux, "PUT", "/api/compitas/ana/connections/browser", `{"command":"x"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("reserved name = %d", rec.Code)
	}
	if rec := computeCall(mux, "PUT", "/api/compitas/ana/connections/github", `{"command":""}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("no command = %d", rec.Code)
	}
	if rec := computeCall(mux, "PUT", "/api/compitas/nobody/connections/github", `{"command":"gh"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown Compita = %d", rec.Code)
	}
	rec := computeCall(mux, "PUT", "/api/compitas/ana/connections/github", `{"command":"gh-mcp","args":["--ro"],"env":{"GITHUB_TOKEN":"ghp_s3cret"}}`)
	var list struct {
		Connections []connectionView `json:"connections"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if rec.Code != http.StatusOK || len(list.Connections) != 1 || list.Connections[0].EnvKeys[0] != "GITHUB_TOKEN" {
		t.Fatalf("set = %d %s", rec.Code, rec.Body)
	}
	for _, path := range []string{"/api/compitas/ana/connections", "/api/compute"} {
		if body := computeCall(mux, "GET", path, "").Body.String(); strings.Contains(body, "ghp_s3cret") {
			t.Fatalf("GET %s leaked the secret: %s", path, body)
		}
	}

	// Only Ana's turns get the connection, with its secret, in her own config.
	computeCall(mux, "POST", "/api/compitas/ana/chat", `{"message":"hi"}`)
	seen, _ := os.ReadFile(filepath.Join(dir, "seen.json"))
	if !strings.Contains(string(seen), `"gh-mcp"`) || !strings.Contains(string(seen), "ghp_s3cret") {
		t.Fatalf("Ana's config lacks her connection: %s", seen)
	}
	_ = os.Remove(filepath.Join(dir, "seen.json"))
	computeCall(mux, "POST", "/api/compitas/bo/chat", `{"message":"hi"}`)
	if seen, _ := os.ReadFile(filepath.Join(dir, "seen.json")); strings.Contains(string(seen), "gh-mcp") || strings.Contains(string(seen), "ghp_s3cret") {
		t.Fatalf("Bo must not get Ana's connection: %s", seen)
	}

	if rec := computeCall(mux, "DELETE", "/api/compitas/ana/connections/github", ""); rec.Code != http.StatusOK {
		t.Fatalf("delete = %d", rec.Code)
	}
	if rec := computeCall(mux, "DELETE", "/api/compitas/ana/connections/github", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("delete twice = %d", rec.Code)
	}
	if st, _ := compute.NewStore(home).Load(); len(st.Compitas[0].Connections) != 0 {
		t.Fatalf("connections = %v", st.Compitas[0].Connections)
	}
}
