package compute

import (
	"strings"
	"testing"
	"time"
)

func TestProgressFileIsPerObjective(t *testing.T) {
	a := &Objective{StartedAt: time.Date(2026, 10, 5, 22, 0, 0, 0, time.UTC)}
	b := &Objective{StartedAt: time.Date(2026, 10, 5, 22, 0, 1, 0, time.UTC)}
	if a.ProgressFile() == b.ProgressFile() || a.ProgressFile() != "PROGRESS-20261005-220000.md" {
		t.Fatalf("progress files = %q, %q", a.ProgressFile(), b.ProgressFile())
	}
	if p := FirstPrompt("g", a.ProgressFile(), nil); !strings.Contains(p, a.ProgressFile()) || strings.Contains(p, b.ProgressFile()) {
		t.Fatalf("the prompt must name this objective's file: %q", p)
	}
}
