package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/kayiik/compa/pkg/agent"
)

// turnRunner is what runWithEvents needs of the agent loop.
type turnRunner interface {
	SetTerminalEvents(sink func(agent.TurnEvent))
	ProcessDirect(ctx context.Context, content, sessionKey string) (string, error)
}

// eventLine is the last line of an --events run: the whole answer, or the
// error that ended the turn.
type eventLine struct {
	Type string `json:"type"` // "final" or "error"
	Text string `json:"text"`
}

// runWithEvents runs one message and writes what happens to out as it happens,
// one JSON object per line: the answer being written and the tools used (see
// agent.TurnEvent), then a final line with the whole answer. A program that
// runs the kernel reads the lines to show the work live.
func runWithEvents(ctx context.Context, loop turnRunner, message, sessionKey string, out io.Writer) error {
	var mu sync.Mutex
	enc := json.NewEncoder(out)
	emit := func(v any) {
		mu.Lock()
		defer mu.Unlock()
		_ = enc.Encode(v)
	}
	loop.SetTerminalEvents(func(e agent.TurnEvent) { emit(e) })
	reply, err := loop.ProcessDirect(ctx, message, sessionKey)
	loop.SetTerminalEvents(nil)
	if err != nil {
		emit(eventLine{Type: "error", Text: err.Error()})
		return fmt.Errorf("error processing message: %w", err)
	}
	emit(eventLine{Type: "final", Text: reply})
	return nil
}
