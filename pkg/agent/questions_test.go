package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kayiik/compa/pkg/bus"
	"github.com/kayiik/compa/pkg/channels"
	"github.com/kayiik/compa/pkg/config"
	runtimeevents "github.com/kayiik/compa/pkg/events"
)

func colorQuestion(multiple bool) bus.Question {
	return bus.Question{
		Header:   "Color",
		Question: "Which color should the button be?",
		Multiple: multiple,
		Options: []bus.QuestionOption{
			{Label: "Red", Description: "Warm and loud"},
			{Label: "Blue", Description: "Calm", Recommended: true},
			{Label: "Green", Description: "Fresh"},
		},
	}
}

func sizeQuestion() bus.Question {
	return bus.Question{
		Header:   "Size",
		Question: "How big?",
		Options: []bus.QuestionOption{
			{Label: "Small", Description: "Fits in"},
			{Label: "Large", Description: "Stands out"},
		},
	}
}

func askQuestionsInBackground(
	al *AgentLoop,
	ctx context.Context,
	inbound *bus.InboundContext,
	questions ...bus.Question,
) <-chan questionResult {
	done := make(chan questionResult, 1)
	go func() {
		done <- al.askQuestions(ctx, HookMeta{}, inbound, questions)
	}()
	return done
}

func nextPost(t *testing.T, msgBus *bus.MessageBus) bus.OutboundMessage {
	t.Helper()
	select {
	case out := <-msgBus.OutboundChan():
		return out
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was posted")
	}
	return bus.OutboundMessage{}
}

func waitQuestionResult(t *testing.T, done <-chan questionResult) questionResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("the questions were not resolved")
	}
	return questionResult{}
}

func questionAnswer(al *AgentLoop, inbound *bus.InboundContext, content string) (string, bool) {
	msg := bus.NormalizeInboundMessage(bus.InboundMessage{Context: *inbound, Content: content})
	return al.questionReply(context.Background(), msg)
}

func TestPromptRegistry(t *testing.T) {
	post := func(string) error { return nil }
	t.Run("answer", func(t *testing.T) {
		r := newOperatorPrompts()
		ids := make(chan string, 1)
		done := make(chan promptOutcome, 1)
		go func() {
			_, outcome, _ := r.wait(context.Background(), &operatorPrompt{kind: promptQuestion, chat: "a"}, time.Minute,
				func(id string) error { ids <- id; return nil })
			done <- outcome
		}()
		id := <-ids
		if r.deliver(id, promptApproval, promptAnswer{approve: true}) {
			t.Fatal("an approval answer answered a question")
		}
		if !r.deliver(strings.ToUpper(id), promptQuestion, promptAnswer{}) {
			t.Fatal("deliver() = false for the waiting question")
		}
		if outcome := <-done; outcome != promptAnswered {
			t.Fatalf("outcome = %v, want answered", outcome)
		}
		if r.deliver(id, promptQuestion, promptAnswer{}) {
			t.Fatal("a question was answered twice")
		}
	})
	t.Run("timeout", func(t *testing.T) {
		r := newOperatorPrompts()
		_, outcome, err := r.wait(context.Background(), &operatorPrompt{kind: promptQuestion}, 20*time.Millisecond, post)
		if outcome != promptExpired || err != nil {
			t.Fatalf("wait() = %v, %v; want expired", outcome, err)
		}
		if _, _, ok := r.question(""); ok {
			t.Fatal("an expired question still waits")
		}
	})
	t.Run("ctx ends", func(t *testing.T) {
		r := newOperatorPrompts()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, outcome, err := r.wait(ctx, &operatorPrompt{kind: promptApproval}, time.Minute, post)
		if outcome != promptUnavailable || !errors.Is(err, context.Canceled) {
			t.Fatalf("wait() = %v, %v; want unavailable, canceled", outcome, err)
		}
	})
	t.Run("post fails", func(t *testing.T) {
		r := newOperatorPrompts()
		_, outcome, err := r.wait(context.Background(), &operatorPrompt{kind: promptApproval}, time.Minute,
			func(string) error { return errors.New("down") })
		if outcome != promptUnavailable || err == nil {
			t.Fatalf("wait() = %v, %v; want unavailable with the error", outcome, err)
		}
	})
	t.Run("one question per chat", func(t *testing.T) {
		r := newOperatorPrompts()
		ids := make(chan string, 1)
		go func() {
			_, _, _ = r.wait(context.Background(), &operatorPrompt{kind: promptQuestion, chat: "a"}, time.Minute,
				func(id string) error { ids <- id; return nil })
		}()
		id := <-ids
		if _, _, err := r.wait(context.Background(), &operatorPrompt{kind: promptQuestion, chat: "a"}, time.Minute, post); !errors.Is(err, errQuestionOpen) {
			t.Fatalf("a second question in the chat: err = %v, want errQuestionOpen", err)
		}
		// Another chat, or an approval in the same chat, still asks.
		if _, outcome, _ := r.wait(context.Background(), &operatorPrompt{kind: promptQuestion, chat: "b"}, 10*time.Millisecond, post); outcome != promptExpired {
			t.Fatalf("a question in another chat: outcome = %v, want expired", outcome)
		}
		if _, outcome, _ := r.wait(context.Background(), &operatorPrompt{kind: promptApproval, chat: "a"}, 10*time.Millisecond, post); outcome != promptExpired {
			t.Fatalf("an approval: outcome = %v, want expired", outcome)
		}
		r.deliver(id, promptQuestion, promptAnswer{})
	})
}

func TestAskUserValidation(t *testing.T) {
	option := func(label string) map[string]any { return map[string]any{"label": label, "description": "d"} }
	question := func(opts ...any) map[string]any {
		return map[string]any{"header": "H", "question": "Q?", "options": opts}
	}
	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{"no questions", map[string]any{}, "questions must be a list of 1 to 4"},
		{"too many", map[string]any{"questions": []any{
			question(option("a"), option("b")), question(option("a"), option("b")),
			question(option("a"), option("b")), question(option("a"), option("b")),
			question(option("a"), option("b")),
		}}, "1 to 4"},
		{"no header", map[string]any{"questions": []any{map[string]any{"question": "Q?", "options": []any{option("a"), option("b")}}}}, "questions[0].header is required"},
		{"one option", map[string]any{"questions": []any{question(option("a"))}}, "questions[0].options must be a list of 2 to 8"},
		{"nine options", map[string]any{"questions": []any{question(
			option("1"), option("2"), option("3"), option("4"), option("5"), option("6"), option("7"), option("8"), option("9"),
		)}}, "2 to 8"},
		{"no label", map[string]any{"questions": []any{question(map[string]any{"description": "d"}, option("b"))}}, "questions[0].options[0].label is required"},
		{"no description", map[string]any{"questions": []any{question(map[string]any{"label": "a"}, option("b"))}}, "options[0].description is required"},
		{"same label twice", map[string]any{"questions": []any{question(option("Yes"), option("yes"))}}, `the label "yes" twice`},
		{"two recommended", map[string]any{"questions": []any{question(
			map[string]any{"label": "a", "description": "d", "recommended": true},
			map[string]any{"label": "b", "description": "d", "recommended": true},
		)}}, "at most one option may be recommended"},
		{"multiple not a bool", map[string]any{"questions": []any{map[string]any{
			"header": "H", "question": "Q?", "multiple": "yes", "options": []any{option("a"), option("b")},
		}}}, "questions[0].multiple must be true or false"},
		{"two-line label", map[string]any{"questions": []any{question(option("a\nb"), option("c"))}}, "must be one line"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseAskUserQuestions(tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("parseAskUserQuestions() error = %v, want %q", err, tt.want)
			}
		})
	}

	got, err := parseAskUserQuestions(map[string]any{"questions": []any{map[string]any{
		"header": " Color ", "question": "Which color should the button be?", "multiple": false,
		"options": []any{
			map[string]any{"label": "Red", "description": "Warm and loud"},
			map[string]any{"label": "Blue", "description": "Calm", "recommended": true},
			map[string]any{"label": "Green", "description": "Fresh"},
		},
	}}})
	if err != nil {
		t.Fatalf("parseAskUserQuestions() error = %v", err)
	}
	if want := []bus.Question{colorQuestion(false)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("questions = %+v, want %+v", got, want)
	}
}

func TestAskUserOutsideATurn(t *testing.T) {
	al, _ := newApprovalTestLoop(t)
	tool := newAskUserTool(al)
	result := tool.Execute(context.Background(), map[string]any{"questions": []any{map[string]any{
		"header": "H", "question": "Q?", "options": []any{
			map[string]any{"label": "a", "description": "d"}, map[string]any{"label": "b", "description": "d"},
		},
	}}})
	if !result.IsError || !strings.Contains(result.ForLLM, "only be used during a conversation turn") {
		t.Fatalf("result = %+v", result)
	}
	if tool.CallTimeout(context.Background(), nil) != ownerQuestionTimeout {
		t.Fatalf("CallTimeout() = %s, want %s", tool.CallTimeout(context.Background(), nil), ownerQuestionTimeout)
	}
}

func TestAskUserIsNotAskedAbout(t *testing.T) {
	if askApplies(askUserToolName, nil) {
		t.Fatal("askApplies(ask_user) = true: asking to ask")
	}
}

func TestQuestionResultText(t *testing.T) {
	questions := []bus.Question{colorQuestion(true), sizeQuestion()}
	answered := questionResultText(questions, questionResult{
		outcome: bus.QuestionAnswered,
		answers: []bus.QuestionAnswer{{Selected: []string{"Red", "Blue"}}, {Selected: []string{"Small"}, Text: "tiny"}},
	}, time.Minute)
	if want := "The person answered:\n1. Color: chose Red, Blue\n2. Size: chose Small; wrote \"tiny\""; answered != want {
		t.Fatalf("answered = %q, want %q", answered, want)
	}
	for _, tt := range []struct {
		result questionResult
		want   string
	}{
		{questionResult{outcome: bus.QuestionCancelled, by: "owner"}, "The person cancelled the questions"},
		{questionResult{outcome: bus.QuestionCancelled, by: "hook:bot", reason: "not now"}, "A hook cancelled the questions without answering them. Reason: not now"},
		{questionResult{outcome: bus.QuestionExpired}, "No answer within 30 minutes."},
		{questionResult{outcome: bus.QuestionEnded}, "The turn ended before the person answered."},
		{questionResult{outcome: questionUnavailable, reason: questionNoChatReason}, questionNoChatReason},
	} {
		if got := questionResultText(questions, tt.result, ownerQuestionTimeout); !strings.Contains(got, tt.want) {
			t.Errorf("%s: text = %q, want %q", tt.result.outcome, got, tt.want)
		}
	}
}

func TestQuestionText(t *testing.T) {
	one := questionText([]bus.Question{colorQuestion(false)})
	want := "Compa asks:\n\nColor: Which color should the button be?\n" +
		"1. Red - Warm and loud\n2. Blue (recommended) - Calm\n3. Green - Fresh\n\n" +
		"Reply with the option number (e.g. 2), your own words, or /cancel."
	if one != want {
		t.Fatalf("one question:\n%s\nwant:\n%s", one, want)
	}
	several := questionText([]bus.Question{colorQuestion(true), sizeQuestion()})
	want = "Compa has 2 questions:\n\n" +
		"1) Color: Which color should the button be? (choose one or more)\n" +
		"   1. Red - Warm and loud\n   2. Blue (recommended) - Calm\n   3. Green - Fresh\n\n" +
		"2) Size: How big?\n   1. Small - Fits in\n   2. Large - Stands out\n\n" +
		"Reply with one line per question, or the answers separated by \";\" (e.g. 2; 1,3; my own words): " +
		"option numbers or your own words. Or /cancel."
	if several != want {
		t.Fatalf("several questions:\n%s\nwant:\n%s", several, want)
	}
}

func TestParseQuestionReply(t *testing.T) {
	single := []bus.Question{colorQuestion(false)}
	multi := []bus.Question{colorQuestion(true)}
	both := []bus.Question{colorQuestion(true), sizeQuestion()}
	tests := []struct {
		name      string
		questions []bus.Question
		reply     string
		want      []bus.QuestionAnswer
		err       string
	}{
		{"a number", single, "2", []bus.QuestionAnswer{{Selected: []string{"Blue"}}}, ""},
		{"a number with a dot", single, " 3. ", []bus.QuestionAnswer{{Selected: []string{"Green"}}}, ""},
		{"own words", single, "purple, please", []bus.QuestionAnswer{{Text: "purple, please"}}, ""},
		{"several numbers", multi, "1,3", []bus.QuestionAnswer{{Selected: []string{"Red", "Green"}}}, ""},
		{"several numbers with spaces", multi, "3 1, 3", []bus.QuestionAnswer{{Selected: []string{"Green", "Red"}}}, ""},
		{"several for a single choice", single, "1,2", nil, "choose only one option"},
		{"out of range", single, "4", nil, "choose an option from 1 to 3"},
		{"lines", both, "1 2\nlarge-ish", []bus.QuestionAnswer{{Selected: []string{"Red", "Blue"}}, {Text: "large-ish"}}, ""},
		{"semicolons", both, "3; 2", []bus.QuestionAnswer{{Selected: []string{"Green"}}, {Selected: []string{"Large"}}}, ""},
		{"too few", both, "3", nil, "please answer all 2 questions"},
		{"out of range in the second", both, "1; 3", nil, "choose an option from 1 to 2 for question 2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseQuestionReply(tt.questions, tt.reply)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("error = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseQuestionReply() = %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
}

func TestOwnerAnswersAQuestionInATextChat(t *testing.T) {
	al, msgBus := newApprovalTestLoop(t)
	owner := ownerTelegram("chat-1")

	done := askQuestionsInBackground(al, context.Background(), owner, colorQuestion(false), sizeQuestion())
	out := nextPost(t, msgBus)
	if out.Context.Channel != "telegram" || out.Context.ChatID != "chat-1" || !strings.HasPrefix(out.Content, "Compa has 2 questions") {
		t.Fatalf("posted %q in %s/%s", out.Content, out.Context.Channel, out.Context.ChatID)
	}

	stranger := &bus.InboundContext{Channel: "telegram", ChatID: "chat-1", SenderID: "telegram:2"}
	if _, handled := questionAnswer(al, stranger, "2; 1"); handled {
		t.Fatal("a stranger's message answered the owner's question")
	}
	if reply, _ := questionAnswer(al, stranger, "/cancel"); reply != questionNotOwnerReply {
		t.Fatalf("a stranger's /cancel = %q, want %q", reply, questionNotOwnerReply)
	}
	if reply, handled := questionAnswer(al, owner, "2"); !handled || !strings.Contains(reply, "answer all 2 questions") {
		t.Fatalf("an incomplete answer = %q, %v; want the question to stay with a hint", reply, handled)
	}
	if _, handled := questionAnswer(al, owner, "/help"); handled {
		t.Fatal("another command was taken for an answer")
	}
	if reply, _ := questionAnswer(al, owner, "2\nhuge"); reply != questionAnsweredReply {
		t.Fatalf("reply = %q, want %q", reply, questionAnsweredReply)
	}
	result := waitQuestionResult(t, done)
	want := []bus.QuestionAnswer{{Selected: []string{"Blue"}}, {Text: "huge"}}
	if result.outcome != bus.QuestionAnswered || result.by != "owner" || !reflect.DeepEqual(result.answers, want) {
		t.Fatalf("result = %+v", result)
	}
	if _, handled := questionAnswer(al, owner, "1"); handled {
		t.Fatal("a message after the answer was taken for one")
	}
}

func TestOwnerCancelsAQuestion(t *testing.T) {
	al, msgBus := newApprovalTestLoop(t)
	owner := ownerTelegram("chat-1")
	if reply, _ := questionAnswer(al, owner, "/cancel"); reply != questionNotWaitingReply {
		t.Fatalf("/cancel with nothing waiting = %q", reply)
	}
	done := askQuestionsInBackground(al, context.Background(), owner, colorQuestion(false))
	nextPost(t, msgBus)
	if reply, _ := questionAnswer(al, owner, "/cancel"); reply != questionCancelledReply {
		t.Fatalf("/cancel = %q", reply)
	}
	if result := waitQuestionResult(t, done); result.outcome != bus.QuestionCancelled {
		t.Fatalf("result = %+v, want cancelled", result)
	}
}

func TestQuestionExpiresAndEnds(t *testing.T) {
	al, msgBus := newApprovalTestLoop(t)
	al.questions.timeout = 30 * time.Millisecond
	done := askQuestionsInBackground(al, context.Background(), ownerTelegram("chat-1"), colorQuestion(false))
	nextPost(t, msgBus)
	if result := waitQuestionResult(t, done); result.outcome != bus.QuestionExpired {
		t.Fatalf("result = %+v, want expired", result)
	}

	al.questions.timeout = time.Minute
	ctx, cancel := context.WithCancel(context.Background())
	done = askQuestionsInBackground(al, ctx, ownerTelegram("chat-1"), colorQuestion(false))
	nextPost(t, msgBus)
	cancel()
	if result := waitQuestionResult(t, done); result.outcome != bus.QuestionEnded {
		t.Fatalf("result = %+v, want ended", result)
	}
}

func TestSecondQuestionInAChatIsRefused(t *testing.T) {
	al, msgBus := newApprovalTestLoop(t)
	owner := ownerTelegram("chat-1")
	done := askQuestionsInBackground(al, context.Background(), owner, colorQuestion(false))
	nextPost(t, msgBus)
	second := waitQuestionResult(t, askQuestionsInBackground(al, context.Background(), owner, sizeQuestion()))
	if second.outcome != questionUnavailable || second.reason != questionOpenReason {
		t.Fatalf("second = %+v, want refused", second)
	}
	questionAnswer(al, owner, "1")
	if result := waitQuestionResult(t, done); result.outcome != bus.QuestionAnswered {
		t.Fatalf("first = %+v, want answered", result)
	}
}

func TestScheduledQuestionWithoutAnOwnerChat(t *testing.T) {
	al, msgBus := newApprovalTestLoop(t)
	result := waitQuestionResult(t, askQuestionsInBackground(al, withScheduledTurn(context.Background()),
		&bus.InboundContext{Channel: "telegram", ChatID: "job-chat"}, colorQuestion(false)))
	if result.outcome != questionUnavailable || result.reason != questionNoChatReason {
		t.Fatalf("result = %+v, want no chat", result)
	}
	noPost(t, msgBus)
	// A scheduled turn takes no answers either.
	if _, handled := al.questionReply(withScheduledTurn(context.Background()),
		bus.InboundMessage{Channel: "telegram", ChatID: "chat-1", Content: "/cancel"}); handled {
		t.Fatal("a scheduled turn's message was taken for an answer")
	}
}

// The owner's answer reaches the waiting question from the receive loop,
// before the session's queue: it is neither queued behind the turn that asked
// nor taken for a new message.
func TestQuestionAnswerReachesTheReceiveLoop(t *testing.T) {
	al, msgBus := newApprovalTestLoop(t)
	owner := ownerTelegram("chat-1")
	done := askQuestionsInBackground(al, context.Background(), owner, colorQuestion(false))
	nextPost(t, msgBus)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = al.Run(ctx) }()
	if err := msgBus.PublishInbound(ctx, bus.InboundMessage{Context: *owner, Content: "3"}); err != nil {
		t.Fatalf("PublishInbound: %v", err)
	}
	result := waitQuestionResult(t, done)
	if result.outcome != bus.QuestionAnswered || !reflect.DeepEqual(result.answers, []bus.QuestionAnswer{{Selected: []string{"Green"}}}) {
		t.Fatalf("result = %+v", result)
	}
	if reply := nextPost(t, msgBus); reply.Content != questionAnsweredReply {
		t.Fatalf("reply = %q, want %q", reply.Content, questionAnsweredReply)
	}
}

// fakeQuestionHook is a hook that answers, cancels or passes.
type fakeQuestionHook struct {
	decision QuestionDecision
	asked    chan *QuestionRequest
}

func (h *fakeQuestionHook) AnswerQuestion(_ context.Context, req *QuestionRequest) (QuestionDecision, error) {
	if h.asked != nil {
		h.asked <- req
	}
	return h.decision, nil
}

type failingQuestionAnswerer struct{}

func (failingQuestionAnswerer) AnswerQuestion(context.Context, *QuestionRequest) (QuestionDecision, error) {
	return QuestionDecision{}, errors.New("broken")
}

func TestQuestionHooks(t *testing.T) {
	t.Run("answers", func(t *testing.T) {
		al, msgBus := newApprovalTestLoop(t)
		hook := &fakeQuestionHook{
			decision: QuestionDecision{Action: QuestionActionAnswer, Answers: []bus.QuestionAnswer{{Selected: []string{"blue"}}}},
			asked:    make(chan *QuestionRequest, 1),
		}
		if err := al.MountHook(NamedHook("answerer", hook)); err != nil {
			t.Fatal(err)
		}
		result := waitQuestionResult(t, askQuestionsInBackground(al, context.Background(), ownerTelegram("c"), colorQuestion(false)))
		if result.outcome != bus.QuestionAnswered || result.by != "hook:answerer" ||
			!reflect.DeepEqual(result.answers, []bus.QuestionAnswer{{Selected: []string{"Blue"}}}) {
			t.Fatalf("result = %+v", result)
		}
		if req := <-hook.asked; req.QuestionID == "" || len(req.Questions) != 1 || req.Context == nil {
			t.Fatalf("request = %+v", req)
		}
		noPost(t, msgBus)
	})
	t.Run("cancels", func(t *testing.T) {
		al, _ := newApprovalTestLoop(t)
		if err := al.MountHook(NamedHook("canceller", &fakeQuestionHook{
			decision: QuestionDecision{Action: QuestionActionCancel, Reason: "busy"},
		})); err != nil {
			t.Fatal(err)
		}
		result := waitQuestionResult(t, askQuestionsInBackground(al, context.Background(), ownerTelegram("c"), colorQuestion(false)))
		if result.outcome != bus.QuestionCancelled || result.reason != "busy" {
			t.Fatalf("result = %+v", result)
		}
	})
	t.Run("pass, fail or misfit leaves it to the owner", func(t *testing.T) {
		al, msgBus := newApprovalTestLoop(t)
		for name, hook := range map[string]any{
			"a-passer": &fakeQuestionHook{},
			"b-broken": failingQuestionAnswerer{},
			"c-misfit": &fakeQuestionHook{decision: QuestionDecision{
				Action: QuestionActionAnswer, Answers: []bus.QuestionAnswer{{Selected: []string{"Purple"}}},
			}},
		} {
			if err := al.MountHook(NamedHook(name, hook)); err != nil {
				t.Fatal(err)
			}
		}
		owner := ownerTelegram("c")
		done := askQuestionsInBackground(al, context.Background(), owner, colorQuestion(false))
		nextPost(t, msgBus)
		questionAnswer(al, owner, "1")
		if result := waitQuestionResult(t, done); result.outcome != bus.QuestionAnswered || result.by != "owner" {
			t.Fatalf("result = %+v, want the owner's answer", result)
		}
	})
}

func TestProcessHookAnswerQuestionIntercept(t *testing.T) {
	opts, err := processHookOptionsFromConfig(config.ProcessHookConfig{
		Command: []string{"hook"}, Intercept: []string{"answer_question"},
	})
	if err != nil || !opts.AnswerQuestion || opts.ApproveTool {
		t.Fatalf("options = %+v, %v; want a question answerer only", opts, err)
	}
	if _, ok := questionAnswerer(HookRegistration{Hook: &ProcessHook{}}); ok {
		t.Fatal("a process hook without the intercept answers questions")
	}
	if _, ok := questionAnswerer(HookRegistration{Hook: &ProcessHook{opts: opts}}); !ok {
		t.Fatal("a process hook with the intercept doesn't answer questions")
	}
}

func TestQuestionEvents(t *testing.T) {
	al, _ := newApprovalTestLoop(t)
	sub, events, err := al.RuntimeEvents().SubscribeChan(context.Background(), runtimeevents.SubscribeOptions{Name: "test", Buffer: 8})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sub.Close() }()
	if err := al.MountHook(NamedHook("canceller", &fakeQuestionHook{decision: QuestionDecision{Action: QuestionActionCancel}})); err != nil {
		t.Fatal(err)
	}
	waitQuestionResult(t, askQuestionsInBackground(al, context.Background(), ownerTelegram("c"), colorQuestion(false)))
	var kinds []runtimeevents.Kind
	timeout := time.After(5 * time.Second)
	for len(kinds) < 2 {
		select {
		case evt := <-events:
			if evt.Kind == runtimeevents.KindQuestionRequested || evt.Kind == runtimeevents.KindQuestionResolved {
				kinds = append(kinds, evt.Kind)
				if p, ok := evt.Payload.(QuestionResolvedPayload); ok && (p.Outcome != bus.QuestionCancelled || p.By != "hook:canceller") {
					t.Fatalf("resolved payload = %+v", p)
				}
			}
		case <-timeout:
			t.Fatalf("events = %v, want question.requested and question.resolved", kinds)
		}
	}
	if kinds[0] != runtimeevents.KindQuestionRequested || kinds[1] != runtimeevents.KindQuestionResolved {
		t.Fatalf("events = %v", kinds)
	}
}

// presenterChannels runs a web channel that shows questions.
type presenterChannels struct {
	approvalChannels
	presenter *fakePresenter
}

func (c presenterChannels) GetChannel(name string) (channels.Channel, bool) {
	if name == config.ChannelWeb {
		return c.presenter, true
	}
	return c.approvalChannels.GetChannel(name)
}

type fakePresenter struct {
	approvalChannel
	shown    chan bus.QuestionPrompt
	respond  chan channels.QuestionResponder
	resolved chan string
}

func (p *fakePresenter) ShowQuestion(_ context.Context, chatID string, prompt bus.QuestionPrompt, respond channels.QuestionResponder) error {
	p.shown <- prompt
	p.respond <- respond
	return nil
}

func (p *fakePresenter) QuestionResolved(_ context.Context, chatID, id, outcome string) {
	p.resolved <- chatID + " " + id + " " + outcome
}

func TestQuestionsGoToAPresenter(t *testing.T) {
	al, msgBus := newApprovalTestLoop(t)
	presenter := &fakePresenter{
		approvalChannel: approvalChannel{name: config.ChannelWeb},
		shown:           make(chan bus.QuestionPrompt, 1),
		respond:         make(chan channels.QuestionResponder, 1),
		resolved:        make(chan string, 1),
	}
	al.channelManager = presenterChannels{approvalChannels: al.channelManager.(approvalChannels), presenter: presenter}
	web := &bus.InboundContext{Channel: config.ChannelWeb, ChatID: "web:s1", SenderID: "web-user"}

	done := askQuestionsInBackground(al, context.Background(), web, colorQuestion(true))
	prompt := <-presenter.shown
	respond := <-presenter.respond
	if len(prompt.Questions) != 1 || prompt.ID == "" || time.Until(prompt.ExpiresAt) < 29*time.Minute {
		t.Fatalf("prompt = %+v", prompt)
	}
	noPost(t, msgBus) // no numbered text besides the frame
	if err := respond([]bus.QuestionAnswer{{Selected: []string{"Purple"}}}, false); err == nil {
		t.Fatal("an answer naming no option was taken")
	}
	if err := respond([]bus.QuestionAnswer{{Selected: []string{"Red", "green"}}}, false); err != nil {
		t.Fatalf("respond() = %v", err)
	}
	result := waitQuestionResult(t, done)
	if result.outcome != bus.QuestionAnswered || !reflect.DeepEqual(result.answers, []bus.QuestionAnswer{{Selected: []string{"Red", "Green"}}}) {
		t.Fatalf("result = %+v", result)
	}
	if got := <-presenter.resolved; got != "web:s1 "+prompt.ID+" answered" {
		t.Fatalf("resolved = %q", got)
	}
	if err := respond(nil, true); !errors.Is(err, errQuestionGone) {
		t.Fatalf("respond() after the answer = %v, want errQuestionGone", err)
	}
}
