package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kayiik/compa/pkg/compute"
	"github.com/kayiik/compa/pkg/config"
)

const streamingKernel = `#!/bin/sh
case "$*" in *--events*)
echo '{"type":"segment"}'
sleep 0.2
echo '{"type":"delta","text":"Hel"}'
sleep 0.2
echo '{"type":"delta","text":"lo"}'
echo '{"type":"tool","tool":"exec","state":"start","detail":"ls"}'
sleep 0.2
echo '{"type":"tool","tool":"exec","state":"end"}'
echo '{"type":"final","text":"Hello"}';;
*) echo "Hello";; esac
`

func writeStreamingKernel(t *testing.T) {
	t.Helper()
	skipWithoutShell(t)
	p := filepath.Join(t.TempDir(), "kernel")
	if err := os.WriteFile(p, []byte(streamingKernel), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvBinary, p)
}

// sseClient reads the data lines of a stream.
type sseClient struct {
	mu   sync.Mutex
	msgs []map[string]any
}

func openStream(t *testing.T, ctx context.Context, url string) *sseClient {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content type = %q", ct)
	}
	c := &sseClient{}
	go func() {
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				var m map[string]any
				if json.Unmarshal([]byte(data), &m) == nil {
					c.mu.Lock()
					c.msgs = append(c.msgs, m)
					c.mu.Unlock()
				}
			}
		}
	}()
	return c
}

func (c *sseClient) types() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, m := range c.msgs {
		out = append(out, m["type"].(string))
	}
	return strings.Join(out, ",")
}

func (c *sseClient) waitFor(t *testing.T, typ string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if strings.Contains(c.types(), typ) {
			return
		}
	}
	t.Fatalf("never saw %q; saw %s", typ, c.types())
}

func TestLocalTurnStreamsToTheDashboardWhileItRuns(t *testing.T) {
	writeStreamingKernel(t)
	mux := computeMux(t)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	computeCall(mux, "PUT", "/api/compute/enabled", `{"enabled":true}`)
	computeCall(mux, "POST", "/api/compitas", `{"name":"Ana","compute_id":"this-computer","isolation":"shared","approvals":"open"}`)
	if rec := computeCall(mux, "GET", "/api/compitas/nobody/stream", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("stream of nobody = %d", rec.Code)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	early := openStream(t, ctx, srv.URL+"/api/compitas/ana/stream")
	early.waitFor(t, "snapshot")
	if early.msgs[0]["active"] != false {
		t.Fatalf("before any turn nothing is active: %v", early.msgs[0])
	}

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- computeCall(mux, "POST", "/api/compitas/ana/chat", `{"message":"hi"}`) }()

	// A dashboard that connects mid-turn starts from what is written so far.
	early.waitFor(t, "delta")
	late := openStream(t, ctx, srv.URL+"/api/compitas/ana/stream")
	late.waitFor(t, "snapshot")
	late.mu.Lock()
	snap := late.msgs[0]
	late.mu.Unlock()
	if snap["active"] != true || !strings.HasPrefix(snap["text"].(string), "Hel") {
		t.Fatalf("a late snapshot = %v", snap)
	}

	rec := <-done
	if !strings.Contains(rec.Body.String(), `"text":"Hello"`) {
		t.Fatalf("the reply is still the whole answer: %s", rec.Body)
	}
	early.waitFor(t, "end")
	got := early.types()
	for _, want := range []string{"begin", "segment", "delta", "tool", "end"} {
		if !strings.Contains(got, want) {
			t.Fatalf("early stream = %s, missing %q", got, want)
		}
	}
	if strings.Index(got, "begin") > strings.Index(got, "delta") || strings.Index(got, "delta") > strings.LastIndex(got, "end") {
		t.Fatalf("events out of order: %s", got)
	}
}

func TestStreamHubFoldsEventsIntoTheLiveView(t *testing.T) {
	h := &streamHub{byID: map[string]*liveStream{}}
	h.begin("a")
	for _, ev := range []compute.StreamEvent{
		{Type: "segment"}, {Type: "delta", Text: "I will look. "},
		{Type: "tool", Tool: "exec", State: "start", Detail: "ls"},
		{Type: "tool", Tool: "exec", State: "end"},
		{Type: "segment"}, {Type: "delta", Text: "Draft"}, {Type: "reset"}, {Type: "delta", Text: "Final answer"},
		{Type: "unknown"},
	} {
		h.apply("a", ev)
	}
	snap, _, cancel := h.subscribe("a")
	defer cancel()
	var s snapshot
	_ = json.Unmarshal(snap, &s)
	if !s.Active || s.Text != "I will look. \n\nFinal answer" || len(s.Tools) != 1 || s.Tools[0].State != "end" || s.Tools[0].Detail != "ls" {
		t.Fatalf("snapshot = %+v", s)
	}
	h.end("a")
	snap2, _, cancel2 := h.subscribe("a")
	defer cancel2()
	_ = json.Unmarshal(snap2, &s)
	if s.Active {
		t.Fatal("an ended turn is not active")
	}
	h.begin("a")
	snap3, _, cancel3 := h.subscribe("a")
	defer cancel3()
	_ = json.Unmarshal(snap3, &s)
	if s.Text != "" || len(s.Tools) != 0 {
		t.Fatalf("a new turn starts clean: %+v", s)
	}
}

func TestStreamHubDropsASubscriberThatCannotKeepUp(t *testing.T) {
	h := &streamHub{byID: map[string]*liveStream{}}
	_, ch, _ := h.subscribe("a")
	h.begin("a")
	for i := 0; i < 600; i++ {
		h.apply("a", compute.StreamEvent{Type: "delta", Text: "x"})
	}
	n := 0
	for range ch {
		n++
	}
	if n == 0 || n > 512 {
		t.Fatalf("a slow subscriber is cut off after its buffer (%d messages)", n)
	}
}

func TestDialOutComputeStreamsThroughTheEventsEndpoint(t *testing.T) {
	writeStreamingKernel(t)
	mux := computeMux(t)
	srv := httptest.NewServer(mux)
	// The worker's long poll would hold Close for 25 seconds.
	t.Cleanup(func() { srv.CloseClientConnections(); srv.Close() })
	computeCall(mux, "PUT", "/api/compute/enabled", `{"enabled":true}`)
	var added struct{ Token string }
	// A name no other test uses: the broker is shared, and a worker that stopped still counts as online for a while.
	_ = json.Unmarshal(computeCall(mux, "POST", "/api/computes", `{"name":"streambox","mode":"dial_out"}`).Body.Bytes(), &added)
	id, err := compute.Enroll(context.Background(), nil, srv.URL, added.Token, compute.Capabilities{})
	if err != nil {
		t.Fatal(err)
	}
	computeCall(mux, "POST", "/api/compitas", `{"name":"Bo","compute_id":"`+id.ComputeID+`","isolation":"shared","approvals":"open"}`)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go compute.Serve(ctx, id, compute.Runner{Home: t.TempDir(), Kernel: os.Getenv(config.EnvBinary)}, func(string, ...any) {})
	for deadline := time.Now().Add(5 * time.Second); !computeBroker.Online(id.ComputeID) && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
	}

	stream := openStream(t, ctx, srv.URL+"/api/compitas/bo/stream")
	stream.waitFor(t, "snapshot")
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- computeCall(mux, "POST", "/api/compitas/bo/chat", `{"message":"hi"}`) }()
	stream.waitFor(t, "delta")
	rec := <-done
	if !strings.Contains(rec.Body.String(), `"text":"Hello"`) || strings.Contains(rec.Body.String(), `"error":true`) {
		t.Fatalf("remote reply = %s", rec.Body)
	}
	stream.waitFor(t, "end")
	if !strings.Contains(stream.types(), "tool") {
		t.Fatalf("a remote turn's tool use must show too: %s", stream.types())
	}

	// Only the compute that runs a job may report for it.
	post := func(cid, cred, body string) int {
		req := httptest.NewRequest("POST", compute.EventsPath, strings.NewReader(body))
		req.Header.Set("X-Compute-ID", cid)
		req.Header.Set("Authorization", "Bearer "+cred)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w.Code
	}
	if code := post(id.ComputeID, "wrong", `{"job_id":"x","events":[]}`); code != http.StatusUnauthorized {
		t.Fatalf("bad credential = %d", code)
	}
	if code := post(id.ComputeID, id.Credential, `{"job_id":"nope","events":[]}`); code != http.StatusNotFound {
		t.Fatalf("an unknown job = %d", code)
	}
	if code := post(compute.LocalID, localCredential("bo"), `{"job_id":"x","events":[]}`); code != http.StatusUnauthorized {
		t.Fatalf("the local compute does not report this way = %d", code)
	}
}

func TestDialInComputeStreamsItsRun(t *testing.T) {
	writeStreamingKernel(t)
	mux := computeMux(t)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	var secret string
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The secret is only known once Compa has made the compute.
		compute.DialInHandler(secret, compute.Runner{Home: t.TempDir(), Kernel: os.Getenv(config.EnvBinary)}).ServeHTTP(w, r)
	}))
	t.Cleanup(remote.Close)
	computeCall(mux, "PUT", "/api/compute/enabled", `{"enabled":true}`)
	var added struct{ Token string }
	_ = json.Unmarshal(computeCall(mux, "POST", "/api/computes", `{"name":"lan","mode":"dial_in","url":"`+remote.URL+`"}`).Body.Bytes(), &added)
	secret = added.Token
	computeCall(mux, "POST", "/api/compitas", `{"name":"Cy","compute_id":"lan","isolation":"shared","approvals":"open"}`)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := openStream(t, ctx, srv.URL+"/api/compitas/cy/stream")
	stream.waitFor(t, "snapshot")
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- computeCall(mux, "POST", "/api/compitas/cy/chat", `{"message":"hi"}`) }()
	stream.waitFor(t, "delta")
	rec := <-done
	if !strings.Contains(rec.Body.String(), `"text":"Hello"`) || strings.Contains(rec.Body.String(), `"error":true`) {
		t.Fatalf("dial-in reply = %s", rec.Body)
	}
	stream.waitFor(t, "end")
}
