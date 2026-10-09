package agent

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/kayiik/compa/pkg/bus"
	runtimeevents "github.com/kayiik/compa/pkg/events"
)

// TurnEvent is something a one-shot terminal turn does while it runs: the
// answer being written, a tool being used. A caller that installs a sink with
// SetTerminalEvents gets the answer as it is written instead of only whole.
type TurnEvent struct {
	// Type is "segment" (a new model call starts writing), "reset" (the
	// provider revised the text so far; start the segment over), "delta" or
	// "tool".
	Type string `json:"type"`
	// Text is, for a delta, the text just written.
	Text string `json:"text,omitempty"`
	// Tool, State and Detail describe a tool call: State is "start", "end" or
	// "error", and Detail says what a started call was asked to do.
	Tool   string `json:"tool,omitempty"`
	State  string `json:"state,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type terminalEvents struct{ sink func(TurnEvent) }

const terminalEventsHook = "terminal-events"

// SetTerminalEvents makes the terminal's turns stream to sink, or stop
// streaming when sink is nil. Without a sink a terminal turn returns its
// answer whole, as it always has.
func (al *AgentLoop) SetTerminalEvents(sink func(TurnEvent)) {
	if al == nil {
		return
	}
	al.UnmountHook(terminalEventsHook)
	if sink == nil {
		al.terminalEvents.Store(nil)
		return
	}
	al.terminalEvents.Store(&terminalEvents{sink: sink})
	_ = al.MountHook(NamedHook(terminalEventsHook, terminalToolObserver{al: al}))
}

// terminalStreaming reports whether the turn is a terminal turn whose caller
// listens for events. Such a turn streams without a channel's streaming
// settings or a bus to publish on: the sink is its channel.
func (p *Pipeline) terminalStreaming(ts *turnState) bool {
	return p != nil && p.al != nil && ts != nil && ts.channel == terminalChannel && p.al.terminalEvents.Load() != nil
}

// streamerFor returns the streamer for a model call of the turn.
func (p *Pipeline) streamerFor(ctx context.Context, ts *turnState) (bus.Streamer, bool) {
	if p.terminalStreaming(ts) {
		return &terminalStream{sink: p.al.terminalEvents.Load().sink}, true
	}
	if p == nil || p.Bus == nil {
		return nil, false
	}
	return p.Bus.GetStreamer(ctx, ts.channel, ts.chatID, ts.sessionKey)
}

// terminalStream turns the text so far, as a streaming provider reports it,
// into the text just written.
type terminalStream struct {
	sink    func(TurnEvent)
	last    string
	started bool
}

func (s *terminalStream) Update(_ context.Context, content string) error {
	if !s.started {
		s.started = true
		s.sink(TurnEvent{Type: "segment"})
	}
	if strings.HasPrefix(content, s.last) {
		if delta := content[len(s.last):]; delta != "" {
			s.sink(TurnEvent{Type: "delta", Text: delta})
		}
	} else {
		s.sink(TurnEvent{Type: "reset"})
		s.sink(TurnEvent{Type: "delta", Text: content})
	}
	s.last = content
	return nil
}

// Finalize and Cancel have nothing to do: the caller gets the whole answer
// when the turn returns.
func (s *terminalStream) Finalize(context.Context, string) error { return nil }
func (s *terminalStream) Cancel(context.Context)                 {}

// terminalToolObserver reports the tools a terminal turn uses.
type terminalToolObserver struct{ al *AgentLoop }

func (o terminalToolObserver) OnRuntimeEvent(_ context.Context, evt runtimeevents.Event) error {
	te := o.al.terminalEvents.Load()
	if te == nil {
		return nil
	}
	switch evt.Kind {
	case runtimeevents.KindAgentToolExecStart:
		switch p := evt.Payload.(type) {
		case ToolExecStartPayload:
			te.sink(TurnEvent{Type: "tool", Tool: p.Tool, State: "start", Detail: toolDetail(p.Arguments)})
		case *ToolExecStartPayload:
			if p != nil {
				te.sink(TurnEvent{Type: "tool", Tool: p.Tool, State: "start", Detail: toolDetail(p.Arguments)})
			}
		}
	case runtimeevents.KindAgentToolExecEnd:
		var tool string
		var failed bool
		switch p := evt.Payload.(type) {
		case ToolExecEndPayload:
			tool, failed = p.Tool, p.IsError
		case *ToolExecEndPayload:
			if p == nil {
				return nil
			}
			tool, failed = p.Tool, p.IsError
		default:
			return nil
		}
		state := "end"
		if failed {
			state = "error"
		}
		te.sink(TurnEvent{Type: "tool", Tool: tool, State: state})
	}
	return nil
}

// toolDetail says what a tool call was asked to do: a command as it is, other
// arguments compactly, clipped.
func toolDetail(args map[string]any) string {
	const max = 200
	var s string
	if cmd, ok := args["command"].(string); ok && cmd != "" {
		s = cmd
	} else if len(args) > 0 {
		raw, _ := json.Marshal(args)
		s = string(raw)
	}
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max]) + "…"
	}
	return s
}
