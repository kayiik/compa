package api

import (
	"runtime"
	"testing"
)

// skipWithoutShell skips a test whose stand-in kernel is a /bin/sh script,
// which Windows cannot run.
func skipWithoutShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in kernel is a /bin/sh script")
	}
}
