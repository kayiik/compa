package bus

import "time"

// Questions the agent asks the owner with the ask_user tool. A channel that
// shows choices (channels.QuestionPresenter) gets them as they are; others
// get them as a numbered text message.

// QuestionOption is one of a question's choices.
type QuestionOption struct {
	// Label is the choice itself, short; an answer names it.
	Label string `json:"label"`
	// Description says what choosing it means, in one line.
	Description string `json:"description"`
	// Recommended marks the choice the agent suggests.
	Recommended bool `json:"recommended,omitempty"`
}

// Question is one question with its choices.
type Question struct {
	// Header is a short title for the question.
	Header string `json:"header"`
	// Question is the question as asked.
	Question string           `json:"question"`
	Options  []QuestionOption `json:"options"`
	// Multiple lets the owner choose several options.
	Multiple bool `json:"multiple,omitempty"`
}

// QuestionAnswer answers one question: the labels of the chosen options,
// words of the owner's own, or both.
type QuestionAnswer struct {
	Selected []string `json:"selected,omitempty"`
	Text     string   `json:"text,omitempty"`
}

// QuestionPrompt is a set of questions waiting for the owner's answers.
type QuestionPrompt struct {
	// ID names the prompt in its answers.
	ID        string     `json:"question_id"`
	Questions []Question `json:"questions"`
	// ExpiresAt is when the agent stops waiting.
	ExpiresAt time.Time `json:"expires_at"`
}

// Question outcomes, as a channel that shows questions is told.
const (
	QuestionAnswered  = "answered"
	QuestionCancelled = "cancelled"
	QuestionExpired   = "expired"
	// QuestionEnded: the turn that asked ended before an answer.
	QuestionEnded = "ended"
)
