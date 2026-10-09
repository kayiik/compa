package compute

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func fast(turn func(context.Context, string) (string, error)) DriveOptions {
	return DriveOptions{Turn: turn, Backoff: func(int) time.Duration { return time.Millisecond }, MaxFailures: 3}
}

func TestOutcome(t *testing.T) {
	for _, c := range []struct {
		in   string
		st   ObjectiveStatus
		text string
	}{
		{"did stuff\nOBJECTIVE_DONE: shipped", ObjDone, "shipped"},
		{"x\nOBJECTIVE_BLOCKED: need a key\n", ObjBlocked, "need a key"},
		{"x\nCONTINUE: write tests", ObjActive, "write tests"},
		{"all good\nVERIFIED: tests pass", ObjDone, "tests pass"},
		{"no marker at all", ObjActive, ""},
	} {
		if st, text := Outcome(c.in); st != c.st || text != c.text {
			t.Errorf("Outcome(%q) = %s, %q", c.in, st, text)
		}
	}
}

func TestDriveKeepsGoingUntilDone(t *testing.T) {
	home := t.TempDir()
	if _, err := StartObjective(home, "ana", "build it", "", 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := StartObjective(home, "ana", "again", "", 0, 0); err == nil {
		t.Fatal("two active objectives at once")
	}
	var prompts []string
	replies := []string{
		"step one\nCONTINUE: two",
		"no marker here",
		"step three\nOBJECTIVE_DONE: all built",
		"ran the tests\nVERIFIED: 12 tests pass",
	}
	var said []string
	opt := fast(func(_ context.Context, p string) (string, error) {
		prompts = append(prompts, p)
		return replies[len(prompts)-1], nil
	})
	opt.Say = func(s string, _ bool) { said = append(said, s) }
	opt.Colleagues = func() []string { return []string{"Bo"} }
	Drive(context.Background(), home, "ana", opt)

	o, _ := LoadObjective(home, "ana")
	if o.Status != ObjDone || o.Turns != 4 || o.Summary != "12 tests pass" {
		t.Fatalf("objective = %+v", o)
	}
	if !strings.Contains(prompts[0], "OBJECTIVE:") || !strings.Contains(prompts[0], "PROGRESS-") || !strings.Contains(prompts[0], "message_peer") {
		t.Fatalf("first prompt = %q", prompts[0])
	}
	if !strings.Contains(prompts[1], "Where you left off: two") {
		t.Fatalf("second prompt = %q", prompts[1])
	}
	if !strings.Contains(prompts[3], "You reported the objective as done") {
		t.Fatalf("a claimed DONE must be checked first: %q", prompts[3])
	}
	if len(said) != 4 {
		t.Fatalf("said %d", len(said))
	}
}

func TestDriveSendsBackADoneThatFailsTheCheck(t *testing.T) {
	home := t.TempDir()
	_, _ = StartObjective(home, "a", "ship it", "", 0, 0)
	replies := []string{
		"OBJECTIVE_DONE: shipped",
		"I ran the tests and 2 fail\nCONTINUE: fix the failing tests",
		"fixed\nOBJECTIVE_DONE: now really shipped",
		"VERIFIED: all green",
	}
	var prompts []string
	Drive(context.Background(), home, "a", fast(func(_ context.Context, p string) (string, error) {
		prompts = append(prompts, p)
		return replies[len(prompts)-1], nil
	}))
	o, _ := LoadObjective(home, "a")
	if o.Status != ObjDone || o.Turns != 4 || o.Summary != "all green" {
		t.Fatalf("objective = %+v", o)
	}
	if !strings.Contains(prompts[2], "Where you left off: fix the failing tests") {
		t.Fatalf("after a failed check the Compita resumes from the gap: %q", prompts[2])
	}
}

func TestDriveStopsWhenItGoesNowhere(t *testing.T) {
	home := t.TempDir()
	_, _ = StartObjective(home, "a", "g", "", 0, 0)
	n := 0
	Drive(context.Background(), home, "a", fast(func(context.Context, string) (string, error) {
		n++
		return "I am thinking about it.  \nCONTINUE: think", nil
	}))
	o, _ := LoadObjective(home, "a")
	if o.Status != ObjBlocked || !strings.Contains(o.Reason, "no progress") || n != 4 {
		t.Fatalf("a Compita repeating itself must stop: %+v after %d turns", o, n)
	}
}

func TestDriveResumesAClaimedDoneWithTheCheck(t *testing.T) {
	home := t.TempDir()
	_, _ = StartObjective(home, "a", "g", "", 0, 0)
	_, _ = UpdateObjective(home, "a", func(o *Objective) error { o.Turns, o.Claimed = 3, "built"; return nil })
	var first string
	Drive(context.Background(), home, "a", fast(func(_ context.Context, p string) (string, error) {
		if first == "" {
			first = p
		}
		return "VERIFIED: checked", nil
	}))
	if !strings.Contains(first, "You reported the objective as done:\nbuilt") {
		t.Fatalf("after a restart the check comes first: %q", first)
	}
	if o, _ := LoadObjective(home, "a"); o.Status != ObjDone {
		t.Fatalf("status = %s", o.Status)
	}
}

func TestDriveBlockedBudgetAndErrors(t *testing.T) {
	home := t.TempDir()
	_, _ = StartObjective(home, "a", "g", "", 0, 0)
	Drive(context.Background(), home, "a", fast(func(context.Context, string) (string, error) {
		return "OBJECTIVE_BLOCKED: need credentials", nil
	}))
	if o, _ := LoadObjective(home, "a"); o.Status != ObjBlocked || o.Reason != "need credentials" {
		t.Fatalf("blocked = %+v", o)
	}

	_, _ = StartObjective(home, "b", "g", "", 2, 0)
	n := 0
	Drive(context.Background(), home, "b", fast(func(context.Context, string) (string, error) { n++; return "ok", nil }))
	if o, _ := LoadObjective(home, "b"); o.Status != ObjExhausted || n != 2 {
		t.Fatalf("budget = %+v after %d turns", o, n)
	}

	_, _ = StartObjective(home, "c", "g", "", 0, 0)
	Drive(context.Background(), home, "c", fast(func(context.Context, string) (string, error) { return "", errors.New("boom") }))
	if o, _ := LoadObjective(home, "c"); o.Status != ObjBlocked || !strings.Contains(o.Reason, "boom") {
		t.Fatalf("errors = %+v", o)
	}
}

func TestDriveRecoversAndWaitsOutOfflineCompute(t *testing.T) {
	home := t.TempDir()
	_, _ = StartObjective(home, "a", "g", "", 0, 0)
	calls := 0
	Drive(context.Background(), home, "a", fast(func(context.Context, string) (string, error) {
		calls++
		switch {
		case calls <= 10:
			return "", ErrOffline // far more than MaxFailures: offline never counts
		case calls == 11:
			return "", errors.New("flaky")
		}
		return "OBJECTIVE_DONE: made it", nil
	}))
	if o, _ := LoadObjective(home, "a"); o.Status != ObjDone || o.Turns != 2 {
		t.Fatalf("objective = %+v", o)
	}
}

func TestDriveStopAndResume(t *testing.T) {
	home := t.TempDir()
	_, _ = StartObjective(home, "a", "g", "", 0, 0)
	ctx, cancel := context.WithCancel(context.Background())
	turns := 0
	Drive(ctx, home, "a", fast(func(context.Context, string) (string, error) {
		turns++
		cancel() // like a launcher shutdown mid-objective
		return "CONTINUE: more", nil
	}))
	if o, _ := LoadObjective(home, "a"); o.Status != ObjActive {
		t.Fatalf("a shutdown must leave the objective active, got %s", o.Status)
	}

	// The user stops it during a turn: the loop ends after that turn.
	Drive(context.Background(), home, "a", fast(func(context.Context, string) (string, error) {
		_, _ = UpdateObjective(home, "a", func(o *Objective) error { o.Status = ObjStopped; return nil })
		return "CONTINUE: more", nil
	}))
	if o, _ := LoadObjective(home, "a"); o.Status != ObjStopped {
		t.Fatalf("status = %s", o.Status)
	}
	if _, err := StartObjective(home, "a", "new goal", "", 0, 0); err != nil {
		t.Fatalf("a stopped objective can be replaced: %v", err)
	}
}
