package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/kayiik/compa/pkg/compute"
	"github.com/kayiik/compa/pkg/config"
)

// fakeLiveView stands in for the browser display's websockify: it echoes
// WebSocket frames, remembers what reached it, and would serve a page to
// anyone who asked.
type fakeLiveView struct {
	srv  *httptest.Server
	mu   sync.Mutex
	hdr  http.Header
	path string
}

func newFakeLiveView(t *testing.T) *fakeLiveView {
	t.Helper()
	f := &fakeLiveView{}
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/websockify", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.hdr, f.path = r.Header.Clone(), r.URL.RequestURI()
		f.mu.Unlock()
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			mt, msg, err := c.ReadMessage()
			if err != nil {
				return
			}
			_ = c.WriteMessage(mt, msg)
		}
	})
	mux.HandleFunc("/vnc.html", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<script>steal()</script>")) })
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeLiveView) addr() string { return strings.TrimPrefix(f.srv.URL, "http://") }

// watch opens the live view of a Compita through the dashboard at base and
// checks that what is sent comes back.
func watch(t *testing.T, base, id string) {
	t.Helper()
	c, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(base, "http")+"/api/compitas/"+id+"/live/ws", nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("live view of %s: %v (HTTP %d)", id, err, status)
	}
	defer c.Close()
	if err := c.WriteMessage(websocket.BinaryMessage, []byte("RFB 003.008\n")); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	if _, msg, err := c.ReadMessage(); err != nil || string(msg) != "RFB 003.008\n" {
		t.Fatalf("echo = %q, %v", msg, err)
	}
}

// refused reports the status of a live view request that must not open.
func refused(t *testing.T, base, id string) int {
	t.Helper()
	_, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(base, "http")+"/api/compitas/"+id+"/live/ws", nil)
	if err == nil {
		t.Fatalf("the live view of %s opened", id)
	}
	if resp == nil {
		t.Fatalf("no answer for %s: %v", id, err)
	}
	return resp.StatusCode
}

func TestLocalLiveViewCarriesTheWebSocketAndNothingElse(t *testing.T) {
	view := newFakeLiveView(t)
	t.Setenv("COMPA_LIVE_ADDR", view.addr())
	mux := computeMux(t)
	s := compute.NewStore(config.GetHome())
	s.Detect = func() compute.Capabilities { return compute.Capabilities{Browser: true} }
	if err := s.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"name":"Watcher7","compute_id":"this-computer","isolation":"shared","browser":true}`,
		`{"name":"Blind7","compute_id":"this-computer","isolation":"shared"}`,
	} {
		if rec := computeCall(mux, "POST", "/api/compitas", body); rec.Code != http.StatusCreated {
			t.Fatalf("create = %d %s", rec.Code, rec.Body)
		}
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	watch(t, srv.URL, "watcher7")

	// The dashboard's credentials and the page's origin stay with the dashboard, and
	// the machine is only ever asked for its one WebSocket endpoint.
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/api/compitas/watcher7/live/ws?path=/etc/passwd",
		http.Header{"Cookie": {"compa_session=secret"}, "Authorization": {"Bearer dash"}, "Origin": {srv.URL}})
	if err != nil {
		t.Fatal(err)
	}
	_ = c.WriteMessage(websocket.BinaryMessage, []byte("x"))
	_, _, _ = c.ReadMessage()
	c.Close()
	view.mu.Lock()
	hdr, path := view.hdr, view.path
	view.mu.Unlock()
	if hdr.Get("Cookie") != "" || hdr.Get("Authorization") != "" || hdr.Get("Origin") != "" {
		t.Fatalf("the dashboard's credentials reached the machine: %v", hdr)
	}
	if path != "/websockify" {
		t.Fatalf("the machine was asked for %q, not just its WebSocket endpoint", path)
	}

	if code := refused(t, srv.URL, "blind7"); code != http.StatusNotFound {
		t.Fatalf("a Compita without a browser = %d", code)
	}
	if code := refused(t, srv.URL, "nobody"); code != http.StatusNotFound {
		t.Fatalf("no such Compita = %d", code)
	}
	// A page the machine serves is never carried: only the WebSocket route exists.
	for _, p := range []string{"/api/compitas/watcher7/live/vnc.html", "/api/compitas/watcher7/live/"} {
		if rec := computeCall(mux, "GET", p, ""); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "steal") {
			t.Fatalf("GET %s = %d %s", p, rec.Code, rec.Body)
		}
	}
	if rec := computeCall(mux, "GET", "/api/compitas/watcher7/live/ws", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("a plain GET of the live view = %d", rec.Code)
	}
}

func TestLiveViewIsRefusedWhileTheBrowserIsNotUp(t *testing.T) {
	t.Setenv("COMPA_LIVE_ADDR", "127.0.0.1:1") // nothing listens there
	mux := computeMux(t)
	s := compute.NewStore(config.GetHome())
	s.Detect = func() compute.Capabilities { return compute.Capabilities{Browser: true} }
	_ = s.SetEnabled(true)
	computeCall(mux, "POST", "/api/compitas", `{"name":"Idle8","compute_id":"this-computer","isolation":"shared","browser":true}`)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	if code := refused(t, srv.URL, "idle8"); code != http.StatusBadGateway {
		t.Fatalf("no browser up = %d, want a clean 502", code)
	}
}

func TestDialInLiveViewGoesThroughTheComputesTunnel(t *testing.T) {
	view := newFakeLiveView(t)
	t.Setenv("COMPA_LIVE_ADDR", view.addr())
	mux := computeMux(t)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	var secret string
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		compute.DialInHandler(secret, compute.Runner{Home: t.TempDir()}).ServeHTTP(w, r)
	}))
	defer remote.Close()
	computeCall(mux, "PUT", "/api/compute/enabled", `{"enabled":true}`)
	var added struct{ Token string }
	_ = json.Unmarshal(computeCall(mux, "POST", "/api/computes", `{"name":"lanview","mode":"dial_in","url":"`+remote.URL+`"}`).Body.Bytes(), &added)
	secret = added.Token
	if err := compute.NewStore(config.GetHome()).SetCapabilities("lanview", compute.Capabilities{Browser: true}); err != nil {
		t.Fatal(err)
	}
	if rec := computeCall(mux, "POST", "/api/compitas", `{"name":"Dia","compute_id":"lanview","isolation":"shared","browser":true}`); rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}

	watch(t, srv.URL, "dia")

	// With the machine's browser down the tunnel is refused, and Compa says so cleanly.
	t.Setenv("COMPA_LIVE_ADDR", "127.0.0.1:1")
	if code := refused(t, srv.URL, "dia"); code != http.StatusBadGateway {
		t.Fatalf("the machine's browser is down = %d", code)
	}
}

func TestDialOutLiveViewComesThroughAConnectBack(t *testing.T) {
	view := newFakeLiveView(t)
	t.Setenv("COMPA_LIVE_ADDR", view.addr())
	mux := computeMux(t)
	srv := httptest.NewServer(mux)
	// The worker's long poll would hold Close for 25 seconds.
	t.Cleanup(func() { srv.CloseClientConnections(); srv.Close() })
	computeCall(mux, "PUT", "/api/compute/enabled", `{"enabled":true}`)
	var added struct{ Token string }
	// A name no other test uses: the broker is shared across tests.
	_ = json.Unmarshal(computeCall(mux, "POST", "/api/computes", `{"name":"liveout","mode":"dial_out"}`).Body.Bytes(), &added)
	id, err := compute.Enroll(context.Background(), nil, srv.URL, added.Token, compute.Capabilities{Browser: true})
	if err != nil {
		t.Fatal(err)
	}
	if rec := computeCall(mux, "POST", "/api/compitas", `{"name":"Out","compute_id":"`+id.ComputeID+`","isolation":"shared","browser":true}`); rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go compute.Serve(ctx, id, compute.Runner{Home: t.TempDir()}, func(string, ...any) {})
	for deadline := time.Now().Add(5 * time.Second); !computeBroker.Online(id.ComputeID) && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
	}

	watch(t, srv.URL, "out")

	// Only the compute that was asked may connect back, and only with its credential.
	req := httptest.NewRequest("GET", compute.LiveConnectPath+"not-a-session", nil)
	req.Header.Set("X-Compute-ID", id.ComputeID)
	req.Header.Set("Authorization", "Bearer wrong")
	if rec := httptest.NewRecorder(); func() int { mux.ServeHTTP(rec, req); return rec.Code }() != http.StatusUnauthorized {
		t.Fatal("a connect back with a wrong credential must be refused")
	}
	req.Header.Set("Authorization", "Bearer "+id.Credential)
	if rec := httptest.NewRecorder(); func() int { mux.ServeHTTP(rec, req); return rec.Code }() != http.StatusNotFound {
		t.Fatal("a connect back for a session nobody asked for must be refused")
	}
}
