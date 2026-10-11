package config

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
)

// In a program built by "go test", an unset COMPA_HOME names a folder of its
// own in the system's temporary folder instead of ~/.compa, so no test reads
// or changes the data of the Compa you use. The programs a test starts
// inherit it, and nothing creates the folder until a test writes there. A
// test that checks the default sets COMPA_HOME to "" itself; a COMPA_HOME set
// before the tests start is used as it is.
func init() {
	if testing.Testing() && os.Getenv(EnvHome) == "" {
		_ = os.Setenv(EnvHome, filepath.Join(os.TempDir(), "compa-test-home-"+rand.Text()))
	}
}
