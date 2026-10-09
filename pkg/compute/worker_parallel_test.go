package compute

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestWorkerRunsOneTurnAtATimePerCompita(t *testing.T) {
	skipWithoutShell(t)
	kernel := filepath.Join(t.TempDir(), "kernel")
	if err := os.WriteFile(kernel, []byte("#!/bin/sh\nsleep 0.5\necho done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := Runner{Home: t.TempDir(), Kernel: kernel}
	began := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.Run(context.Background(), Turn{CompitaID: "same", Isolation: IsolationShared, Session: "s", Message: "m"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if took := time.Since(began); took < 950*time.Millisecond {
		t.Errorf("two turns of one Compita overlapped (%v): a turn the host gave up on must not run beside its replacement", took)
	}
}

func TestWorkerRunsJobsTogetherAndStaysOnline(t *testing.T) {
	skipWithoutShell(t)
	b := NewBroker()
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+PollPath, func(w http.ResponseWriter, r *http.Request) {
		job, ok := b.Next(r.Context(), "box", 200*time.Millisecond)
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

	kernel := filepath.Join(t.TempDir(), "kernel")
	if err := os.WriteFile(kernel, []byte("#!/bin/sh\nsleep 1\necho done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Serve(ctx, Identity{CompaURL: srv.URL, ComputeID: "box", Credential: "c"}, Runner{Home: t.TempDir(), Kernel: kernel}, func(string, ...any) {})
	for deadline := time.Now().Add(5 * time.Second); !b.Online("box") && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
	}

	began := time.Now()
	var wg sync.WaitGroup
	for _, id := range []string{"ana", "bo"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := b.Submit(context.Background(), "box", Job{CompitaID: id, Isolation: IsolationShared, Session: "s", Message: "m"})
			if err != nil || res.Reply != "done" {
				t.Errorf("%s: %v %+v", id, err, res)
			}
		}()
	}
	time.Sleep(500 * time.Millisecond)
	if !b.Online("box") {
		t.Error("a compute busy with jobs must keep polling and so stay online")
	}
	wg.Wait()
	if took := time.Since(began); took > 1900*time.Millisecond {
		t.Errorf("two Compitas on one compute should work together, took %v", took)
	}
}
