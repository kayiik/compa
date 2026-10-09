package compute

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateConnection(t *testing.T) {
	ok := Connection{Command: "npx", Args: []string{"-y", "x"}, Env: map[string]string{"TOKEN": "s"}}
	for name, c := range map[string]Connection{
		"github": ok, "g_1": ok,
	} {
		if err := ValidateConnection(name, c); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, bad := range []struct {
		name string
		c    Connection
	}{
		{"", ok}, {"GitHub", ok}, {"1x", ok}, {"has-dash", ok}, {strings.Repeat("a", 33), ok},
		{BrowserServerName, ok},
		{"x", Connection{}}, {"x", Connection{Command: "  "}},
		{"x", Connection{Command: "c", Env: map[string]string{"bad name": "v"}}},
		{"x", Connection{Command: "c", Env: map[string]string{"A": strings.Repeat("v", maxEnvValueLen+1)}}},
		{"x", Connection{Command: "c", Args: make([]string, maxArgs+1)}},
	} {
		if err := ValidateConnection(bad.name, bad.c); err == nil {
			t.Errorf("%q %+v should be refused", bad.name, bad.c)
		}
	}
}

func TestConnectionsKeepSecretsTheDashboardNeverSees(t *testing.T) {
	s := testStore(t, false)
	if err := s.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	p, _ := s.AddCompita("Ana", "", LocalID, IsolationShared)
	if err := s.SetConnection(p.ID, "github", Connection{Command: "gh-mcp", Env: map[string]string{"TOKEN": "s3cret", "ORG": "acme"}}); err != nil {
		t.Fatal(err)
	}
	// Editing without restating a secret keeps it; a new value replaces it.
	if err := s.SetConnection(p.ID, "github", Connection{Command: "gh-mcp2", Env: map[string]string{"TOKEN": "", "ORG": "newco"}}); err != nil {
		t.Fatal(err)
	}
	st, _ := s.Load()
	got := st.Compitas[0].Connections["github"]
	if got.Command != "gh-mcp2" || got.Env["TOKEN"] != "s3cret" || got.Env["ORG"] != "newco" {
		t.Fatalf("connection = %+v", got)
	}
	raw, _ := json.Marshal(st.Compitas[0].Redacted())
	if strings.Contains(string(raw), "s3cret") || strings.Contains(string(raw), "newco") || !strings.Contains(string(raw), `"TOKEN":""`) {
		t.Fatalf("the dashboard view must carry names, not values: %s", raw)
	}
	if st.Compitas[0].Connections["github"].Env["TOKEN"] != "s3cret" {
		t.Fatal("redacting must not touch the stored connection")
	}

	for i := 0; i < maxConnections; i++ {
		_ = s.SetConnection(p.ID, "c"+string(rune('a'+i)), Connection{Command: "x"})
	}
	if err := s.SetConnection(p.ID, "onetoomany", Connection{Command: "x"}); err == nil {
		t.Fatal("too many connections")
	}
	if err := s.RemoveConnection(p.ID, "github"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveConnection(p.ID, "github"); err != ErrNotFound {
		t.Fatalf("removing twice = %v", err)
	}
	if err := s.SetConnection("nobody", "x", Connection{Command: "c"}); err != ErrNotFound {
		t.Fatalf("unknown Compita = %v", err)
	}
}

func TestCompitaConfigCarriesItsConnectionsAsTrustedServers(t *testing.T) {
	out, err := buildCompitaConfig([]byte(`{}`), configPatch{
		Connections: map[string]Connection{"github": {Command: "gh-mcp", Args: []string{"--ro"}, Env: map[string]string{"TOKEN": "s3cret"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	_ = json.Unmarshal(out, &cfg)
	mcp := cfg["tools"].(map[string]any)["mcp"].(map[string]any)
	gh := mcp["servers"].(map[string]any)["github"].(map[string]any)
	if mcp["enabled"] != true || gh["command"] != "gh-mcp" || gh["trusted"] != true || gh["env"].(map[string]any)["TOKEN"] != "s3cret" || gh["args"].([]any)[0] != "--ro" {
		t.Fatalf("server = %v", gh)
	}
}
