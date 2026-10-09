package compute

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const hostConfig = `{
  "agents": {"defaults": {"model_name": "x/y"}},
  "provider_instances": [{"id": "x"}],
  "channel_list": {"telegram": {"token": "SECRET"}},
  "hooks": {"enabled": true, "processes": {"mine": {"enabled": true, "command": ["/host/only"]}}},
  "tools": {"approval": {"rules": [{"tool": "install_skill", "action": "ask"}]}},
  "some_future_key": 7
}`

func build(t *testing.T, patch configPatch) map[string]any {
	t.Helper()
	out, err := buildCompitaConfig([]byte(hostConfig), patch)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func rulesOf(cfg map[string]any) []map[string]any {
	var out []map[string]any
	for _, r := range cfg["tools"].(map[string]any)["approval"].(map[string]any)["rules"].([]any) {
		out = append(out, r.(map[string]any))
	}
	return out
}

func TestCompitaConfigKeepsModelsDropsChannelsAndHostHooks(t *testing.T) {
	cfg := build(t, configPatch{HookCommand: []string{"/k/compa-kernel", "compute", "approver"}})
	if cfg["agents"] == nil || cfg["provider_instances"] == nil || cfg["some_future_key"] == nil {
		t.Fatalf("model setup and unknown keys must stay: %v", cfg)
	}
	if _, ok := cfg["channel_list"]; ok {
		t.Fatal("chat-app credentials must not reach a Compita")
	}
	procs := cfg["hooks"].(map[string]any)["processes"].(map[string]any)
	if len(procs) != 1 || procs[ApproverHookName] == nil {
		t.Fatalf("only the approver hook may run: %v", procs)
	}
	hook := procs[ApproverHookName].(map[string]any)
	if hook["intercept"].([]any)[0] != "approve_tool" || hook["command"].([]any)[2] != "approver" {
		t.Fatalf("hook = %v", hook)
	}
	if ms := cfg["hooks"].(map[string]any)["defaults"].(map[string]any)["approval_timeout_ms"].(float64); ms < 600000 {
		t.Fatalf("an approval must be allowed to wait for the owner: %v ms", ms)
	}
}

func TestCompitaPolicyPresets(t *testing.T) {
	careful := rulesOf(build(t, configPatch{HookCommand: []string{"k"}}))
	if len(careful) != 3 || careful[0]["action"] != "ask" || careful[1]["tool"] != "exec" || careful[2]["tool"] != "install_skill" {
		t.Fatalf("careful on a machine asks about risky tools and exec, then the host rules: %v", careful)
	}
	boxed := rulesOf(build(t, configPatch{Container: true, HookCommand: []string{"k"}}))
	if len(boxed) != 2 || boxed[0]["tool"] != nil {
		t.Fatalf("exec inside a container needs no ask: %v", boxed)
	}
	open := rulesOf(build(t, configPatch{Approvals: ApprovalsOpen}))
	if len(open) != 1 || open[0]["tool"] != "install_skill" {
		t.Fatalf("open leaves the host policy alone: %v", open)
	}
	// With no hook there is nobody to ask: the policy still asks, so the kernel refuses.
	if cfg := build(t, configPatch{}); cfg["hooks"].(map[string]any)["processes"].(map[string]any)[ApproverHookName] != nil {
		t.Fatal("no peer, no approver hook")
	}
}

func TestCompitaConfigGivesOnlyItsOwnConnections(t *testing.T) {
	src := `{"tools":{"mcp":{"enabled":true,"servers":{"github":{"enabled":true,"command":"gh-mcp","env":{"GITHUB_TOKEN":"SECRET"}}}}}}`
	plain, err := buildCompitaConfig([]byte(src), configPatch{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "SECRET") || strings.Contains(string(plain), "gh-mcp") {
		t.Fatalf("the machine's MCP servers must not reach a Compita: %s", plain)
	}
	withBrowser, err := buildCompitaConfig([]byte(src), configPatch{Browser: true, Container: true})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	_ = json.Unmarshal(withBrowser, &cfg)
	mcp := cfg["tools"].(map[string]any)["mcp"].(map[string]any)
	server := mcp["servers"].(map[string]any)[BrowserServerName].(map[string]any)
	if mcp["enabled"] != true || server["enabled"] != true || server["command"] == "" || len(mcp["servers"].(map[string]any)) != 1 {
		t.Fatalf("a Compita with a browser gets exactly the browser server: %v", mcp)
	}
}

func TestCompitaConfigGivesEachCompitaItsOwnWorkspace(t *testing.T) {
	cfg := build(t, configPatch{Workspace: "/data/workspace"})
	defaults := cfg["agents"].(map[string]any)["defaults"].(map[string]any)
	if defaults["workspace"] != "/data/workspace" || defaults["model_name"] != "x/y" {
		t.Fatalf("workspace must be the Compita's own, model kept: %v", defaults)
	}
	out, err := buildCompitaConfig([]byte(`{"agents":{"defaults":{"workspace":"/host/ws"},"list":[{"id":"main","workspace":"/host/other"}]}}`), configPatch{Workspace: "/c/ws"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "/host/") {
		t.Fatalf("the machine's workspace paths must not survive: %s", out)
	}
}

func TestCompitaConfigUsesDefaultRulesWhenTheHostHasNone(t *testing.T) {
	out, err := buildCompitaConfig([]byte(`{"agents":{}}`), configPatch{Approvals: ApprovalsOpen})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	_ = json.Unmarshal(out, &cfg)
	rules := rulesOf(cfg)
	if len(rules) != 2 || rules[1]["tool"] != "install_skill" {
		t.Fatalf("the built-in default rules must survive: %v", rules)
	}
}

func TestPrepareWritesTheConfigOnceAndPerTurnPreset(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(hostConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	r := Runner{Home: home, Kernel: "/k/compa-kernel", Peer: &Peer{URL: "http://h", ComputeID: LocalID, Credential: "c"}}
	dir, err := r.prepare(Turn{CompitaID: "ana", Isolation: IsolationShared})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if strings.Contains(string(raw), "SECRET") || !strings.Contains(string(raw), "approver") {
		t.Fatalf("config = %s", raw)
	}
	// The same turn again changes nothing; another preset does.
	st1, _ := os.Stat(filepath.Join(dir, "config.json"))
	if _, err := r.prepare(Turn{CompitaID: "ana", Isolation: IsolationShared}); err != nil {
		t.Fatal(err)
	}
	st2, _ := os.Stat(filepath.Join(dir, "config.json"))
	if !st1.ModTime().Equal(st2.ModTime()) {
		t.Fatal("an unchanged config must not be rewritten")
	}
	if _, err := r.prepare(Turn{CompitaID: "ana", Isolation: IsolationShared, Approvals: ApprovalsOpen}); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(filepath.Join(dir, "config.json"))
	if strings.Contains(string(raw), `"exec"`) {
		t.Fatalf("the open preset must drop the exec rule: %s", raw)
	}
}
