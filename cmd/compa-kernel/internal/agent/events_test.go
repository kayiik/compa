package agent

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kayiik/compa/pkg/agent"
)

type fakeRunner struct {
	sink  func(agent.TurnEvent)
	reply string
	err   error
	gone  bool
}

func (f *fakeRunner) SetTerminalEvents(sink func(agent.TurnEvent)) {
	if sink == nil {
		f.gone = true
	}
	f.sink = sink
}

func (f *fakeRunner) ProcessDirect(context.Context, string, string) (string, error) {
	f.sink(agent.TurnEvent{Type: "segment"})
	f.sink(agent.TurnEvent{Type: "delta", Text: "Hi"})
	f.sink(agent.TurnEvent{Type: "tool", Tool: "exec", State: "start", Detail: "ls"})
	return f.reply, f.err
}

func TestRunWithEventsWritesJSONLinesThenTheWholeAnswer(t *testing.T) {
	var out bytes.Buffer
	f := &fakeRunner{reply: "Hi there"}
	if err := runWithEvents(context.Background(), f, "hello", "s", &out); err != nil {
		t.Fatal(err)
	}
	want := `{"type":"segment"}
{"type":"delta","text":"Hi"}
{"type":"tool","tool":"exec","state":"start","detail":"ls"}
{"type":"final","text":"Hi there"}
`
	if out.String() != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out.String(), want)
	}
	if !f.gone {
		t.Fatal("the sink must be removed when the turn ends")
	}
}

func TestRunWithEventsReportsAFailedTurnAsAnErrorLine(t *testing.T) {
	var out bytes.Buffer
	err := runWithEvents(context.Background(), &fakeRunner{err: errors.New("quota exceeded")}, "hello", "s", &out)
	if err == nil || !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("err = %v", err)
	}
	if !strings.HasSuffix(out.String(), `{"type":"error","text":"quota exceeded"}`+"\n") {
		t.Fatalf("the failure must be the last line: %s", out.String())
	}
}
