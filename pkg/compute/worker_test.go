package compute

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A fake kernel that echoes its arguments and its COMPA_HOME.
func fakeKernel(t *testing.T) string {
	t.Helper()
	skipWithoutShell(t)
	p := filepath.Join(t.TempDir(), "kernel")
	script := "#!/bin/sh\necho \"reply to $5 in $COMPA_HOME\"\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunnerUsesOwnHomeAndSeedsConfig(t *testing.T) {
	home := t.TempDir()
	_ = os.WriteFile(filepath.Join(home, "config.json"), []byte(`{"x":1}`), 0o600)
	r := Runner{Home: home, Kernel: fakeKernel(t)}
	out, err := r.Run(context.Background(), Turn{CompitaID: "ana", Isolation: IsolationShared, Session: "s", Message: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	want := "reply to hello in " + filepath.Join(home, "compitas", "ana")
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
	if _, err := os.Stat(filepath.Join(home, "compitas", "ana", "config.json")); err != nil {
		t.Fatal("config was not seeded")
	}
}

func TestContainerCommandMountsOnlyOwnDir(t *testing.T) {
	r := Runner{Home: t.TempDir()}
	cmd, err := r.command(context.Background(), Turn{CompitaID: "ana", Isolation: IsolationContainer, Session: "s", Message: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, a := range cmd.Args {
		joined += a + " "
	}
	for _, need := range []string{"docker run --rm", r.CompitaHome("ana") + ":/data", DefaultImage, "agent -s s -m hi"} {
		if !contains(joined, need) {
			t.Fatalf("missing %q in %q", need, joined)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestBrokerWorkerRoundTrip(t *testing.T) {
	b := NewBroker()
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+PollPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer cred" {
			w.WriteHeader(401)
			return
		}
		job, ok := b.Next(r.Context(), "box", time.Second)
		if !ok {
			w.WriteHeader(204)
			return
		}
		_ = json.NewEncoder(w).Encode(job)
	})
	mux.HandleFunc("POST "+ResultPath, func(w http.ResponseWriter, r *http.Request) {
		var res Result
		_ = json.NewDecoder(r.Body).Decode(&res)
		b.Complete("box", res)
		w.WriteHeader(204)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if _, err := b.Submit(context.Background(), "box", Job{}); err != ErrOffline {
		t.Fatalf("offline submit = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	home := t.TempDir()
	go Serve(ctx, Identity{CompaURL: srv.URL, ComputeID: "box", Credential: "cred"},
		Runner{Home: home, Kernel: fakeKernel(t)}, func(string, ...any) {})

	deadline := time.Now().Add(5 * time.Second)
	for !b.Online("box") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	sctx, scancel := context.WithTimeout(ctx, 10*time.Second)
	defer scancel()
	res, err := b.Submit(sctx, "box", Job{CompitaID: "ana", Isolation: IsolationShared, Session: "s", Message: "ping"})
	if err != nil || res.Error != "" {
		t.Fatalf("submit: %v %+v", err, res)
	}
	if !contains(res.Reply, "reply to ping") {
		t.Fatalf("reply = %q", res.Reply)
	}
}
