package web

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/kayiik/compa/pkg/bus"
	"github.com/kayiik/compa/pkg/channels"
)

type recordedResponse struct {
	answers []bus.QuestionAnswer
	cancel  bool
}

func testPrompt() bus.QuestionPrompt {
	return bus.QuestionPrompt{
		ID: "q7k2mp",
		Questions: []bus.Question{{
			Header: "Color", Question: "Which?",
			Options: []bus.QuestionOption{{Label: "Red", Description: "warm"}, {Label: "Blue", Description: "cool", Recommended: true}},
		}},
		ExpiresAt: time.UnixMilli(1_700_000_000_000),
	}
}

func showTestQuestion(t *testing.T, ch *WebChannel, err error) <-chan recordedResponse {
	t.Helper()
	responses := make(chan recordedResponse, 4)
	if showErr := ch.ShowQuestion(context.Background(), "web:sess-1", testPrompt(),
		func(answers []bus.QuestionAnswer, cancel bool) error {
			responses <- recordedResponse{answers, cancel}
			return err
		}); showErr != nil {
		t.Fatalf("ShowQuestion() = %v", showErr)
	}
	return responses
}

func TestQuestionFrames(t *testing.T) {
	ch := newTestWebChannel(t)
	if err := ch.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer ch.Stop(context.Background())
	clientConn, received, cleanup := newTestWebSocket(t)
	defer cleanup()
	pc := &webConn{id: "conn-1", conn: clientConn, sessionID: "sess-1"}
	ch.addConnForTest(pc)

	responses := showTestQuestion(t, ch, nil)
	create := mustReceiveWebMessage(t, received)
	if create.Type != TypeQuestionCreate || create.SessionID != "sess-1" ||
		create.Payload[PayloadKeyQuestionID] != "q7k2mp" ||
		create.Payload[PayloadKeyExpiresAt] != float64(1_700_000_000_000) {
		t.Fatalf("create frame = %+v", create)
	}
	questions, _ := create.Payload[PayloadKeyQuestions].([]any)
	first, _ := questions[0].(map[string]any)
	options, _ := first["options"].([]any)
	blue, _ := options[1].(map[string]any)
	if first["header"] != "Color" || len(options) != 2 || blue["label"] != "Blue" || blue["recommended"] != true {
		t.Fatalf("questions = %#v", questions)
	}

	ch.handleMessage(pc, WebMessage{Type: TypeQuestionAnswer, ID: "req-1", Payload: map[string]any{
		PayloadKeyQuestionID: "q7k2mp",
		PayloadKeyAnswers:    []any{map[string]any{"selected": []any{"Blue"}, "text": "navy if possible"}},
	}})
	got := <-responses
	if got.cancel || !reflect.DeepEqual(got.answers, []bus.QuestionAnswer{{Selected: []string{"Blue"}, Text: "navy if possible"}}) {
		t.Fatalf("response = %+v", got)
	}

	ch.handleMessage(pc, WebMessage{Type: TypeQuestionCancel, ID: "req-2", Payload: map[string]any{PayloadKeyQuestionID: "q7k2mp"}})
	if got := <-responses; !got.cancel {
		t.Fatalf("response = %+v, want a cancel", got)
	}

	ch.handleMessage(pc, WebMessage{Type: TypeQuestionAnswer, ID: "req-3", Payload: map[string]any{
		PayloadKeyQuestionID: "q7k2mp", PayloadKeyAnswers: "Blue",
	}})
	if refused := mustReceiveWebMessage(t, received); refused.Type != TypeError ||
		refused.Payload["code"] != "invalid_answer" || refused.Payload[PayloadKeyRequestID] != "req-3" {
		t.Fatalf("malformed answers = %+v, want invalid_answer", refused)
	}

	ch.QuestionResolved(context.Background(), "web:sess-1", "q7k2mp", bus.QuestionAnswered)
	resolved := mustReceiveWebMessage(t, received)
	if resolved.Type != TypeQuestionResolved || resolved.Payload[PayloadKeyQuestionID] != "q7k2mp" ||
		resolved.Payload[PayloadKeyOutcome] != "answered" {
		t.Fatalf("resolved frame = %+v", resolved)
	}
	ch.handleMessage(pc, WebMessage{Type: TypeQuestionCancel, ID: "req-4", Payload: map[string]any{PayloadKeyQuestionID: "q7k2mp"}})
	if refused := mustReceiveWebMessage(t, received); refused.Payload["code"] != "question_not_waiting" {
		t.Fatalf("an answer after the question resolved = %+v, want question_not_waiting", refused)
	}
	assertNoWebMessage(t, received)
}

func TestQuestionAnswerErrorReachesTheClient(t *testing.T) {
	ch := newTestWebChannel(t)
	if err := ch.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer ch.Stop(context.Background())
	clientConn, received, cleanup := newTestWebSocket(t)
	defer cleanup()
	pc := &webConn{id: "conn-1", conn: clientConn, sessionID: "sess-1"}
	ch.addConnForTest(pc)
	showTestQuestion(t, ch, errors.New("question 1 has no option \"Purple\""))
	mustReceiveWebMessage(t, received)

	ch.handleMessage(pc, WebMessage{Type: TypeQuestionAnswer, ID: "req-1", Payload: map[string]any{
		PayloadKeyQuestionID: "q7k2mp", PayloadKeyAnswers: []any{map[string]any{"selected": []any{"Purple"}}},
	}})
	if refused := mustReceiveWebMessage(t, received); refused.Payload["code"] != "invalid_answer" ||
		refused.Payload["message"] != "question 1 has no option \"Purple\"" {
		t.Fatalf("refusal = %+v", refused)
	}
}

// A socket that joins the session gets the waiting question again, with the
// same id; once resolved, it no longer does.
func TestQuestionIsResentToAJoiningSocket(t *testing.T) {
	ch := newTestWebChannel(t)
	if err := ch.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer ch.Stop(context.Background())
	firstConn, firstReceived, firstCleanup := newTestWebSocket(t)
	defer firstCleanup()
	ch.addConnForTest(&webConn{id: "conn-1", conn: firstConn, sessionID: "sess-1"})
	showTestQuestion(t, ch, nil)
	mustReceiveWebMessage(t, firstReceived)

	clientConn, received, cleanup := newTestWebSocket(t)
	defer cleanup()
	pc := &webConn{id: "conn-2", conn: clientConn, sessionID: "sess-1"}
	ch.addConnForTest(pc)
	ch.resendQuestions(pc)
	if again := mustReceiveWebMessage(t, received); again.Type != TypeQuestionCreate ||
		again.Payload[PayloadKeyQuestionID] != "q7k2mp" || again.SessionID != "sess-1" {
		t.Fatalf("resent frame = %+v", again)
	}

	other := &webConn{id: "conn-3", conn: clientConn, sessionID: "sess-2"}
	ch.resendQuestions(other)
	assertNoWebMessage(t, received)

	ch.QuestionResolved(context.Background(), "web:sess-1", "q7k2mp", bus.QuestionExpired)
	mustReceiveWebMessage(t, received)
	ch.resendQuestions(pc)
	assertNoWebMessage(t, received)
}

// With no socket open in the chat, the question can't be shown: ShowQuestion
// fails, as an approval request does, and keeps nothing.
func TestQuestionWithoutASocketFails(t *testing.T) {
	ch := newTestWebChannel(t)
	if err := ch.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer ch.Stop(context.Background())
	err := ch.ShowQuestion(context.Background(), "web:sess-1", testPrompt(),
		func([]bus.QuestionAnswer, bool) error { return nil })
	if !errors.Is(err, channels.ErrSendFailed) {
		t.Fatalf("ShowQuestion() with no socket = %v, want ErrSendFailed", err)
	}
	if _, ok := ch.waitingQuestion("web:sess-1", "q7k2mp"); ok {
		t.Fatal("a question nobody was shown is kept")
	}
}

// A socket joining while the question resolves never gets the question's
// question.create after its question.resolved.
func TestQuestionIsNeverResentAfterItsResolvedFrame(t *testing.T) {
	ch := newTestWebChannel(t)
	if err := ch.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer ch.Stop(context.Background())
	clientConn, received, cleanup := newTestWebSocket(t)
	defer cleanup()
	pc := &webConn{id: "conn-1", conn: clientConn, sessionID: "sess-1"}
	ch.addConnForTest(pc)
	showTestQuestion(t, ch, nil)
	mustReceiveWebMessage(t, received)

	const resends = 20
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range resends {
			ch.resendQuestions(pc)
		}
	}()
	ch.QuestionResolved(context.Background(), "web:sess-1", "q7k2mp", bus.QuestionAnswered)
	wg.Wait()

	resolved := false
	for {
		select {
		case msg := <-received:
			switch msg.Type {
			case TypeQuestionResolved:
				resolved = true
			case TypeQuestionCreate:
				if resolved {
					t.Fatal("question.create after question.resolved")
				}
			}
			continue
		case <-time.After(300 * time.Millisecond):
		}
		break
	}
	if !resolved {
		t.Fatal("no question.resolved")
	}
}

// An answer the agent finds no longer waiting is refused as
// question_not_waiting, not as an invalid answer.
func TestQuestionAnswerToAGoneQuestionIsNotWaiting(t *testing.T) {
	ch := newTestWebChannel(t)
	if err := ch.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer ch.Stop(context.Background())
	clientConn, received, cleanup := newTestWebSocket(t)
	defer cleanup()
	pc := &webConn{id: "conn-1", conn: clientConn, sessionID: "sess-1"}
	ch.addConnForTest(pc)
	showTestQuestion(t, ch, channels.ErrQuestionNotWaiting)
	mustReceiveWebMessage(t, received)

	ch.handleMessage(pc, WebMessage{Type: TypeQuestionAnswer, ID: "req-1", Payload: map[string]any{
		PayloadKeyQuestionID: "q7k2mp", PayloadKeyAnswers: []any{map[string]any{"selected": []any{"Red"}}},
	}})
	if refused := mustReceiveWebMessage(t, received); refused.Payload["code"] != "question_not_waiting" {
		t.Fatalf("refusal = %+v, want question_not_waiting", refused)
	}
}

func TestQuestionAnswerFromANonOwnerIsRefused(t *testing.T) {
	ch := newTestWebChannel(t)
	ch.allowSenderFn = func(bus.SenderInfo) bool { return false }
	if err := ch.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer ch.Stop(context.Background())
	clientConn, received, cleanup := newTestWebSocket(t)
	defer cleanup()
	pc := &webConn{id: "conn-1", conn: clientConn, sessionID: "sess-1"}
	ch.addConnForTest(pc)
	responses := showTestQuestion(t, ch, nil)
	mustReceiveWebMessage(t, received)

	ch.handleMessage(pc, WebMessage{Type: TypeQuestionCancel, ID: "req-1", Payload: map[string]any{PayloadKeyQuestionID: "q7k2mp"}})
	if refused := mustReceiveWebMessage(t, received); refused.Payload["code"] != "forbidden" {
		t.Fatalf("refusal = %+v, want forbidden", refused)
	}
	select {
	case got := <-responses:
		t.Fatalf("a non-owner's cancel was delivered: %+v", got)
	default:
	}
}
