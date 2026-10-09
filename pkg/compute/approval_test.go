package compute

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestInboxParksUntilTheOwnerAnswers(t *testing.T) {
	in := NewInbox()
	got := make(chan string, 1)
	go func() {
		ok, reason := in.Ask(context.Background(), ApprovalAsk{From: "ana", Tool: "exec", Arguments: map[string]any{"command": "rm -rf build"}}, time.Minute)
		got <- reason
		if !ok {
			got <- "denied"
		}
	}()
	var pending []Approval
	for i := 0; i < 200 && len(pending) == 0; i++ {
		time.Sleep(5 * time.Millisecond)
		pending = in.Pending("ana")
	}
	if len(pending) != 1 || pending[0].Summary != "exec: rm -rf build" || len(in.Pending("bo")) != 0 {
		t.Fatalf("pending = %+v", pending)
	}
	if _, err := in.Decide(pending[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if reason := <-got; !strings.Contains(reason, "approved") {
		t.Fatalf("reason = %q", reason)
	}
	if _, err := in.Decide(pending[0].ID, true); err != ErrNoSuchApproval {
		t.Fatalf("answering twice = %v", err)
	}
	if len(in.Pending("")) != 0 {
		t.Fatal("answered requests must leave the inbox")
	}
}

func TestInboxDeniesOnTimeoutWithdrawalAndFlood(t *testing.T) {
	in := NewInbox()
	if ok, reason := in.Ask(context.Background(), ApprovalAsk{From: "a", Tool: "x"}, 20*time.Millisecond); ok || !strings.Contains(reason, "did not answer") {
		t.Fatalf("timeout = %v %q", ok, reason)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if ok, _ := in.Ask(ctx, ApprovalAsk{From: "a", Tool: "x"}, time.Minute); ok {
		t.Fatal("a withdrawn request must be denied")
	}
	if len(in.Pending("")) != 0 {
		t.Fatal("expired requests must leave the inbox")
	}

	var wg sync.WaitGroup
	for i := 0; i < MaxPendingPerCompita; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			in.Ask(context.Background(), ApprovalAsk{From: "a", Tool: "x"}, 300*time.Millisecond)
		}()
	}
	for len(in.Pending("a")) < MaxPendingPerCompita {
		time.Sleep(5 * time.Millisecond)
	}
	if ok, reason := in.Ask(context.Background(), ApprovalAsk{From: "a", Tool: "x"}, time.Minute); ok || !strings.Contains(reason, "too many") {
		t.Fatalf("flood = %v %q", ok, reason)
	}
	wg.Wait()
}

func TestSummarizeShowsTheWholeCommand(t *testing.T) {
	if s := Summarize("exec", map[string]any{"command": "df -h"}); s != "exec: df -h" {
		t.Fatalf("summary = %q", s)
	}
	if s := Summarize("mcp_github_create_issue", map[string]any{"title": "t"}); s != `mcp_github_create_issue {"title":"t"}` {
		t.Fatalf("summary = %q", s)
	}
	if s := Summarize("x", map[string]any{"command": strings.Repeat("a", 5000)}); !strings.Contains(s, "more characters") {
		t.Fatal("a huge call must be clipped, with a note")
	}
}

func TestServeApproverSpeaksTheHookProtocol(t *testing.T) {
	in := strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"hook.hello","params":{"name":"compita-approver","version":1,"modes":["approve"]}}` + "\n" +
			`{"jsonrpc":"2.0","method":"hook.runtime_event","params":{}}` + "\n" +
			`{"jsonrpc":"2.0","id":2,"method":"hook.approve_tool","params":{"tool":"exec","arguments":{"command":"ls"}}}` + "\n" +
			`{"jsonrpc":"2.0","id":3,"method":"hook.approve_tool","params":{"tool":"exec","arguments":{"command":"rm x"}}}` + "\n" +
			`{"jsonrpc":"2.0","id":4,"method":"hook.before_tool","params":{}}` + "\n")
	var out bytes.Buffer
	ask := func(_ context.Context, tool string, args map[string]any) (bool, string) {
		if args["command"] == "ls" {
			return true, "fine"
		}
		return false, "no"
	}
	if err := ServeApprover(context.Background(), in, &out, ask); err != nil {
		t.Fatal(err)
	}
	replies := map[uint64]map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var m struct {
			ID     uint64         `json:"id"`
			Result map[string]any `json:"result"`
			Error  map[string]any `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad line %q", line)
		}
		if m.Error != nil {
			replies[m.ID] = m.Error
		} else {
			replies[m.ID] = m.Result
		}
	}
	if len(replies) != 4 {
		t.Fatalf("replies = %v", replies)
	}
	if replies[2]["approved"] != true || replies[3]["approved"] != false || replies[3]["reason"] != "no" {
		t.Fatalf("approvals = %v %v", replies[2], replies[3])
	}
	if replies[4]["code"] == nil {
		t.Fatalf("an unsupported method must be an error: %v", replies[4])
	}
}

func TestHostAskerPostsAndFailsClosed(t *testing.T) {
	var seen struct {
		auth, compute string
		ask           ApprovalAsk
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.auth, seen.compute = r.Header.Get("Authorization"), r.Header.Get("X-Compute-ID")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &seen.ask)
		_, _ = w.Write([]byte(`{"approved":true,"reason":"ok"}`))
	}))
	defer srv.Close()
	h := &HostAsker{URL: srv.URL, ComputeID: "lan", Token: "tok", Self: "cy"}
	ok, reason := h.Ask(context.Background(), "exec", map[string]any{"command": "ls"})
	if !ok || reason != "ok" || seen.auth != "Bearer tok" || seen.compute != "lan" || seen.ask.From != "cy" || seen.ask.Tool != "exec" {
		t.Fatalf("ask = %v %q %+v", ok, reason, seen)
	}
	if ok, _ := (*HostAsker)(nil).Ask(context.Background(), "exec", nil); ok {
		t.Fatal("no host to ask must mean no")
	}
	srv.Close()
	if ok, reason := h.Ask(context.Background(), "exec", nil); ok || !strings.Contains(reason, "could not reach") {
		t.Fatalf("unreachable host = %v %q", ok, reason)
	}
}
