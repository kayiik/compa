// Compa - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/kayiik/compa/pkg/bus"
)

// The owner's prompts: what the agent asks the owner and waits for, an
// approval request (/approve, /deny) or a set of questions (ask_user). One
// registry holds them all under ids no two waiting prompts share, so an id
// answers one prompt of one kind.

type promptKind int

const (
	promptApproval promptKind = iota
	promptQuestion
)

// promptOutcome is how waiting for a prompt's answer ended.
type promptOutcome int

const (
	promptAnswered promptOutcome = iota
	promptExpired
	// promptUnavailable: the prompt could not be posted, or ctx ended first.
	promptUnavailable
)

// errQuestionOpen refuses a question in a chat where one already waits.
var errQuestionOpen = errors.New("a question is already waiting in this chat")

// promptAnswer is the owner's answer to a prompt.
type promptAnswer struct {
	// approve answers an approval request.
	approve bool
	// cancelled and answers answer a question prompt.
	cancelled bool
	answers   []bus.QuestionAnswer
	// by names who answered a question: "owner" or "hook:<name>".
	by string
}

// operatorPrompt is a prompt waiting for its answer.
type operatorPrompt struct {
	kind promptKind
	// chat is the chat a question waits in; at most one per chat.
	chat      string
	questions []bus.Question
	answer    chan promptAnswer
}

// operatorPrompts holds the prompts waiting for the owner's answer.
type operatorPrompts struct {
	mu      sync.Mutex
	pending map[string]*operatorPrompt
}

func newOperatorPrompts() *operatorPrompts {
	return &operatorPrompts{pending: make(map[string]*operatorPrompt)}
}

// chatKey names a chat in the registry.
func chatKey(channel, chatID string) string {
	return strings.TrimSpace(channel) + "\x00" + strings.TrimSpace(chatID)
}

// wait registers p under a new id, posts it through post and waits for the
// answer, timeout, or the end of ctx. A question is refused with
// errQuestionOpen while another waits in its chat.
func (r *operatorPrompts) wait(
	ctx context.Context,
	p *operatorPrompt,
	timeout time.Duration,
	post func(id string) error,
) (promptAnswer, promptOutcome, error) {
	p.answer = make(chan promptAnswer, 1)
	r.mu.Lock()
	if p.kind == promptQuestion {
		for _, other := range r.pending {
			if other.kind == promptQuestion && other.chat == p.chat {
				r.mu.Unlock()
				return promptAnswer{}, promptUnavailable, errQuestionOpen
			}
		}
	}
	id := r.newIDLocked()
	r.pending[id] = p
	r.mu.Unlock()
	defer r.remove(id)

	if err := post(id); err != nil {
		return promptAnswer{}, promptUnavailable, err
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case answer := <-p.answer:
		return answer, promptAnswered, nil
	case <-timer.C:
		return promptAnswer{}, promptExpired, nil
	case <-ctx.Done():
		return promptAnswer{}, promptUnavailable, ctx.Err()
	}
}

func (r *operatorPrompts) remove(id string) {
	r.mu.Lock()
	delete(r.pending, id)
	r.mu.Unlock()
}

// deliver hands answer to the prompt id of kind. It reports false when no
// prompt of that id and kind waits: unknown, already answered or expired.
func (r *operatorPrompts) deliver(id string, kind promptKind, answer promptAnswer) bool {
	id = strings.ToLower(strings.TrimSpace(id))
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.pending[id]
	if !ok || p.kind != kind {
		return false
	}
	delete(r.pending, id)
	p.answer <- answer // buffered: the waiter takes it, or has stopped waiting
	return true
}

// question returns the question prompt waiting in chat, if any.
func (r *operatorPrompts) question(chat string) (string, []bus.Question, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, p := range r.pending {
		if p.kind == promptQuestion && p.chat == chat {
			return id, p.questions, true
		}
	}
	return "", nil, false
}

// questionByID returns the questions of the question prompt id.
func (r *operatorPrompts) questionByID(id string) ([]bus.Question, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.pending[id]
	if !ok || p.kind != promptQuestion {
		return nil, false
	}
	return p.questions, true
}

// newIDLocked returns an id no waiting prompt has. Must be called with the
// lock held.
func (r *operatorPrompts) newIDLocked() string {
	for {
		buf := make([]byte, approvalIDLength)
		_, _ = rand.Read(buf)
		for i, b := range buf {
			buf[i] = approvalIDAlphabet[int(b)%len(approvalIDAlphabet)]
		}
		if id := string(buf); r.pending[id] == nil {
			return id
		}
	}
}
