package compute

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	connectionName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	envName        = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
)

const (
	maxConnections = 10
	maxArgs        = 50
	maxArgLen      = 2000
	maxEnvVars     = 50
	maxEnvValueLen = 8000
)

// ValidateConnection checks a connection before it is stored.
func ValidateConnection(name string, c Connection) error {
	if !connectionName.MatchString(name) {
		return errors.New("compute: a connection name is lowercase letters, digits and _, starting with a letter (32 at most)")
	}
	if name == BrowserServerName {
		return fmt.Errorf("compute: %q is the browser; choose another name", BrowserServerName)
	}
	if strings.TrimSpace(c.Command) == "" || len(c.Command) > 512 {
		return errors.New("compute: a connection needs a command (512 characters at most)")
	}
	if len(c.Args) > maxArgs {
		return fmt.Errorf("compute: at most %d arguments", maxArgs)
	}
	for _, a := range c.Args {
		if len(a) > maxArgLen {
			return fmt.Errorf("compute: an argument is longer than %d characters", maxArgLen)
		}
	}
	if len(c.Env) > maxEnvVars {
		return fmt.Errorf("compute: at most %d environment variables", maxEnvVars)
	}
	for k, v := range c.Env {
		if !envName.MatchString(k) {
			return fmt.Errorf("compute: %q is not a valid variable name", k)
		}
		if len(v) > maxEnvValueLen {
			return fmt.Errorf("compute: the value of %s is too long", k)
		}
	}
	return nil
}

// SetConnection adds or replaces a connection of a Compita. A new one with
// the name of an existing connection keeps the secrets it does not restate:
// a variable left empty keeps its stored value, so the dashboard can edit a
// connection without ever being told its secrets.
func (s *Store) SetConnection(compitaID, name string, c Connection) error {
	if err := ValidateConnection(name, c); err != nil {
		return err
	}
	return s.update(func(st *State) error {
		for i := range st.Compitas {
			p := &st.Compitas[i]
			if p.ID != compitaID {
				continue
			}
			old, exists := p.Connections[name]
			if !exists && len(p.Connections) >= maxConnections {
				return fmt.Errorf("compute: at most %d connections per Compita", maxConnections)
			}
			merged := Connection{Command: strings.TrimSpace(c.Command), Args: c.Args, Env: map[string]string{}}
			for k, v := range c.Env {
				if v == "" {
					v = old.Env[k]
				}
				merged.Env[k] = v
			}
			if p.Connections == nil {
				p.Connections = map[string]Connection{}
			}
			p.Connections[name] = merged
			return nil
		}
		return ErrNotFound
	})
}

// RemoveConnection deletes a connection of a Compita.
func (s *Store) RemoveConnection(compitaID, name string) error {
	return s.update(func(st *State) error {
		for i := range st.Compitas {
			p := &st.Compitas[i]
			if p.ID != compitaID {
				continue
			}
			if _, ok := p.Connections[name]; !ok {
				return ErrNotFound
			}
			delete(p.Connections, name)
			return nil
		}
		return ErrNotFound
	})
}
