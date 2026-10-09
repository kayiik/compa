package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Environment a Compita turn is started with so it can reach its peers.
const (
	EnvPeerURL     = "COMPA_PEER_URL"
	EnvPeerCompute = "COMPA_PEER_COMPUTE"
	EnvPeerToken   = "COMPA_PEER_TOKEN"
	EnvPeerSelf    = "COMPA_PEER_SELF"
	EnvPeerDepth   = "COMPA_PEER_DEPTH"
)

// MessagePeerTool lets a Compita ask another Compita and wait for its answer.
// The Compa host routes the message to wherever the peer runs.
type MessagePeerTool struct {
	url, compute, token, self string
	depth                     int
	client                    *http.Client
}

// NewMessagePeerToolFromEnv returns nil when this process is not a Compita turn.
func NewMessagePeerToolFromEnv() *MessagePeerTool {
	url := strings.TrimRight(os.Getenv(EnvPeerURL), "/")
	if url == "" || os.Getenv(EnvPeerToken) == "" {
		return nil
	}
	depth, _ := strconv.Atoi(os.Getenv(EnvPeerDepth))
	return &MessagePeerTool{
		url: url, compute: os.Getenv(EnvPeerCompute), token: os.Getenv(EnvPeerToken),
		self: os.Getenv(EnvPeerSelf), depth: depth,
		// The colleague's whole turn runs inside this call; the host gives a turn
		// 15 minutes (30 for an objective's), and the call must outlast it.
		client: &http.Client{Timeout: 16 * time.Minute},
	}
}

func (t *MessagePeerTool) Name() string { return "message_peer" }

func (t *MessagePeerTool) Description() string {
	return "Send a message to another Compita (a peer agent with its own computer and workspace) " +
		"and wait for its reply. Use it to hand off work or ask a colleague. Name the peer by its name or id."
}

func (t *MessagePeerTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"to":      map[string]any{"type": "string", "description": "Name or id of the peer Compita"},
			"message": map[string]any{"type": "string", "description": "What to tell or ask the peer"},
		},
		"required": []string{"to", "message"},
	}
}

func (t *MessagePeerTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	to, _ := args["to"].(string)
	msg, _ := args["message"].(string)
	if strings.TrimSpace(to) == "" || strings.TrimSpace(msg) == "" {
		return ErrorResult("to and message are required")
	}
	body, _ := json.Marshal(map[string]any{"from": t.self, "to": to, "message": msg, "depth": t.depth})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url+"/api/compute/peer", bytes.NewReader(body))
	if err != nil {
		return ErrorResult(err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Compute-ID", t.compute)
	req.Header.Set("Authorization", "Bearer "+t.token)
	resp, err := t.client.Do(req)
	if err != nil {
		return ErrorResult("could not reach Compa: " + err.Error())
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		Reply string `json:"reply"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode != http.StatusOK || out.Error != "" {
		if out.Error == "" {
			out.Error = fmt.Sprintf("status %d", resp.StatusCode)
		}
		return ErrorResult(out.Error)
	}
	return NewToolResult(out.Reply)
}
