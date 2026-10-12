// Compa - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kayiik/compa/pkg/bus"
	"github.com/kayiik/compa/pkg/channels"
	"github.com/kayiik/compa/pkg/commands"
	"github.com/kayiik/compa/pkg/cron"
	runtimeevents "github.com/kayiik/compa/pkg/events"
	"github.com/kayiik/compa/pkg/logger"
)

// Questions to the owner (the ask_user tool): the registered question hooks
// answer first; with none deciding, the owner is asked in the chat an
// approval would be asked in (approvalTarget) and the turn waits, like for an
// approval, up to ownerQuestionTimeout. At most one question waits per chat,
// so the owner's next message there answers it; a second ask in that chat is
// refused while the first waits. A channel that shows choices
// (channels.QuestionPresenter) gets the questions as they are; any other
// gets them as a numbered text message.

const (
	// ownerQuestionTimeout is how long questions wait for the owner. Longer
	// than an approval: a question may take thought.
	ownerQuestionTimeout = 30 * time.Minute

	// questionAnswerMaxRunes caps an answer in the owner's own words, from
	// any channel or hook; a longer one is refused, not cut.
	questionAnswerMaxRunes = 2000

	// questionUnavailable is the outcome when nobody could be asked.
	questionUnavailable = "unavailable"

	questionNoChatReason = "There is no chat to ask the person in: this turn runs on a schedule, " +
		"or for someone else, and the owner hasn't written to Compa from a chat that shows questions. " +
		"Carry on with your best judgement and say what you assumed."
	questionOpenReason = "Another question is already waiting for the person's answer in this chat. " +
		"Wait for that answer, or carry on without asking."
	questionNotPostedReason = "The questions could not be shown to the person. Carry on with your best judgement " +
		"and say what you assumed."

	questionAnsweredReply   = "Got it, thanks."
	questionCancelledReply  = "Cancelled."
	questionNotWaitingReply = "No question is waiting for an answer in this chat."
	questionNotOwnerReply   = "Only the owner can answer or cancel questions."
)

// errQuestionGone answers an answer to a question that no longer waits.
var errQuestionGone = channels.ErrQuestionNotWaiting

// ownerQuestions holds the questions waiting for the owner's answers, in the
// owner's prompts (operatorPrompts).
type ownerQuestions struct {
	prompts *operatorPrompts
	timeout time.Duration
}

func newOwnerQuestions(prompts *operatorPrompts) *ownerQuestions {
	return &ownerQuestions{prompts: prompts, timeout: ownerQuestionTimeout}
}

// respond delivers the owner's answers to question id, or cancels it.
func (q *ownerQuestions) respond(id string, answers []bus.QuestionAnswer, cancel bool) error {
	questions, ok := q.prompts.questionByID(id)
	if !ok {
		return errQuestionGone
	}
	answer := promptAnswer{cancelled: cancel, by: "owner"}
	if !cancel {
		normalized, err := normalizeQuestionAnswers(questions, answers)
		if err != nil {
			return err
		}
		answer.answers = normalized
	}
	if !q.prompts.deliver(id, promptQuestion, answer) {
		return errQuestionGone
	}
	return nil
}

// questionResult is how a set of questions ended.
type questionResult struct {
	// outcome is a bus.Question* outcome or questionUnavailable.
	outcome string
	answers []bus.QuestionAnswer
	// by names who answered or cancelled: owner, or hook:<name>.
	by string
	// reason tells the model why nobody answered, or why a hook cancelled.
	reason string
}

// askQuestions asks questions for a turn answering inbound: the hooks first,
// then the owner. The ask is published as a question.requested event and its
// end as question.resolved.
func (al *AgentLoop) askQuestions(
	ctx context.Context,
	meta HookMeta,
	inbound *bus.InboundContext,
	questions []bus.Question,
) questionResult {
	if meta.turnContext == nil && inbound != nil {
		meta.turnContext = &TurnContext{Inbound: inbound}
	}
	id := fmt.Sprintf("question-%d", al.approvalSeq.Add(1))
	al.emitEvent(runtimeevents.KindQuestionRequested, meta, QuestionRequestedPayload{
		QuestionID: id,
		Questions:  cloneQuestions(questions),
	})

	result := al.resolveQuestions(ctx, meta, inbound, id, questions)

	al.emitEvent(runtimeevents.KindQuestionResolved, meta, QuestionResolvedPayload{
		QuestionID: id,
		Outcome:    result.outcome,
		By:         result.by,
	})
	return result
}

func (al *AgentLoop) resolveQuestions(
	ctx context.Context,
	meta HookMeta,
	inbound *bus.InboundContext,
	id string,
	questions []bus.Question,
) questionResult {
	decision, name, decided := al.hooks.AnswerQuestion(ctx, &QuestionRequest{
		Meta:       meta,
		Context:    cloneTurnContext(meta.turnContext),
		QuestionID: id,
		Questions:  questions,
	})
	if decided {
		by := "hook:" + name
		if decision.Action == QuestionActionCancel {
			return questionResult{outcome: bus.QuestionCancelled, by: by, reason: decision.Reason}
		}
		answers, _ := normalizeQuestionAnswers(questions, decision.Answers) // checked by AnswerQuestion
		return questionResult{outcome: bus.QuestionAnswered, answers: answers, by: by}
	}
	// The owner may take ownerQuestionTimeout to answer, so the owner is
	// asked outside the hooks' timeout.
	return al.askOwnerQuestions(ctx, inbound, questions)
}

// askOwnerQuestions asks the owner questions where an approval would be
// asked, and waits for the answers. A scheduled job's run doesn't count the
// wait against its timeout.
func (al *AgentLoop) askOwnerQuestions(
	ctx context.Context,
	inbound *bus.InboundContext,
	questions []bus.Question,
) questionResult {
	if al.questions == nil {
		return questionResult{outcome: questionUnavailable, reason: questionNoChatReason}
	}
	target, ok := al.approvalTarget(ctx, inbound)
	if !ok {
		return questionResult{outcome: questionUnavailable, reason: questionNoChatReason}
	}
	resume := cron.PauseJobTimeout(ctx)
	defer resume()

	presenter := al.questionPresenter(target.channel)
	timeout := al.questions.timeout
	var postedID string
	answer, outcome, err := al.questions.prompts.wait(ctx, &operatorPrompt{
		kind:      promptQuestion,
		chat:      chatKey(target.channel, target.chatID, target.topicID()),
		questions: questions,
	}, timeout, func(id string) error {
		if presenter == nil {
			return al.postApprovalRequest(ctx, target, questionText(questions))
		}
		prompt := bus.QuestionPrompt{ID: id, Questions: cloneQuestions(questions), ExpiresAt: time.Now().Add(timeout)}
		err := presenter.ShowQuestion(ctx, target.chatID, prompt, func(answers []bus.QuestionAnswer, cancel bool) error {
			return al.questions.respond(id, answers, cancel)
		})
		if err == nil {
			postedID = id
		}
		return err
	})

	var result questionResult
	switch {
	case outcome == promptAnswered && answer.cancelled:
		result = questionResult{outcome: bus.QuestionCancelled, by: answer.by}
	case outcome == promptAnswered:
		result = questionResult{outcome: bus.QuestionAnswered, answers: answer.answers, by: answer.by}
	case outcome == promptExpired:
		result = questionResult{outcome: bus.QuestionExpired}
	case errors.Is(err, errQuestionOpen):
		result = questionResult{outcome: questionUnavailable, reason: questionOpenReason}
	case ctx.Err() != nil:
		result = questionResult{outcome: bus.QuestionEnded}
	default:
		logger.WarnCF("agent", "Questions could not be posted", map[string]any{"error": fmt.Sprint(err)})
		result = questionResult{outcome: questionUnavailable, reason: questionNotPostedReason}
	}
	if postedID != "" {
		// The turn's context may have ended: the chat is told anyway.
		resolveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		presenter.QuestionResolved(resolveCtx, target.chatID, postedID, result.outcome)
		cancel()
	}
	return result
}

// topicID is the topic or thread of the chat a question is asked in: the
// turn's, when it is asked in the turn's chat.
func (c approvalChat) topicID() string {
	if c.inbound == nil {
		return ""
	}
	return c.inbound.TopicID
}

// questionPresenter returns channel when it shows questions with choices.
func (al *AgentLoop) questionPresenter(channel string) channels.QuestionPresenter {
	cm := al.currentChannelManager()
	if cm == nil {
		return nil
	}
	ch, ok := cm.GetChannel(channel)
	if !ok {
		return nil
	}
	presenter, _ := ch.(channels.QuestionPresenter)
	return presenter
}

// QuestionWaiting reports whether a question waits for the owner's answer in
// channel's chatID: the owner's next message there answers it.
func (al *AgentLoop) QuestionWaiting(channel, chatID string) bool {
	if al.questions == nil {
		return false
	}
	_, _, ok := al.questions.prompts.question(chatKey(channel, chatID, ""))
	return ok
}

// questionText is the questions as a text message: each question numbered,
// with its options numbered, and how to answer.
func questionText(questions []bus.Question) string {
	var b strings.Builder
	single := len(questions) == 1
	if single {
		b.WriteString("Compa asks:\n\n")
	} else {
		fmt.Fprintf(&b, "Compa has %d questions:\n\n", len(questions))
	}
	for i, q := range questions {
		if i > 0 {
			b.WriteString("\n")
		}
		prefix, indent := "", ""
		if !single {
			prefix, indent = fmt.Sprintf("%d) ", i+1), "   "
		}
		fmt.Fprintf(&b, "%s%s: %s", prefix, q.Header, q.Question)
		if q.Multiple {
			b.WriteString(" (choose one or more)")
		}
		b.WriteString("\n")
		for j, opt := range q.Options {
			fmt.Fprintf(&b, "%s%d. %s", indent, j+1, opt.Label)
			if opt.Recommended {
				b.WriteString(" (recommended)")
			}
			if opt.Description != "" {
				fmt.Fprintf(&b, " - %s", opt.Description)
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	if single {
		if questions[0].Multiple {
			b.WriteString("Reply with the option numbers (e.g. 2, or 1,3 for several), your own words, or /cancel.")
		} else {
			b.WriteString("Reply with the option number (e.g. 2), your own words, or /cancel.")
		}
	} else {
		b.WriteString("Reply with one line per question, or the answers separated by \";\" (e.g. 2; 1,3; my own words): " +
			"option numbers or your own words. Or /cancel.")
	}
	return b.String()
}

// parseQuestionReply reads the owner's text reply to questions: for each
// question, option numbers or the owner's own words. Several questions are
// answered one line per question, or with the answers separated by ";".
func parseQuestionReply(questions []bus.Question, content string) ([]bus.QuestionAnswer, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, errors.New("the reply is empty")
	}
	parts := []string{content}
	if n := len(questions); n > 1 {
		parts = nonEmptyParts(strings.Split(content, "\n"))
		if len(parts) != n {
			parts = nonEmptyParts(strings.Split(content, ";"))
		}
		if len(parts) != n {
			return nil, fmt.Errorf("please answer all %d questions: one line per question, "+
				"or the answers separated by \";\"", n)
		}
	}
	answers := make([]bus.QuestionAnswer, len(questions))
	for i, part := range parts {
		q := questions[i]
		numbers, ok := optionNumbers(part, len(q.Options))
		if !ok {
			// Own words are always an answer, numbers too ("2024", "10")
			// when they aren't options.
			answers[i] = bus.QuestionAnswer{Text: part}
			continue
		}
		which := ""
		if len(questions) > 1 {
			which = fmt.Sprintf(" for question %d", i+1)
		}
		for _, n := range numbers {
			label := q.Options[n-1].Label
			if !containsString(answers[i].Selected, label) {
				answers[i].Selected = append(answers[i].Selected, label)
			}
		}
		if !q.Multiple && len(answers[i].Selected) > 1 {
			return nil, fmt.Errorf("choose only one option%s", which)
		}
	}
	return answers, nil
}

func nonEmptyParts(parts []string) []string {
	kept := parts[:0:0]
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			kept = append(kept, part)
		}
	}
	return kept
}

// optionNumbers reads a reply part made only of option numbers from 1 to
// options, separated by commas or spaces ("2", "1,3", "1, 3", "2."); ok is
// false for anything else, a sign or a number that is no option included.
func optionNumbers(part string, options int) ([]int, bool) {
	fields := strings.FieldsFunc(part, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
	if len(fields) == 0 {
		return nil, false
	}
	numbers := make([]int, 0, len(fields))
	for _, field := range fields {
		digits := strings.TrimRight(field, ".)")
		if digits == "" || len(digits) > 3 || strings.TrimLeft(digits, "0123456789") != "" {
			return nil, false
		}
		n, err := strconv.Atoi(digits)
		if err != nil || n < 1 || n > options {
			return nil, false
		}
		numbers = append(numbers, n)
	}
	return numbers, true
}

func containsString(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

// normalizeQuestionAnswers checks answers against questions -- one per
// question, each choosing among its options (one, unless it takes several)
// or saying something -- and returns them with each chosen label as the
// option has it.
func normalizeQuestionAnswers(questions []bus.Question, answers []bus.QuestionAnswer) ([]bus.QuestionAnswer, error) {
	if len(answers) != len(questions) {
		return nil, fmt.Errorf("give one answer per question: %d questions, %d answers", len(questions), len(answers))
	}
	normalized := make([]bus.QuestionAnswer, len(answers))
	for i, answer := range answers {
		q := questions[i]
		out := bus.QuestionAnswer{Text: strings.TrimSpace(answer.Text)}
		if n := utf8.RuneCountInString(out.Text); n > questionAnswerMaxRunes {
			return nil, fmt.Errorf("the answer to question %d is too long (%d characters, at most %d)",
				i+1, n, questionAnswerMaxRunes)
		}
		for _, selected := range answer.Selected {
			label, ok := optionLabel(q, selected)
			if !ok {
				return nil, fmt.Errorf("question %d has no option %q", i+1, selected)
			}
			if !containsString(out.Selected, label) {
				out.Selected = append(out.Selected, label)
			}
		}
		if !q.Multiple && len(out.Selected) > 1 {
			return nil, fmt.Errorf("question %d takes only one option", i+1)
		}
		if len(out.Selected) == 0 && out.Text == "" {
			return nil, fmt.Errorf("question %d has no answer", i+1)
		}
		normalized[i] = out
	}
	return normalized, nil
}

// validateQuestionAnswers reports whether answers fit questions.
func validateQuestionAnswers(questions []bus.Question, answers []bus.QuestionAnswer) error {
	_, err := normalizeQuestionAnswers(questions, answers)
	return err
}

// optionLabel finds the option of q a label names, ignoring case.
func optionLabel(q bus.Question, label string) (string, bool) {
	label = strings.TrimSpace(label)
	for _, opt := range q.Options {
		if strings.EqualFold(opt.Label, label) {
			return opt.Label, true
		}
	}
	return "", false
}

// questionReply answers or cancels the question waiting in msg's chat with
// msg and returns the reply for the chat; handled is false when msg is not
// for a question. /cancel cancels; any other registered command passes, and
// other text starting with "/" (a path, say) is an answer. Only the owner's
// answer counts.
func (al *AgentLoop) questionReply(ctx context.Context, msg bus.InboundMessage) (reply string, handled bool) {
	if al.questions == nil || isScheduledTurn(ctx) {
		return "", false
	}
	id, questions, waiting := al.questions.prompts.question(chatKey(msg.Channel, msg.ChatID, msg.Context.TopicID))
	name, isCommand := commands.CommandName(msg.Content)
	if isCommand && name != "cancel" && !al.isRegisteredCommand(name) {
		isCommand = false
	}
	owner := senderIsOwner(msg)
	if isCommand {
		if name != "cancel" {
			return "", false
		}
		switch {
		case !owner && !waiting:
			return "", false
		case !owner:
			logger.InfoCF("agent", "Question cancel refused: sender is not the owner",
				map[string]any{"channel": msg.Channel})
			return questionNotOwnerReply, true
		case !waiting:
			return questionNotWaitingReply, true
		}
		if err := al.questions.respond(id, nil, true); err != nil {
			return questionNotWaitingReply, true
		}
		return questionCancelledReply, true
	}
	if !waiting || !owner || strings.TrimSpace(msg.Content) == "" {
		return "", false
	}
	al.recordOwnerChat(ctx, &msg.Context)
	answers, err := parseQuestionReply(questions, msg.Content)
	if err == nil {
		err = al.questions.respond(id, answers, false)
	}
	if errors.Is(err, errQuestionGone) {
		// Answered or expired meanwhile: the message is an ordinary one.
		return "", false
	}
	if err != nil {
		return capitalize(err.Error()) + ". Or reply /cancel.", true
	}
	return questionAnsweredReply, true
}

// isRegisteredCommand reports whether name is one of the agent's commands
// (or an alias of one).
func (al *AgentLoop) isRegisteredCommand(name string) bool {
	if al.cmdRegistry == nil {
		return true
	}
	_, ok := al.cmdRegistry.Lookup(name)
	return ok
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// handleQuestionReply acts on the owner's answer to a waiting question as
// soon as it arrives, reporting whether msg was one: the turn waiting for it
// holds msg's session, and may hold every worker.
func (al *AgentLoop) handleQuestionReply(ctx context.Context, msg bus.InboundMessage, sessionKey string) bool {
	msg = bus.NormalizeInboundMessage(msg)
	reply, handled := al.questionReply(ctx, msg)
	if !handled {
		return false
	}
	go al.publishDirectReply(ctx, msg, sessionKey, reply)
	return true
}

// questionResultText is what the model is told of a set of questions.
func questionResultText(questions []bus.Question, result questionResult, timeout time.Duration) string {
	switch result.outcome {
	case bus.QuestionAnswered:
		var b strings.Builder
		b.WriteString("The person answered:\n")
		for i, q := range questions {
			var answer bus.QuestionAnswer
			if i < len(result.answers) {
				answer = result.answers[i]
			}
			fmt.Fprintf(&b, "%d. %s: ", i+1, q.Header)
			var parts []string
			if len(answer.Selected) > 0 {
				parts = append(parts, "chose "+strings.Join(answer.Selected, ", "))
			}
			if answer.Text != "" {
				parts = append(parts, fmt.Sprintf("wrote %q", answer.Text))
			}
			b.WriteString(strings.Join(parts, "; "))
			b.WriteString("\n")
		}
		return strings.TrimSuffix(b.String(), "\n")
	case bus.QuestionCancelled:
		if strings.HasPrefix(result.by, "hook:") {
			text := "A hook cancelled the questions without answering them."
			if result.reason != "" {
				text += " Reason: " + result.reason
			}
			return text
		}
		return "The person cancelled the questions without answering. Don't ask them again; " +
			"carry on as best you can, or ask what they would like instead."
	case bus.QuestionExpired:
		return fmt.Sprintf("No answer within %d minutes. Carry on with your best judgement and say what you assumed.",
			int(timeout.Minutes()))
	case bus.QuestionEnded:
		return "The turn ended before the person answered."
	default:
		return result.reason
	}
}
