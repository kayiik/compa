package compute

import (
	"fmt"
	"path/filepath"
	"regexp"
)

// idPattern is what a Compita's id looks like (see slug and uniqueID).
var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,127}$`)

// validID reports whether id is safe to use as a folder name.
func validID(id string) bool { return idPattern.MatchString(id) }

// stateDir is where Compa keeps what it knows about a Compita: its chat, its
// objective and its triggers. It is a folder of Compa's own, apart from the
// Compita's (compitas/<id>), which a Compita can write and a container mounts.
// What is kept here, such as budgets, the address a Compita reaches Compa at
// and the token of a webhook, cannot be edited from the Compita's side, nor
// pointed at another file with a link.
func stateDir(home, id string) (string, error) {
	if !validID(id) {
		return "", fmt.Errorf("compute: %q is not a Compita id", id)
	}
	return filepath.Join(home, "compita-state", id), nil
}

// statePath is the file name in the Compita's state folder.
func statePath(home, id, name string) (string, error) {
	dir, err := stateDir(home, id)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}
