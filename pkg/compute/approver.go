package compute

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// ApproverHookName is the process hook that routes a Compita's approvals to
// the owner through the Compa host.
const ApproverHookName = "compita-approver"

// HostAsker asks the Compa host for the owner's approval. It is built from the
// same environment as the message_peer tool.
type HostAsker struct {
	URL, ComputeID, Token, Self string
	Client                      *http.Client
}

// HostAskerFromEnv returns nil when the Compita has no way to reach its host.
func HostAskerFromEnv() *HostAsker {
	h := &HostAsker{
		URL: os.Getenv("COMPA_PEER_URL"), ComputeID: os.Getenv("COMPA_PEER_COMPUTE"),
		Token: os.Getenv("COMPA_PEER_TOKEN"), Self: os.Getenv("COMPA_PEER_SELF"),
	}
	if h.URL == "" || h.Self == "" {
		return nil
	}
	return h
}

// ApprovalWait is how long the host holds a request for the owner.
var ApprovalWait = 10 * time.Minute

// Ask posts the request and blocks until the owner answers.
func (h *HostAsker) Ask(ctx context.Context, tool string, args map[string]any) (bool, string) {
	if h == nil {
		return false, "this Compita cannot reach its owner to ask"
	}
	body, _ := json.Marshal(ApprovalAsk{From: h.Self, Tool: tool, Arguments: args})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(h.URL, "/")+"/api/compute/approval", bytes.NewReader(body))
	if err != nil {
		return false, err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Compute-ID", h.ComputeID)
	req.Header.Set("Authorization", "Bearer "+h.Token)
	client := h.Client
	if client == nil {
		client = &http.Client{Timeout: ApprovalWait + time.Minute}
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, "could not reach the owner to ask: " + err.Error()
	}
	defer resp.Body.Close()
	var out struct {
		Approved bool   `json:"approved"`
		Reason   string `json:"reason"`
		Error    string `json:"error"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Sprintf("the owner could not be asked (%d %s)", resp.StatusCode, out.Error)
	}
	return out.Approved, out.Reason
}

type hookMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      uint64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *hookErr        `json:"error,omitempty"`
}

type hookErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ServeApprover speaks the kernel's process-hook protocol (JSON-RPC 2.0, one
// message per line) as an approve_tool hook: every call it is asked about goes
// to ask, and its answer goes back to the kernel.
func ServeApprover(ctx context.Context, in io.Reader, out io.Writer, ask func(ctx context.Context, tool string, args map[string]any) (bool, string)) error {
	var wmu sync.Mutex
	send := func(m hookMsg) {
		m.JSONRPC = "2.0"
		raw, err := json.Marshal(m)
		if err != nil {
			return
		}
		wmu.Lock()
		defer wmu.Unlock()
		_, _ = out.Write(append(raw, '\n'))
	}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
	var wg sync.WaitGroup
	defer wg.Wait()
	for sc.Scan() {
		var m hookMsg
		if json.Unmarshal(sc.Bytes(), &m) != nil || m.ID == 0 {
			continue // a notification, or noise
		}
		switch m.Method {
		case "hook.hello":
			send(hookMsg{ID: m.ID, Result: map[string]any{}})
		case "hook.approve_tool":
			var p struct {
				Tool      string         `json:"tool"`
				Arguments map[string]any `json:"arguments"`
			}
			if err := json.Unmarshal(m.Params, &p); err != nil {
				send(hookMsg{ID: m.ID, Error: &hookErr{Code: -32602, Message: "bad params"}})
				continue
			}
			wg.Add(1)
			go func(id uint64) {
				defer wg.Done()
				ok, reason := ask(ctx, p.Tool, p.Arguments)
				send(hookMsg{ID: id, Result: map[string]any{"approved": ok, "reason": reason}})
			}(m.ID)
		default:
			send(hookMsg{ID: m.ID, Error: &hookErr{Code: -32601, Message: "method not supported"}})
		}
	}
	return sc.Err()
}
