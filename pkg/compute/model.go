// Package compute is the control-plane state for Compitas: the computes a
// Compa can run agents on, and the Compitas placed on them.
//
// A Compute is a place (this computer, or another machine that enrolled).
// A Compita is an agent bound to exactly one Compute at a chosen isolation
// level. The state lives in compute.json in the Compa home, beside
// pairing.json, so it adds no keys to config.json and the kernel does not
// depend on it.
package compute

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Kind says where a compute is.
type Kind string

const (
	// KindLocal is the machine Compa itself runs on.
	KindLocal Kind = "local"
	// KindRemote is another machine that enrolls with a one-time token.
	KindRemote Kind = "remote"
)

// Mode says who opens the connection between Compa and a remote compute.
type Mode string

const (
	// ModeDialOut: the compute connects out to Compa. It needs no open port.
	ModeDialOut Mode = "dial_out"
	// ModeDialIn: Compa connects to the compute's URL.
	ModeDialIn Mode = "dial_in"
)

// Status is the enrollment state of a compute.
type Status string

const (
	StatusPending  Status = "pending"
	StatusEnrolled Status = "enrolled"
)

// Isolation is how a Compita is separated from the others on its compute,
// strongest first.
type Isolation string

const (
	// IsolationMachine: the Compita has the whole compute to itself.
	IsolationMachine Isolation = "machine"
	// IsolationContainer: its own container, volume and browser.
	IsolationContainer Isolation = "container"
	// IsolationProfile: a shared container, a separate browser profile.
	// Cookies and files are separate by convention only.
	IsolationProfile Isolation = "profile"
	// IsolationShared: everything is shared with the other Compitas.
	IsolationShared Isolation = "shared"
)

// Isolations lists every level, strongest first.
var Isolations = []Isolation{IsolationMachine, IsolationContainer, IsolationProfile, IsolationShared}

// Capabilities are what a compute can offer.
type Capabilities struct {
	// Docker is true when a container runtime was found.
	Docker bool `json:"docker"`
	// DockerVersion is the runtime's version when Docker is true.
	DockerVersion string `json:"docker_version,omitempty"`
	// Browser is true when the machine has a browser its Compitas can drive
	// without a container of their own (playwright-mcp), as the browser image
	// does. A Compita in a container brings its own.
	Browser bool `json:"browser,omitempty"`
}

// Compute is a place Compitas can run.
type Compute struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Kind         Kind         `json:"kind"`
	Mode         Mode         `json:"mode,omitempty"`
	URL          string       `json:"url,omitempty"`
	Status       Status       `json:"status"`
	Capabilities Capabilities `json:"capabilities"`
	CreatedAt    time.Time    `json:"created_at"`
	EnrolledAt   *time.Time   `json:"enrolled_at,omitempty"`
	// CredentialHash is the SHA-256 of the secret the compute presents after
	// enrolling. The secret itself is never stored.
	CredentialHash string `json:"credential_hash,omitempty"`
	// DialSecret is what Compa presents to a dial-in compute. Compa is the
	// client there, so it has to keep the secret; compute.json is mode 0600.
	DialSecret string `json:"dial_secret,omitempty"`
	// PinSHA256 pins the certificate of a dial-in compute that serves its own
	// self-signed certificate (`compute serve --tls`).
	PinSHA256 string `json:"pin_sha256,omitempty"`
}

// Compita is an agent placed on one compute.
type Compita struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	ComputeID   string    `json:"compute_id"`
	Isolation   Isolation `json:"isolation"`
	// Approvals is the approval preset; empty means careful.
	Approvals string `json:"approvals,omitempty"`
	// Browser gives the Compita a web browser it can drive and a human can
	// watch. In a container it brings its own; on the machine itself the
	// compute must have one (see Capabilities).
	Browser bool `json:"browser,omitempty"`
	// Connections are the tools and integrations made for this Compita, as MCP
	// servers it may use (a GitHub server with its token, say). Their
	// environment holds secrets, so the dashboard never gets it back.
	Connections map[string]Connection `json:"connections,omitempty"`
	CreatedAt   time.Time             `json:"created_at"`
}

// Connection is an MCP server a Compita can use.
type Connection struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// Redacted returns the Compita as the dashboard may see it: connections keep
// their command and the names of their variables, never the values.
func (p Compita) Redacted() Compita {
	if len(p.Connections) == 0 {
		return p
	}
	hidden := make(map[string]Connection, len(p.Connections))
	for name, c := range p.Connections {
		env := make(map[string]string, len(c.Env))
		for k := range c.Env {
			env[k] = ""
		}
		hidden[name] = Connection{Command: c.Command, Args: c.Args, Env: env}
	}
	p.Connections = hidden
	return p
}

// Enrollment is a pending one-time token for a remote compute.
type Enrollment struct {
	ComputeID string    `json:"compute_id"`
	TokenHash string    `json:"token_hash"`
	ExpiresAt time.Time `json:"expires_at"`
}

// State is everything compute.json holds.
type State struct {
	Enabled     bool         `json:"enabled"`
	Computes    []Compute    `json:"computes"`
	Compitas    []Compita    `json:"compitas"`
	Enrollments []Enrollment `json:"enrollments,omitempty"`
}

const maxNameRunes = 64

var (
	// ErrDisabled is returned when Compitas support is off.
	ErrDisabled = errors.New("compute: Compitas support is not enabled")
	// ErrNotFound is returned for an unknown compute or Compita.
	ErrNotFound = errors.New("compute: not found")
	// ErrInvalidToken covers an unknown, used or expired enrollment token.
	ErrInvalidToken = errors.New("compute: enrollment token is invalid or expired")
	// ErrInUse is returned when removing a compute that still has Compitas.
	ErrInUse = errors.New("compute: still has Compitas")

	slugPattern = regexp.MustCompile(`[^a-z0-9]+`)
)

func cleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("compute: a name is required")
	}
	if n := len([]rune(name)); n > maxNameRunes {
		return "", fmt.Errorf("compute: the name is %d characters; the limit is %d", n, maxNameRunes)
	}
	return name, nil
}

func slug(name string) string {
	s := strings.Trim(slugPattern.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if s == "" {
		return "item"
	}
	return s
}

// ValidIsolation reports whether i is a known level.
func ValidIsolation(i Isolation) bool {
	for _, known := range Isolations {
		if i == known {
			return true
		}
	}
	return false
}

// AllowsBrowser reports whether a Compita at isolation level i can have a
// browser on this compute. Allows has already vouched for Docker when i is a
// container, whose image has the browser; at the other levels the browser runs
// on the compute itself, which must have one.
func (c Compute) AllowsBrowser(i Isolation) error {
	if i == IsolationContainer || c.Capabilities.Browser {
		return nil
	}
	return fmt.Errorf("compute %q has no browser: give the Compita its own container, or run the compute from the browser image", c.Name)
}

// Allows reports whether a compute with these capabilities can host a
// Compita at the given isolation level.
func (c Compute) Allows(i Isolation) error {
	switch i {
	case IsolationContainer:
		if !c.Capabilities.Docker {
			return fmt.Errorf("compute %q has no Docker, so it cannot give a Compita its own container", c.Name)
		}
	case IsolationMachine:
		if c.Kind == KindLocal {
			return errors.New("the local computer is shared with Compa, so it cannot give a Compita the whole machine")
		}
	case IsolationProfile, IsolationShared:
	default:
		return fmt.Errorf("unknown isolation level %q", i)
	}
	return nil
}
