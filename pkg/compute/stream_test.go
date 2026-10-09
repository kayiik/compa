package compute

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func scriptKernel(t *testing.T, body string) string {
	t.Helper()
	skipWithoutShell(t)
	p := filepath.Join(t.TempDir(), "kernel")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunStreamDeliversEventsWhileTheTurnRuns(t *testing.T) {
	kernel := scriptKernel(t, `case "$*" in *--events*)
echo '{"type":"segment"}'
sleep 0.4
echo '{"type":"delta","text":"Hi"}'
echo 'a log line that is not an event'
echo '{"type":"tool","tool":"exec","state":"start","detail":"ls"}'
sleep 0.4
echo '{"type":"final","text":"Hi there"}';;
*) echo plain;; esac
`)
	r := Runner{Home: t.TempDir(), Kernel: kernel}
	var mu sync.Mutex
	var got []StreamEvent
	var firstAt time.Duration
	began := time.Now()
	reply, err := r.RunStream(context.Background(), Turn{CompitaID: "ana", Isolation: IsolationShared, Session: "s", Message: "hi"}, func(e StreamEvent) {
		mu.Lock()
		defer mu.Unlock()
		if len(got) == 0 {
			firstAt = time.Since(began)
		}
		got = append(got, e)
	})
	total := time.Since(began)
	if err != nil || reply != "Hi there" {
		t.Fatalf("reply = %q, %v", reply, err)
	}
	if len(got) != 3 || got[0].Type != "segment" || got[1].Text != "Hi" || got[2] != (StreamEvent{Type: "tool", Tool: "exec", State: "start", Detail: "ls"}) {
		t.Fatalf("events = %+v", got)
	}
	if firstAt > total-500*time.Millisecond {
		t.Fatalf("the first event came at %v of %v: events must arrive while the turn runs", firstAt, total)
	}
}

func TestRunStreamFailsWithTheKernelsOwnError(t *testing.T) {
	kernel := scriptKernel(t, "echo '{\"type\":\"error\",\"text\":\"quota exceeded\"}'\nexit 1\n")
	r := Runner{Home: t.TempDir(), Kernel: kernel}
	_, err := r.RunStream(context.Background(), Turn{CompitaID: "ana", Isolation: IsolationShared, Session: "s", Message: "hi"}, func(StreamEvent) {})
	if err == nil || !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunStreamFallsBackWhenTheKernelCannotStream(t *testing.T) {
	kernel := scriptKernel(t, `case "$*" in *--events*) echo "Error: unknown flag: --events" >&2; exit 1;; esac
echo "plain reply"
`)
	r := Runner{Home: t.TempDir(), Kernel: kernel}
	events := 0
	reply, err := r.RunStream(context.Background(), Turn{CompitaID: "ana", Isolation: IsolationShared, Session: "s", Message: "hi"}, func(StreamEvent) { events++ })
	if err != nil || reply != "plain reply" || events != 0 {
		t.Fatalf("an older kernel must still answer: %q %v (%d events)", reply, err, events)
	}
}

func TestRunStreamUsesThePlainAnswerWhenNoFinalLineComes(t *testing.T) {
	kernel := scriptKernel(t, "echo 'just text'\n")
	r := Runner{Home: t.TempDir(), Kernel: kernel}
	reply, err := r.RunStream(context.Background(), Turn{CompitaID: "ana", Isolation: IsolationShared, Session: "s", Message: "hi"}, func(StreamEvent) {})
	if err != nil || reply != "just text" {
		t.Fatalf("reply = %q, %v", reply, err)
	}
}

func TestRunWithoutAListenerDoesNotAskForEvents(t *testing.T) {
	kernel := scriptKernel(t, `case "$*" in *--events*) echo "events requested" ;; *) echo "whole" ;; esac
`)
	r := Runner{Home: t.TempDir(), Kernel: kernel}
	reply, err := r.Run(context.Background(), Turn{CompitaID: "ana", Isolation: IsolationShared, Session: "s", Message: "hi"})
	if err != nil || reply != "whole" {
		t.Fatalf("reply = %q, %v", reply, err)
	}
}
