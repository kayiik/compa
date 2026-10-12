// Compa - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kayiik/compa/pkg/bus"
	"github.com/kayiik/compa/pkg/tools"
)

// askUserToolName is the tool that asks the owner questions with choices.
// The approval policy never asks about it (askApplies): it only asks.
const askUserToolName = "ask_user"

const (
	askUserMaxQuestions        = 4
	askUserMinOptions          = 2
	askUserMaxOptions          = 8
	askUserHeaderMaxRunes      = 40
	askUserQuestionMaxRunes    = 1000
	askUserLabelMaxRunes       = 80
	askUserDescriptionMaxRunes = 300
)

// askUserTool asks the owner questions and waits for the answers
// (AgentLoop.askQuestions).
type askUserTool struct {
	al *AgentLoop
}

var _ tools.SelfTimed = (*askUserTool)(nil)

func newAskUserTool(al *AgentLoop) *askUserTool { return &askUserTool{al: al} }

func (t *askUserTool) Name() string { return askUserToolName }

func (t *askUserTool) Description() string {
	return "Ask the person you work for one to four questions, each with two to eight options, and wait " +
		"for the answers. Use it when you need their decision or preference and can't reasonably assume it; " +
		"don't use it for what you can find out yourself. They may choose options, answer in their own " +
		"words, or cancel. Mark the option you suggest as recommended."
}

func (t *askUserTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"questions": map[string]any{
				"type":        "array",
				"description": "The questions, asked together (1 to 4).",
				"minItems":    1,
				"maxItems":    askUserMaxQuestions,
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"header": map[string]any{
							"type":        "string",
							"description": "A short title for the question, a few words.",
						},
						"question": map[string]any{
							"type":        "string",
							"description": "The question, complete and clear.",
						},
						"options": map[string]any{
							"type":        "array",
							"description": "The choices (2 to 8). A typed answer is always allowed; don't add an 'Other' option.",
							"minItems":    askUserMinOptions,
							"maxItems":    askUserMaxOptions,
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"label": map[string]any{
										"type":        "string",
										"description": "The choice, in a few words.",
									},
									"description": map[string]any{
										"type":        "string",
										"description": "What choosing it means, in one line.",
									},
									"recommended": map[string]any{
										"type":        "boolean",
										"description": "Marks the option you suggest; at most one per question.",
									},
								},
								"required": []string{"label", "description"},
							},
						},
						"multiple": map[string]any{
							"type":        "boolean",
							"description": "Whether several options may be chosen. Default false.",
						},
					},
					"required": []string{"header", "question", "options"},
				},
			},
		},
		"required": []string{"questions"},
	}
}

// CallTimeout gives the call the time the owner has to answer, on top of
// the general tool timeout.
func (t *askUserTool) CallTimeout(context.Context, map[string]any) time.Duration {
	return t.timeout()
}

func (t *askUserTool) timeout() time.Duration {
	if t.al != nil && t.al.questions != nil {
		return t.al.questions.timeout
	}
	return ownerQuestionTimeout
}

func (t *askUserTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	questions, err := parseAskUserQuestions(args)
	if err != nil {
		return tools.ErrorResult(err.Error())
	}
	ts := turnStateFromContext(ctx)
	if t.al == nil || ts == nil {
		return tools.ErrorResult("ask_user can only be used during a conversation turn.")
	}
	var inbound *bus.InboundContext
	if ts.turnCtx != nil {
		inbound = ts.turnCtx.Inbound
	}
	result := t.al.askQuestions(ctx, ts.eventMeta("runTurn", "turn.tool.ask_user"), inbound, questions)
	text := questionResultText(questions, result, t.timeout())
	if result.outcome == questionUnavailable {
		return tools.ErrorResult(text)
	}
	return tools.NewToolResult(text)
}

// parseAskUserQuestions reads and checks the questions of an ask_user call.
func parseAskUserQuestions(args map[string]any) ([]bus.Question, error) {
	raw, ok := args["questions"].([]any)
	if !ok || len(raw) == 0 || len(raw) > askUserMaxQuestions {
		return nil, fmt.Errorf("questions must be a list of 1 to %d questions", askUserMaxQuestions)
	}
	questions := make([]bus.Question, 0, len(raw))
	for i, item := range raw {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("questions[%d] must be an object", i)
		}
		where := fmt.Sprintf("questions[%d]", i)
		var q bus.Question
		var err error
		if q.Header, err = requiredText(obj, "header", where, askUserHeaderMaxRunes); err != nil {
			return nil, err
		}
		if q.Question, err = requiredText(obj, "question", where, askUserQuestionMaxRunes); err != nil {
			return nil, err
		}
		if v, present := obj["multiple"]; present && v != nil {
			b, ok := v.(bool)
			if !ok {
				return nil, fmt.Errorf("%s.multiple must be true or false", where)
			}
			q.Multiple = b
		}
		opts, ok := obj["options"].([]any)
		if !ok || len(opts) < askUserMinOptions || len(opts) > askUserMaxOptions {
			return nil, fmt.Errorf("%s.options must be a list of %d to %d options",
				where, askUserMinOptions, askUserMaxOptions)
		}
		recommended := 0
		for j, rawOpt := range opts {
			optObj, ok := rawOpt.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s.options[%d] must be an object", where, j)
			}
			optWhere := fmt.Sprintf("%s.options[%d]", where, j)
			var opt bus.QuestionOption
			if opt.Label, err = requiredText(optObj, "label", optWhere, askUserLabelMaxRunes); err != nil {
				return nil, err
			}
			if opt.Description, err = requiredText(optObj, "description", optWhere, askUserDescriptionMaxRunes); err != nil {
				return nil, err
			}
			if v, present := optObj["recommended"]; present && v != nil {
				b, ok := v.(bool)
				if !ok {
					return nil, fmt.Errorf("%s.recommended must be true or false", optWhere)
				}
				opt.Recommended = b
			}
			if opt.Recommended {
				recommended++
			}
			if _, dup := optionLabel(q, opt.Label); dup {
				return nil, fmt.Errorf("%s.options has the label %q twice", where, opt.Label)
			}
			q.Options = append(q.Options, opt)
		}
		if recommended > 1 {
			return nil, fmt.Errorf("%s: at most one option may be recommended", where)
		}
		questions = append(questions, q)
	}
	return questions, nil
}

// requiredText reads obj[key] as one non-empty line of at most maxRunes.
func requiredText(obj map[string]any, key, where string, maxRunes int) (string, error) {
	s, _ := obj[key].(string)
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("%s.%s is required", where, key)
	}
	if key != "question" && strings.ContainsAny(s, "\r\n") {
		return "", fmt.Errorf("%s.%s must be one line", where, key)
	}
	if n := utf8.RuneCountInString(s); n > maxRunes {
		return "", fmt.Errorf("%s.%s is too long (%d characters, at most %d)", where, key, n, maxRunes)
	}
	return s, nil
}
