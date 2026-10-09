package compute

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kayiik/compa/pkg/fileutil"
)

// ObjectiveStatus is where a Compita stands on its objective.
type ObjectiveStatus string

const (
	ObjActive    ObjectiveStatus = "active"
	ObjDone      ObjectiveStatus = "done"
	ObjBlocked   ObjectiveStatus = "blocked"
	ObjPaused    ObjectiveStatus = "paused"
	ObjStopped   ObjectiveStatus = "stopped"
	ObjExhausted ObjectiveStatus = "exhausted" // a budget ran out
)

// Free reports whether a Compita can take new work: it has no objective, or
// its last one ended. One that is blocked or paused waits for the owner, so
// scheduled and incoming work waits too.
func Free(o *Objective) bool {
	return o == nil || o.Status == ObjDone || o.Status == ObjStopped || o.Status == ObjExhausted
}

// Objective is a goal a Compita keeps working on, turn after turn, until it
// is done, blocked, out of budget or stopped.
type Objective struct {
	Goal       string          `json:"goal"`
	Status     ObjectiveStatus `json:"status"`
	Turns      int             `json:"turns"`
	MaxTurns   int             `json:"max_turns"`
	MaxMinutes int             `json:"max_minutes"`
	StartedAt  time.Time       `json:"started_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
	// Spent is the active working time so far, so a budget survives restarts.
	SpentSeconds int `json:"spent_seconds"`
	// Note is the Compita's latest progress note; Summary or Reason end it.
	Note    string `json:"note,omitempty"`
	Summary string `json:"summary,omitempty"`
	Reason  string `json:"reason,omitempty"`
	// Claimed is the Compita's own report that it is done, kept while a check
	// turn confirms it. A Compita that says "done" is not trusted until then.
	Claimed string `json:"claimed,omitempty"`
	// Sig and Stalls spot a Compita that keeps repeating itself.
	Sig    string `json:"sig,omitempty"`
	Stalls int    `json:"stalls,omitempty"`
	// BaseURL is how the Compita reaches Compa to message its peers.
	BaseURL string `json:"base_url,omitempty"`
}

// Defaults for a new objective.
const (
	DefaultMaxTurns   = 50
	DefaultMaxMinutes = 120
)

var objMu sync.Mutex

func objectivePath(home, id string) (string, error) {
	return statePath(home, id, "objective.json")
}

// LoadObjective returns the Compita's objective, or nil when it has none.
func LoadObjective(home, id string) (*Objective, error) {
	objMu.Lock()
	defer objMu.Unlock()
	return loadObjective(home, id)
}

func loadObjective(home, id string) (*Objective, error) {
	p, err := objectivePath(home, id)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var o Objective
	if err := json.Unmarshal(raw, &o); err != nil {
		return nil, err
	}
	return &o, nil
}

func saveObjective(home, id string, o *Objective) error {
	o.UpdatedAt = time.Now()
	p, err := objectivePath(home, id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(p, raw, 0o600)
}

// UpdateObjective applies fn to the stored objective under a lock.
func UpdateObjective(home, id string, fn func(*Objective) error) (*Objective, error) {
	objMu.Lock()
	defer objMu.Unlock()
	o, err := loadObjective(home, id)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, ErrNotFound
	}
	if err := fn(o); err != nil {
		return o, err
	}
	return o, saveObjective(home, id, o)
}

// StartObjective sets a new objective and makes it active. A Compita works on
// one objective at a time; an active one must be stopped first.
func StartObjective(home, id, goal, baseURL string, maxTurns, maxMinutes int) (*Objective, error) {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return nil, errors.New("compute: an objective needs a goal")
	}
	if len(goal) > 8000 {
		return nil, errors.New("compute: the goal is too long (8000 characters at most)")
	}
	objMu.Lock()
	defer objMu.Unlock()
	if cur, err := loadObjective(home, id); err != nil {
		return nil, err
	} else if cur != nil && cur.Status == ObjActive {
		return nil, errors.New("compute: this Compita is already working on an objective; stop it first")
	}
	if maxTurns <= 0 {
		maxTurns = DefaultMaxTurns
	}
	if maxMinutes <= 0 {
		maxMinutes = DefaultMaxMinutes
	}
	o := &Objective{
		Goal: goal, Status: ObjActive, MaxTurns: maxTurns, MaxMinutes: maxMinutes,
		StartedAt: time.Now(), BaseURL: baseURL,
	}
	return o, saveObjective(home, id, o)
}

// Markers a Compita ends a turn with.
const (
	markDone     = "OBJECTIVE_DONE:"
	markVerified = "VERIFIED:"
	markBlocked  = "OBJECTIVE_BLOCKED:"
	markContinue = "CONTINUE:"
)

const endingRules = "End every reply with exactly one line:\n" +
	markDone + " <what you accomplished>   -- only when the objective is fully achieved; it will be checked before it counts\n" +
	markBlocked + " <what you need>   -- only when you cannot go on without something you cannot get yourself; say exactly what\n" +
	markContinue + " <what you will do next>   -- in every other case"

// ProgressFile is where the Compita keeps this objective's acceptance criteria
// and progress. It is named for the objective, so a file left by an earlier
// one is never mistaken for this one's.
func (o *Objective) ProgressFile() string {
	return "PROGRESS-" + o.StartedAt.UTC().Format("20060102-150405") + ".md"
}

func rosterLine(colleagues []string) string {
	if len(colleagues) == 0 {
		return ""
	}
	return "Colleagues you can hand work to with the message_peer tool (each has its own computer): " +
		strings.Join(colleagues, ", ") + ". Delegate what they can do instead of repeating it, then use their answer.\n\n"
}

// FirstPrompt starts work on an objective.
func FirstPrompt(goal, progress string, colleagues []string) string {
	return "You have been given an objective to pursue on your own, over as many steps as it takes. " +
		"Nobody will answer questions while you work, so make reasonable decisions and keep going.\n\n" +
		"OBJECTIVE:\n" + goal + "\n\n" +
		"Keep this objective's progress in " + progress + " in your workspace. If it already exists, you have started this before: " +
		"read it and carry on from there. Otherwise write it first, with the acceptance criteria (how anyone could check the " +
		"objective is met) and a short plan. Keep it up to date as you go: what is done, what is next. " +
		"Later steps start from it.\n\n" + rosterLine(colleagues) + endingRules
}

// NextPrompt asks for the next step after a turn that did not finish.
func NextPrompt(goal, note, progress string, colleagues []string) string {
	s := "Continue working on your objective:\n" + goal + "\n"
	if note != "" {
		s += "\nWhere you left off: " + note + "\n"
	}
	return s + "\nRead " + progress + ", take the next concrete step, and update it.\n\n" + rosterLine(colleagues) + endingRules
}

// VerifyPrompt asks the Compita to prove a claimed result before it counts.
func VerifyPrompt(goal, claimed, progress string) string {
	return "You reported the objective as done:\n" + claimed + "\n\nOBJECTIVE:\n" + goal + "\n\n" +
		"Before this is accepted, check it for real against the acceptance criteria in " + progress + ": run the commands, " +
		"read the files, look at the actual results. Do not rely on memory.\n" +
		"End your reply with exactly one line:\n" +
		markVerified + " <the evidence you checked>   -- every criterion is met\n" +
		markContinue + " <what is still missing>   -- anything is missing or failing\n" +
		markBlocked + " <what you need>   -- you cannot finish without something you cannot get yourself"
}

// Outcome reads the marker a reply ends with. With no marker the objective
// simply goes on: a reply never ends the work by itself.
func Outcome(reply string) (status ObjectiveStatus, text string) {
	status, text, _ = outcomeMarked(reply)
	return status, text
}

// outcomeMarked is Outcome that also says whether any marker was found.
func outcomeMarked(reply string) (status ObjectiveStatus, text string, found bool) {
	lines := strings.Split(strings.TrimSpace(reply), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		for _, m := range []struct {
			mark string
			st   ObjectiveStatus
		}{{markDone, ObjDone}, {markVerified, ObjDone}, {markBlocked, ObjBlocked}, {markContinue, ObjActive}} {
			if strings.HasPrefix(l, m.mark) {
				return m.st, strings.TrimSpace(strings.TrimPrefix(l, m.mark)), true
			}
		}
	}
	return ObjActive, "", false
}

// signature identifies a reply for repeat detection, ignoring case and spacing.
func signature(reply string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.Join(strings.Fields(reply), " "))))
	return hex.EncodeToString(sum[:8])
}

// DriveOptions tune Drive; zero values give production behavior.
type DriveOptions struct {
	// Turn runs one turn of the Compita and returns its reply.
	Turn func(ctx context.Context, prompt string) (string, error)
	// Say records a Compita line in its chat; Log is optional.
	Say func(text string, isError bool)
	// Pause is the gap between turns; Backoff computes retry waits.
	Pause   time.Duration
	Backoff func(failures int) time.Duration
	// MaxFailures is how many turns in a row may fail before the objective
	// is marked blocked. Waiting for an offline compute does not count.
	MaxFailures int
	// MaxStalls is how many identical replies in a row mean no progress.
	MaxStalls int
	// Colleagues names the other Compitas, listed in each prompt. Optional.
	Colleagues func() []string
}

func defaultBackoff(n int) time.Duration {
	d := 5 * time.Second << min(n, 5)
	return min(d, 2*time.Minute)
}

// Drive works on the Compita's objective until it ends, or ctx is cancelled.
// Cancelling leaves an active objective active, so the next start resumes it.
// Pausing or stopping are done by changing the status; Drive notices after
// the turn in flight.
//
// The brakes: a turn and a time budget, a cap on failing turns in a row, a cap
// on identical replies in a row, and a check turn before "done" is accepted.
func Drive(ctx context.Context, home, id string, opt DriveOptions) {
	if opt.Backoff == nil {
		opt.Backoff = defaultBackoff
	}
	if opt.MaxFailures == 0 {
		opt.MaxFailures = 8
	}
	if opt.MaxStalls == 0 {
		opt.MaxStalls = 3
	}
	colleagues := func() []string {
		if opt.Colleagues == nil {
			return nil
		}
		return opt.Colleagues()
	}
	finish := func(st ObjectiveStatus, summary, reason string) {
		_, _ = UpdateObjective(home, id, func(o *Objective) error {
			if o.Status == ObjActive {
				o.Status, o.Summary, o.Reason = st, summary, reason
			}
			return nil
		})
	}
	sleep := func(d time.Duration) bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(d):
			return true
		}
	}
	failures := 0
	for {
		o, err := LoadObjective(home, id)
		if err != nil || o == nil || o.Status != ObjActive || ctx.Err() != nil {
			return
		}
		if o.Turns >= o.MaxTurns {
			finish(ObjExhausted, "", fmt.Sprintf("used all %d turns", o.MaxTurns))
			return
		}
		if o.SpentSeconds >= o.MaxMinutes*60 {
			finish(ObjExhausted, "", fmt.Sprintf("used all %d minutes", o.MaxMinutes))
			return
		}
		verifying := o.Claimed != ""
		var prompt string
		switch {
		case verifying:
			prompt = VerifyPrompt(o.Goal, o.Claimed, o.ProgressFile())
		case o.Turns == 0:
			prompt = FirstPrompt(o.Goal, o.ProgressFile(), colleagues())
		default:
			prompt = NextPrompt(o.Goal, o.Note, o.ProgressFile(), colleagues())
		}
		began := time.Now()
		reply, err := opt.Turn(ctx, prompt)
		spent := int(time.Since(began).Seconds())
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			if errors.Is(err, ErrOffline) {
				// The machine is away; wait for it rather than give up.
				if !sleep(opt.Backoff(1)) {
					return
				}
				continue
			}
			failures++
			if opt.Say != nil {
				opt.Say("Objective turn failed: "+err.Error(), true)
			}
			if failures >= opt.MaxFailures {
				finish(ObjBlocked, "", fmt.Sprintf("%d turns failed in a row; last error: %v", failures, err))
				return
			}
			if !sleep(opt.Backoff(failures)) {
				return
			}
			continue
		}
		failures = 0
		st, text, marked := outcomeMarked(reply)
		sig := signature(reply)
		ended := false
		_, _ = UpdateObjective(home, id, func(o *Objective) error {
			o.Turns++
			o.SpentSeconds += spent
			if text != "" {
				o.Note = text
			}
			if o.Status != ObjActive {
				return nil
			}
			if sig == o.Sig {
				o.Stalls++
			} else {
				o.Sig, o.Stalls = sig, 0
			}
			switch {
			case st == ObjBlocked:
				o.Status, o.Reason, o.Claimed, ended = ObjBlocked, text, "", true
			case verifying && st == ObjActive && marked:
				o.Claimed = "" // the check found gaps: back to work
			case verifying:
				// Confirmed (or the Compita gave no verdict, which must not
				// strand finished work).
				o.Status, o.Summary, o.Claimed, ended = ObjDone, firstNonEmpty(text, o.Claimed), "", true
			case st == ObjDone:
				o.Claimed = firstNonEmpty(text, "the objective is complete")
				o.Stalls = 0
			case o.Stalls >= opt.MaxStalls:
				o.Status, o.Reason, ended = ObjBlocked, fmt.Sprintf("no progress: the last %d replies were identical", o.Stalls+1), true
			}
			return nil
		})
		if opt.Say != nil {
			opt.Say(reply, false)
		}
		if ended {
			return
		}
		if !sleep(opt.Pause) {
			return
		}
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
