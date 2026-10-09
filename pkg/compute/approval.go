package compute

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ApprovalAsk is what a Compita's approver hook sends the Compa host when a
// tool call needs the owner's say-so.
type ApprovalAsk struct {
	From      string         `json:"from"`
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// Approval is a request waiting for the owner.
type Approval struct {
	ID        string    `json:"id"`
	CompitaID string    `json:"compita_id"`
	Tool      string    `json:"tool"`
	Summary   string    `json:"summary"`
	At        time.Time `json:"at"`
}

type answer struct {
	approved bool
	reason   string
}

// Inbox holds the approvals owed to the owner. The turn that asked stays
// parked inside its tool call until the owner answers, so an answer resumes
// the very same work.
type Inbox struct {
	mu      sync.Mutex
	pending map[string]*Approval
	waiters map[string]chan answer
	seq     int
}

// MaxPendingPerCompita keeps a runaway Compita from burying the owner.
const MaxPendingPerCompita = 10

// NewInbox returns an empty inbox.
func NewInbox() *Inbox {
	return &Inbox{pending: map[string]*Approval{}, waiters: map[string]chan answer{}}
}

// Summarize says what a call does in words the owner can judge. The whole
// command or arguments are shown, up to a generous cap.
func Summarize(tool string, args map[string]any) string {
	const maxRunes = 4000
	if cmd, ok := args["command"].(string); ok && cmd != "" {
		return clip(fmt.Sprintf("%s: %s", tool, cmd), maxRunes)
	}
	raw, _ := json.Marshal(args)
	if len(args) == 0 {
		return tool
	}
	return clip(fmt.Sprintf("%s %s", tool, raw), maxRunes)
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + fmt.Sprintf("… (%d more characters)", len(r)-n)
}

// Ask files the request and waits for the owner's answer. No answer within
// wait, or the caller going away, denies it.
func (in *Inbox) Ask(ctx context.Context, a ApprovalAsk, wait time.Duration) (approved bool, reason string) {
	in.mu.Lock()
	count := 0
	for _, p := range in.pending {
		if p.CompitaID == a.From {
			count++
		}
	}
	if count >= MaxPendingPerCompita {
		in.mu.Unlock()
		return false, "too many approvals are already waiting for the owner"
	}
	in.seq++
	id := fmt.Sprintf("%s-%d", time.Now().Format("150405"), in.seq)
	item := &Approval{ID: id, CompitaID: a.From, Tool: a.Tool, Summary: Summarize(a.Tool, a.Arguments), At: time.Now()}
	ch := make(chan answer, 1)
	in.pending[id], in.waiters[id] = item, ch
	in.mu.Unlock()

	defer func() {
		in.mu.Lock()
		delete(in.pending, id)
		delete(in.waiters, id)
		in.mu.Unlock()
	}()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case r := <-ch:
		return r.approved, r.reason
	case <-timer.C:
		return false, fmt.Sprintf("the owner did not answer within %v", wait.Round(time.Second))
	case <-ctx.Done():
		return false, "the request was withdrawn before the owner answered"
	}
}

// ErrNoSuchApproval means the request was already answered or has gone.
var ErrNoSuchApproval = errors.New("compute: that approval is no longer waiting")

// Decide gives the owner's answer to a waiting request.
func (in *Inbox) Decide(id string, approved bool) (Approval, error) {
	in.mu.Lock()
	item, ok := in.pending[id]
	ch := in.waiters[id]
	if ok {
		delete(in.pending, id)
		delete(in.waiters, id)
	}
	in.mu.Unlock()
	if !ok {
		return Approval{}, ErrNoSuchApproval
	}
	reason := "the owner approved it"
	if !approved {
		reason = "the owner denied it"
	}
	ch <- answer{approved: approved, reason: reason}
	return *item, nil
}

// Pending lists what waits for the owner, oldest first; compitaID narrows it.
func (in *Inbox) Pending(compitaID string) []Approval {
	in.mu.Lock()
	defer in.mu.Unlock()
	out := []Approval{}
	for _, p := range in.pending {
		if compitaID == "" || strings.EqualFold(p.CompitaID, compitaID) {
			out = append(out, *p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}
