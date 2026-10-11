package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kayiik/compa/pkg"
)

func TestGetHomeHonoursCompaHome(t *testing.T) {
	want := t.TempDir()
	t.Setenv(EnvHome, want)

	if got := GetHome(); got != want {
		t.Errorf("GetHome() = %q, want %q", got, want)
	}
}

// Tests run with a home of their own (testhome.go), never with the user's
// Compa data.
func TestTestsDoNotUseTheUsersCompaData(t *testing.T) {
	userHome, err := os.UserHomeDir()
	if err != nil || userHome == "" {
		t.Skip("no user home directory")
	}
	if got := GetHome(); strings.EqualFold(filepath.Clean(got), filepath.Join(userHome, pkg.DefaultHome)) {
		t.Fatalf("GetHome() = %q: the tests are using your Compa data", got)
	}
}

// Without COMPA_HOME the data is in the user's profile, wherever the binary
// sits.
func TestGetHomeDefaultsToTheUserProfile(t *testing.T) {
	t.Setenv(EnvHome, "")
	userHome, err := os.UserHomeDir()
	if err != nil || userHome == "" {
		t.Skip("no user home directory")
	}

	if got, want := GetHome(), filepath.Join(userHome, pkg.DefaultHome); got != want {
		t.Errorf("GetHome() = %q, want %q", got, want)
	}
}
