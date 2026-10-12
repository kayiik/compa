package web

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/kayiik/compa/pkg/bus"
	"github.com/kayiik/compa/pkg/channels"
	"github.com/kayiik/compa/pkg/identity"
)

// Questions with choices (the agent's ask_user tool): the web chat shows them
// as question.create frames instead of a numbered text message, and takes the
// owner's question.answer or question.cancel. A question waits until the
// agent resolves it; a socket that joins the session meanwhile gets its
// question.create again, with the same question_id.

// webQuestion is a question waiting in a session.
type webQuestion struct {
	prompt  bus.QuestionPrompt
	respond channels.QuestionResponder
}

var _ channels.QuestionPresenter = (*WebChannel)(nil)

// ShowQuestion implements channels.QuestionPresenter. The question waits for
// a socket even when none is open.
func (c *WebChannel) ShowQuestion(
	_ context.Context,
	chatID string,
	prompt bus.QuestionPrompt,
	respond channels.QuestionResponder,
) error {
	if prompt.ID == "" || respond == nil {
		return errors.New("question without id or responder")
	}
	c.questionsMu.Lock()
	if c.questions == nil {
		c.questions = make(map[string]map[string]webQuestion)
	}
	byID := c.questions[chatID]
	if byID == nil {
		byID = make(map[string]webQuestion)
		c.questions[chatID] = byID
	}
	byID[prompt.ID] = webQuestion{prompt: prompt, respond: respond}
	c.questionsMu.Unlock()

	_ = c.broadcastToSession(chatID, questionCreateFrame(prompt))
	return nil
}

// QuestionResolved implements channels.QuestionPresenter: the session's
// sockets get question.resolved, to drop the question.
func (c *WebChannel) QuestionResolved(_ context.Context, chatID, id, outcome string) {
	c.questionsMu.Lock()
	if byID := c.questions[chatID]; byID != nil {
		delete(byID, id)
		if len(byID) == 0 {
			delete(c.questions, chatID)
		}
	}
	c.questionsMu.Unlock()
	_ = c.broadcastToSession(chatID, newMessage(TypeQuestionResolved, map[string]any{
		PayloadKeyQuestionID: id,
		PayloadKeyOutcome:    outcome,
	}))
}

func questionCreateFrame(prompt bus.QuestionPrompt) WebMessage {
	return newMessage(TypeQuestionCreate, map[string]any{
		PayloadKeyQuestionID: prompt.ID,
		PayloadKeyQuestions:  prompt.Questions,
		PayloadKeyExpiresAt:  prompt.ExpiresAt.UnixMilli(),
	})
}

// waitingQuestions returns the questions waiting in chatID, oldest first.
func (c *WebChannel) waitingQuestions(chatID string) []webQuestion {
	c.questionsMu.Lock()
	defer c.questionsMu.Unlock()
	waiting := make([]webQuestion, 0, len(c.questions[chatID]))
	for _, q := range c.questions[chatID] {
		waiting = append(waiting, q)
	}
	sort.Slice(waiting, func(i, j int) bool {
		return waiting[i].prompt.ExpiresAt.Before(waiting[j].prompt.ExpiresAt)
	})
	return waiting
}

func (c *WebChannel) waitingQuestion(chatID, id string) (webQuestion, bool) {
	c.questionsMu.Lock()
	defer c.questionsMu.Unlock()
	q, ok := c.questions[chatID][id]
	return q, ok
}

// resendQuestions sends a socket that joined a session the questions waiting
// there.
func (c *WebChannel) resendQuestions(pc *webConn) {
	for _, q := range c.waitingQuestions("web:" + pc.sessionID) {
		frame := questionCreateFrame(q.prompt)
		frame.SessionID = pc.sessionID
		_ = pc.writeJSON(frame)
	}
}

// handleQuestionAnswer takes a question.answer, or a question.cancel when
// cancel is set. Only the owner's counts, as only the owner's messages do.
func (c *WebChannel) handleQuestionAnswer(pc *webConn, msg WebMessage, cancel bool) {
	refuse := func(code, message string) {
		_ = pc.writeJSON(newErrorWithPayload(code, message, map[string]any{PayloadKeyRequestID: msg.ID}))
	}
	senderID := "web-user"
	sender := bus.SenderInfo{
		Platform:    "web",
		PlatformID:  senderID,
		CanonicalID: identity.BuildCanonicalID("web", senderID),
	}
	allowed := c.IsAllowedSender
	if c.allowSenderFn != nil {
		allowed = c.allowSenderFn
	}
	if !allowed(sender) {
		refuse("forbidden", "only the owner can answer questions")
		return
	}
	sessionID := msg.SessionID
	if sessionID == "" {
		sessionID = pc.sessionID
	}
	id, _ := msg.Payload[PayloadKeyQuestionID].(string)
	q, ok := c.waitingQuestion("web:"+sessionID, strings.TrimSpace(id))
	if !ok {
		refuse("question_not_waiting", "no such question is waiting for an answer")
		return
	}
	var answers []bus.QuestionAnswer
	if !cancel {
		var err error
		if answers, err = parseQuestionAnswers(msg.Payload[PayloadKeyAnswers]); err != nil {
			refuse("invalid_answer", err.Error())
			return
		}
	}
	if err := q.respond(answers, cancel); err != nil {
		refuse("invalid_answer", err.Error())
	}
}

// parseQuestionAnswers reads the answers of a question.answer: a list with
// one {selected: [labels], text} per question.
func parseQuestionAnswers(raw any) ([]bus.QuestionAnswer, error) {
	list, ok := raw.([]any)
	if !ok {
		return nil, errors.New("answers must be a list with one answer per question")
	}
	answers := make([]bus.QuestionAnswer, 0, len(list))
	for i, item := range list {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("answers[%d] must be an object", i)
		}
		var answer bus.QuestionAnswer
		if text, present := obj["text"]; present && text != nil {
			s, ok := text.(string)
			if !ok {
				return nil, fmt.Errorf("answers[%d].text must be a string", i)
			}
			answer.Text = s
		}
		if selected, present := obj["selected"]; present && selected != nil {
			labels, ok := selected.([]any)
			if !ok {
				return nil, fmt.Errorf("answers[%d].selected must be a list of labels", i)
			}
			for _, label := range labels {
				s, ok := label.(string)
				if !ok {
					return nil, fmt.Errorf("answers[%d].selected must be a list of labels", i)
				}
				answer.Selected = append(answer.Selected, s)
			}
		}
		answers = append(answers, answer)
	}
	return answers, nil
}
