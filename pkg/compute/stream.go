package compute

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// StreamEvent is something a Compita's turn does while it runs, as the kernel
// reports it with `agent --events`: the answer being written ("segment",
// "reset", "delta") or a tool being used ("tool").
type StreamEvent struct {
	Type   string `json:"type"`
	Text   string `json:"text,omitempty"`
	Tool   string `json:"tool,omitempty"`
	State  string `json:"state,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// errNoEventsFlag marks a kernel that predates `agent --events`.
var errNoEventsFlag = errors.New("compute: this kernel cannot stream")

// RunStream is Run that also reports what the turn does as it happens. A
// kernel that cannot stream still answers: the turn runs whole.
func (r Runner) RunStream(ctx context.Context, t Turn, onEvent func(StreamEvent)) (string, error) {
	defer lockCompita(t.CompitaID)()
	if t.Isolation == IsolationContainer {
		removeContainers(t.CompitaID)
	}
	t.Events = onEvent != nil
	reply, err := r.runOnce(ctx, t, onEvent)
	if errors.Is(err, errNoEventsFlag) {
		t.Events = false
		return r.runOnce(ctx, t, nil)
	}
	return reply, err
}

// Run sends one message to the Compita and returns its reply.
func (r Runner) Run(ctx context.Context, t Turn) (string, error) {
	return r.RunStream(ctx, t, nil)
}

func (r Runner) runOnce(ctx context.Context, t Turn, onEvent func(StreamEvent)) (string, error) {
	cmd, err := r.command(ctx, t)
	if err != nil {
		return "", err
	}
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if !t.Events {
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			return "", runFailure(err, errb.String())
		}
		return strings.TrimSpace(out.String()), nil
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	var final, failure string
	var raw strings.Builder // anything that is not an event: a plain answer
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
	for sc.Scan() {
		line := sc.Bytes()
		var ev StreamEvent
		if json.Unmarshal(line, &ev) != nil || ev.Type == "" {
			raw.Write(line)
			raw.WriteByte('\n')
			continue
		}
		switch ev.Type {
		case "final":
			final = ev.Text
		case "error":
			failure = ev.Text
		default:
			onEvent(ev)
		}
	}
	scanErr := sc.Err()
	waitErr := cmd.Wait()
	if waitErr != nil {
		if strings.Contains(errb.String(), "unknown flag: --events") {
			return "", errNoEventsFlag
		}
		if failure != "" {
			return "", fmt.Errorf("compita run failed: %w: %s", waitErr, failure)
		}
		return "", runFailure(waitErr, errb.String())
	}
	if scanErr != nil {
		return "", fmt.Errorf("compita run failed: reading the turn's events: %w", scanErr)
	}
	if final != "" || failure == "" && raw.Len() == 0 {
		return strings.TrimSpace(final), nil
	}
	if failure != "" {
		return "", errors.New("compita run failed: " + failure)
	}
	return strings.TrimSpace(raw.String()), nil
}

func runFailure(err error, stderr string) error {
	detail := strings.TrimSpace(stderr)
	if len(detail) > 600 {
		detail = detail[len(detail)-600:]
	}
	return fmt.Errorf("compita run failed: %w: %s", err, detail)
}
