package compute

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kayiik/compa/pkg/approval"
)

// Approval presets a Compita can run with.
const (
	// ApprovalsCareful asks the owner before a tool that writes outside, is
	// destructive or may cost money runs, and before commands run directly on
	// a machine rather than in a container. Everything else follows the
	// machine's own policy.
	ApprovalsCareful = "careful"
	// ApprovalsOpen leaves the machine's own policy as it is.
	ApprovalsOpen = "open"
)

// ValidApprovals reports whether p is a known preset; empty means careful.
func ValidApprovals(p string) bool {
	return p == "" || p == ApprovalsCareful || p == ApprovalsOpen
}

// configPatch says how a Compita's config differs from its machine's.
type configPatch struct {
	Approvals string
	// Container means commands run inside a container, away from the machine.
	Container bool
	// Browser adds the browser MCP server.
	Browser bool
	// Connections are the Compita's own MCP servers.
	Connections map[string]Connection
	// Home is the Compita's own directory, as the kernel sees the path.
	Home string
	// Workspace is where the Compita keeps its files and sessions, as the
	// kernel sees the path. Never the machine's own workspace.
	Workspace string
	// HookCommand starts the approver hook; nil means the Compita cannot reach
	// an owner to ask, so what the policy asks about is refused.
	HookCommand []string
}

// buildCompitaConfig derives a Compita's config.json from its machine's: the
// model setup stays, the machine's channels and hooks do not, and the approval
// policy gets the Compita's preset in front of the machine's own rules.
func buildCompitaConfig(src []byte, p configPatch) ([]byte, error) {
	var cfg map[string]any
	if err := json.Unmarshal(src, &cfg); err != nil {
		return nil, fmt.Errorf("compute: read the machine's config: %w", err)
	}
	// Chat-app credentials stay with the host; a Compita does not need them.
	delete(cfg, "channel_list")
	delete(cfg, "channels")

	// Its own space: files, memory and sessions live in the Compita's own
	// workspace, not the machine's.
	if p.Workspace != "" {
		defaults := object(object(cfg, "agents"), "defaults")
		defaults["workspace"] = p.Workspace
		if list, ok := object(cfg, "agents")["list"].([]any); ok {
			for _, a := range list {
				if m, ok := a.(map[string]any); ok {
					delete(m, "workspace")
				}
			}
		}
	}

	tools := object(cfg, "tools")
	policy := object(tools, "approval")
	hostRules, ok := policy["rules"].([]any)
	if !ok {
		raw, _ := json.Marshal(approval.DefaultPolicy().Rules)
		_ = json.Unmarshal(raw, &hostRules)
	}
	var ours []any
	if p.Approvals != ApprovalsOpen {
		ours = append(ours, map[string]any{
			"hints":  []any{approval.HintDestructive, approval.HintExternalWrites, approval.HintCostUnknown},
			"action": string(approval.Ask),
		})
		if !p.Container {
			ours = append(ours, map[string]any{"tool": "exec", "action": string(approval.Ask)})
		}
	}
	policy["rules"] = append(ours, hostRules...)

	// A Compita gets only the connections made for it: the machine's own MCP
	// servers carry the machine owner's credentials.
	mcp := object(tools, "mcp")
	servers := map[string]any{}
	mcp["servers"] = servers
	if p.Browser {
		mcp["enabled"] = true
		servers[BrowserServerName] = browserMCPServer(p.Container, p.Home)
	}
	for name, c := range p.Connections {
		mcp["enabled"] = true
		args := make([]any, len(c.Args))
		for i, a := range c.Args {
			args[i] = a
		}
		env := make(map[string]any, len(c.Env))
		for k, v := range c.Env {
			env[k] = v
		}
		// The owner chose this server, so its own annotations (read-only,
		// destructive) count, and the approval rules can match them.
		servers[name] = map[string]any{"enabled": true, "command": c.Command, "args": args, "env": env, "trusted": true}
	}

	hooks := object(cfg, "hooks")
	hooks["processes"] = map[string]any{}
	if len(p.HookCommand) > 0 {
		hooks["enabled"] = true
		object(hooks, "defaults")["approval_timeout_ms"] = int((ApprovalWait + time.Minute) / time.Millisecond)
		cmd := make([]any, len(p.HookCommand))
		for i, c := range p.HookCommand {
			cmd[i] = c
		}
		hooks["processes"] = map[string]any{
			ApproverHookName: map[string]any{
				"enabled": true, "command": cmd, "intercept": []any{"approve_tool"},
			},
		}
	}
	return json.MarshalIndent(cfg, "", "  ")
}

func object(parent map[string]any, key string) map[string]any {
	if m, ok := parent[key].(map[string]any); ok {
		return m
	}
	m := map[string]any{}
	parent[key] = m
	return m
}

// sameJSON reports whether two JSON documents hold the same data.
func sameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return bytes.Equal(a, b)
	}
	return reflect.DeepEqual(x, y)
}

// compitaSecretsDropped are the sections of a machine's .security.yml that
// hold what buildCompitaConfig leaves out of config.json: the credentials of
// the machine's chat apps, MCP servers and hooks.
var compitaSecretsDropped = map[string]bool{"channel_list": true, "mcp": true, "hooks": true}

// buildCompitaSecurity derives a Compita's .security.yml from its machine's:
// the model and tool keys stay, the secrets of the chat apps, MCP servers and
// hooks do not, so a Compita never holds them.
func buildCompitaSecurity(src []byte) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, fmt.Errorf("compute: read the machine's .security.yml: %w", err)
	}
	if len(doc.Content) == 0 {
		return src, nil
	}
	top := doc.Content[0]
	if top.Kind != yaml.MappingNode {
		return nil, errors.New("compute: the machine's .security.yml is not a mapping")
	}
	kept := make([]*yaml.Node, 0, len(top.Content))
	for i := 0; i+1 < len(top.Content); i += 2 {
		if !compitaSecretsDropped[top.Content[i].Value] {
			kept = append(kept, top.Content[i], top.Content[i+1])
		}
	}
	top.Content = kept
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
