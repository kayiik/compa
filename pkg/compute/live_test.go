package compute

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// echoServer stands in for the browser display's live view: it echoes what it
// is sent. It returns its address.
func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); _ = c.Close() }()
		}
	}()
	return ln.Addr().String()
}

func echoThrough(t *testing.T, conn net.Conn, msg string) {
	t.Helper()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte(msg)); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != msg {
		t.Fatalf("echo = %q, %v; want %q", got, err, msg)
	}
}

func TestDialInTunnelCarriesBytesToTheLiveView(t *testing.T) {
	t.Setenv("COMPA_LIVE_ADDR", echoServer(t))
	srv := httptest.NewServer(DialInHandler("secret", Runner{}))
	defer srv.Close()
	c := Compute{Mode: ModeDialIn, URL: srv.URL, DialSecret: "secret"}

	conn, err := DialTunnel(context.Background(), c, "tunnelbox1", IsolationShared)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	echoThrough(t, conn, "RFB 003.008\n")
	// A frame bigger than one message is carried whole, in order.
	echoThrough(t, conn, strings.Repeat("0123456789abcdef", 8<<10))

	bad := c
	bad.DialSecret = "wrong"
	if _, err := DialTunnel(context.Background(), bad, "tunnelbox1", IsolationShared); err == nil {
		t.Fatal("a tunnel opened with the wrong secret")
	}
}

func TestDialInTunnelOverTLSHonoursThePin(t *testing.T) {
	t.Setenv("COMPA_LIVE_ADDR", echoServer(t))
	srv := httptest.NewTLSServer(DialInHandler("secret", Runner{}))
	defer srv.Close()
	c := Compute{Mode: ModeDialIn, URL: srv.URL, DialSecret: "secret", PinSHA256: Fingerprint(srv.Certificate().Raw)}

	conn, err := DialTunnel(context.Background(), c, "tunnelbox5", IsolationShared)
	if err != nil {
		t.Fatalf("the pinned certificate must be accepted: %v", err)
	}
	defer conn.Close()
	echoThrough(t, conn, "over wss")

	c.PinSHA256 = Fingerprint([]byte("some other certificate"))
	if _, err := DialTunnel(context.Background(), c, "tunnelbox5", IsolationShared); err == nil {
		t.Fatal("a certificate that does not match the pin must be refused")
	}
}

func TestDialInTunnelRefusesWhenTheBrowserIsNotUp(t *testing.T) {
	t.Setenv("COMPA_LIVE_ADDR", "127.0.0.1:1") // nothing listens there
	srv := httptest.NewServer(DialInHandler("secret", Runner{}))
	defer srv.Close()
	c := Compute{Mode: ModeDialIn, URL: srv.URL, DialSecret: "secret"}
	if _, err := DialTunnel(context.Background(), c, "tunnelbox2", IsolationShared); err == nil || !strings.Contains(err.Error(), "bad handshake") {
		t.Fatalf("err = %v, want the handshake to be refused", err)
	}
	// A Compita id that could be a filter or a path is never looked up.
	if _, err := DialTunnel(context.Background(), c, "../x,y", IsolationShared); err == nil {
		t.Fatal("a malformed Compita id was accepted")
	}
	if _, err := DialTunnel(context.Background(), c, "tunnelbox2", Isolation("moon")); err == nil {
		t.Fatal("an unknown isolation level was accepted")
	}
}

// hostFor serves what Compa serves a dial-out compute that connects back.
func hostFor(b *Broker, computeID string) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+LiveConnectPath+"{session}", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Compute-Id") != computeID || r.Header.Get("Authorization") != "Bearer cred" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		b.HandleTunnel(w, r, computeID, r.PathValue("session"))
	})
	return httptest.NewServer(mux)
}

func TestDialOutTunnelConnectsBackWhenAsked(t *testing.T) {
	t.Setenv("COMPA_LIVE_ADDR", echoServer(t))
	b := NewBroker()
	srv := hostFor(b, "outbox")
	defer srv.Close()
	id := Identity{CompaURL: srv.URL, ComputeID: "outbox", Credential: "cred"}

	// The worker's poll loop, in short: it polls, and answers a control job.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.Next(ctx, "outbox", time.Millisecond) // the compute is online
	go func() {
		job, ok := b.Next(ctx, "outbox", 5*time.Second)
		if ok && job.Tunnel != "" {
			connectBack(ctx, id, job.Tunnel, job.CompitaID, job.Isolation, t.Logf)
		}
	}()

	conn, err := b.OpenTunnel(ctx, "outbox", "tunnelbox3", IsolationShared)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	echoThrough(t, conn, "hello through the tunnel")
}

func TestDialOutTunnelFailsFastWhenTheBrowserIsNotUp(t *testing.T) {
	t.Setenv("COMPA_LIVE_ADDR", "127.0.0.1:1")
	b := NewBroker()
	srv := hostFor(b, "outbox2")
	defer srv.Close()
	id := Identity{CompaURL: srv.URL, ComputeID: "outbox2", Credential: "cred"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.Next(ctx, "outbox2", time.Millisecond)
	go func() {
		job, _ := b.Next(ctx, "outbox2", 5*time.Second)
		connectBack(ctx, id, job.Tunnel, job.CompitaID, job.Isolation, t.Logf)
	}()
	conn, err := b.OpenTunnel(ctx, "outbox2", "tunnelbox4", IsolationShared)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// The compute hung up at once, so a reader hears it straight away.
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil || strings.Contains(err.Error(), "timeout") {
		t.Fatalf("read = %v, want the tunnel to be closed by the compute", err)
	}
}

func TestOpenTunnelNeedsAnOnlineComputeAndTheRightSession(t *testing.T) {
	b := NewBroker()
	if _, err := b.OpenTunnel(context.Background(), "ghost", "ana", IsolationShared); err != ErrOffline {
		t.Fatalf("offline compute: err = %v", err)
	}

	srv := hostFor(b, "outbox3")
	defer srv.Close()
	// Nobody asked for this session, so connecting back is refused.
	d := http.Header{"X-Compute-Id": {"outbox3"}, "Authorization": {"Bearer cred"}}
	resp, err := http.DefaultClient.Do(mustReq(t, srv.URL+LiveConnectPath+"unknown", d))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown session = %d", resp.StatusCode)
	}

	// Another compute cannot take over a session asked of this one.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	b.Next(ctx, "outbox3", time.Millisecond)
	done := make(chan error, 1)
	go func() { _, err := b.OpenTunnel(ctx, "outbox3", "ana", IsolationShared); done <- err }()
	job, ok := b.Next(ctx, "outbox3", time.Second)
	if !ok || job.Tunnel == "" {
		t.Fatalf("no control job queued: %+v", job)
	}
	rec := httptest.NewRecorder()
	b.HandleTunnel(rec, httptest.NewRequest("GET", LiveConnectPath+job.Tunnel, nil), "someone-else", job.Tunnel)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("another compute's connect back = %d", rec.Code)
	}
	cancel()
	<-done
}

func mustReq(t *testing.T, url string, h http.Header) *http.Request {
	t.Helper()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header = h
	return req
}

// Where a Compita's live view is depends on its isolation: a Compita in a
// container is shown its own container's display or nothing, never whatever
// else listens on the machine's default address.
func TestLiveAddrKeepsContainerAndMachineBrowsersApart(t *testing.T) {
	addr := echoServer(t)
	t.Setenv("COMPA_LIVE_ADDR", addr)
	for _, iso := range []Isolation{IsolationMachine, IsolationProfile, IsolationShared} {
		if got, ok := LiveAddr("liveaddr1", iso); !ok || got != addr {
			t.Fatalf("%s: LiveAddr = %q, %v; want the machine's own %s", iso, got, ok, addr)
		}
	}
	if got, ok := LiveAddr("liveaddr1", IsolationContainer); ok {
		t.Fatalf("a Compita in a container (with no container running) was shown %q", got)
	}
	if _, ok := LiveAddr("../etc", IsolationShared); ok {
		t.Fatal("a malformed Compita id must find nothing")
	}
	t.Setenv("COMPA_LIVE_ADDR", "127.0.0.1:1")
	if _, ok := LiveAddr("liveaddr1", IsolationShared); ok {
		t.Fatal("nothing listens, so there is no live view")
	}
}
