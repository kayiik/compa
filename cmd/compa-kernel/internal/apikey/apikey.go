// Package apikey reads an API key a command is given on stdin.
package apikey

import (
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/kayiik/compa/pkg/auth"
)

// Terminal tells whether a file is a terminal and reads a line from it
// without echo. Tests stand in for a terminal with their own.
type Terminal struct {
	IsTerminal func(f *os.File) bool
	ReadHidden func(f *os.File) ([]byte, error)
}

// System is the real terminal.
var System = Terminal{
	IsTerminal: func(f *os.File) bool { return term.IsTerminal(int(f.Fd())) },
	ReadHidden: func(f *os.File) ([]byte, error) { return term.ReadPassword(int(f.Fd())) },
}

// Read reads the key for provider from in: without echoing it when in is a
// terminal, so it doesn't stay on screen or in the scrollback, else as a
// line. The prompt goes to out. An empty key is an error.
func Read(provider string, in *os.File, out io.Writer, t Terminal) (string, error) {
	if !t.IsTerminal(in) {
		cred, err := auth.LoginPasteTokenWithPrompt(provider, in, out)
		if err != nil {
			return "", err
		}
		return cred.AccessToken, nil
	}
	fmt.Fprintf(out, "Paste your %s API key (input hidden): ", provider)
	raw, err := t.ReadHidden(in)
	fmt.Fprintln(out)
	if err != nil {
		return "", fmt.Errorf("reading key: %w", err)
	}
	key := strings.TrimSpace(string(raw))
	if key == "" {
		return "", fmt.Errorf("token cannot be empty")
	}
	return key, nil
}
