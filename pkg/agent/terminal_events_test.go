package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/kayiik/compa/pkg/bus"
	runtimeevents "github.com/kayiik/compa/pkg/events"
	"github.com/kayiik/compa/pkg/providers"
)

type eventLog struct {
	mu sync.Mutex
	ev []TurnEvent
}

func (l *eventLog) add(e TurnEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ev = append(l.ev, e)
}

func (l *eventLog) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var parts []string
	for _, e := range l.ev {
		parts = append(parts, e.Type+":"+e.Text)
	}
	return strings.Join(parts, "|")
}

func TestTerminalTurnStreamsTheAnswerToItsSink(t *testing.T) {
	cfg := newConfiguredStreamingTestConfig(t, false, true, nil)
	provider := &configuredStreamingProvider{streamPlan: []configuredStreamingCall{{
		chunks:   []string{"Hel", "Hello", "Hello wor"},
		response: &providers.LLMResponse{Content: "Hello world"},
	}}}
	al := newConfiguredStreamingLoop(cfg, bus.NewMessageBus(), provider)
	var log eventLog
	al.SetTerminalEvents(log.add)

	reply, err := al.ProcessDirect(context.Background(), "hi", "cli:test")
	if err != nil {
		t.Fatal(err)
	}
	if reply != "Hello world" {
		t.Fatalf("the turn still returns the whole answer: %q", reply)
	}
	if got, want := log.text(), "segment:|delta:Hel|delta:lo|delta: wor"; got != want {
		t.Fatalf("events = %q, want %q", got, want)
	}
}

func TestTerminalTurnWithoutASinkDoesNotStream(t *testing.T) {
	cfg := newConfiguredStreamingTestConfig(t, false, true, nil)
	provider := &configuredStreamingProvider{}
	al := newConfiguredStreamingLoop(cfg, bus.NewMessageBus(), provider)

	reply, err := al.ProcessDirect(context.Background(), "hi", "cli:test")
	if err != nil {
		t.Fatal(err)
	}
	if reply != "chat response" || provider.streamCalls != 0 || provider.chatCalls == 0 {
		t.Fatalf("a terminal turn with no sink must call the model whole: reply=%q stream=%d chat=%d", reply, provider.streamCalls, provider.chatCalls)
	}

	// Installing and removing a sink leaves it as it was.
	al.SetTerminalEvents(func(TurnEvent) {})
	al.SetTerminalEvents(nil)
	provider.chatCalls = 0
	if _, err := al.ProcessDirect(context.Background(), "again", "cli:test"); err != nil || provider.streamCalls != 0 || provider.chatCalls == 0 {
		t.Fatalf("after removing the sink: err=%v stream=%d chat=%d", err, provider.streamCalls, provider.chatCalls)
	}
}

func TestTerminalStreamTurnsTheTextSoFarIntoDeltas(t *testing.T) {
	var log eventLog
	s := &terminalStream{sink: log.add}
	for _, so := range []string{"Hello", "Hello there", "Help me"} {
		if err := s.Update(context.Background(), so); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := log.text(), "segment:|delta:Hello|delta: there|reset:|delta:Help me"; got != want {
		t.Fatalf("events = %q, want %q", got, want)
	}
}

func TestTerminalToolObserverReportsToolsAndStopsWithTheSink(t *testing.T) {
	al := &AgentLoop{}
	var log eventLog
	al.terminalEvents.Store(&terminalEvents{sink: log.add})
	o := terminalToolObserver{al: al}
	ctx := context.Background()
	_ = o.OnRuntimeEvent(ctx, runtimeevents.Event{Kind: runtimeevents.KindAgentToolExecStart, Payload: ToolExecStartPayload{Tool: "exec", Arguments: map[string]any{"command": "ls -la"}}})
	_ = o.OnRuntimeEvent(ctx, runtimeevents.Event{Kind: runtimeevents.KindAgentToolExecEnd, Payload: ToolExecEndPayload{Tool: "exec"}})
	_ = o.OnRuntimeEvent(ctx, runtimeevents.Event{Kind: runtimeevents.KindAgentToolExecEnd, Payload: &ToolExecEndPayload{Tool: "web", IsError: true}})
	_ = o.OnRuntimeEvent(ctx, runtimeevents.Event{Kind: runtimeevents.KindAgentError, Payload: ErrorPayload{}})
	log.mu.Lock()
	got := log.ev
	log.mu.Unlock()
	if len(got) != 3 ||
		got[0] != (TurnEvent{Type: "tool", Tool: "exec", State: "start", Detail: "ls -la"}) ||
		got[1] != (TurnEvent{Type: "tool", Tool: "exec", State: "end"}) ||
		got[2] != (TurnEvent{Type: "tool", Tool: "web", State: "error"}) {
		t.Fatalf("events = %+v", got)
	}

	al.terminalEvents.Store(nil)
	_ = o.OnRuntimeEvent(ctx, runtimeevents.Event{Kind: runtimeevents.KindAgentToolExecEnd, Payload: ToolExecEndPayload{Tool: "exec"}})
	log.mu.Lock()
	n := len(log.ev)
	log.mu.Unlock()
	if n != 3 {
		t.Fatal("nothing must be reported once the sink is gone")
	}
}

func TestToolDetailIsTheCommandOrCompactArgumentsClipped(t *testing.T) {
	if d := toolDetail(map[string]any{"command": "echo hi"}); d != "echo hi" {
		t.Fatalf("detail = %q", d)
	}
	if d := toolDetail(map[string]any{"url": "https://x"}); d != `{"url":"https://x"}` {
		t.Fatalf("detail = %q", d)
	}
	if d := toolDetail(map[string]any{"command": strings.Repeat("a", 500)}); len([]rune(d)) != 201 {
		t.Fatalf("a long command must be clipped: %d runes", len([]rune(d)))
	}
	if toolDetail(nil) != "" {
		t.Fatal("no arguments, no detail")
	}
}
