package compute

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kayiik/compa/pkg/config"
	"github.com/kayiik/compa/pkg/fileutil"
)

const (
	// FileName is the store's file in the Compa home.
	FileName = "compute.json"
	// TokenTTL is how long an enrollment token can be redeemed.
	TokenTTL = 15 * time.Minute

	// LocalID is the id of the compute that is this computer.
	LocalID = "this-computer"
)

// Store reads and writes compute.json under one Compa home. Every change runs
// under the file's lock, so the dashboard and a compute enrolling cannot lose
// each other's writes.
type Store struct {
	Home string
	// Detect reports what this computer offers Compitas: a container runtime
	// and a browser. Tests replace it.
	Detect func() Capabilities
	now    func() time.Time
}

// NewStore returns a store for home that detects this computer's capabilities
// with Detect.
func NewStore(home string) *Store {
	return &Store{Home: home, Detect: Detect, now: time.Now}
}

func (s *Store) path() (string, error) {
	if strings.TrimSpace(s.Home) == "" {
		return "", errors.New("compute: the Compa home is required")
	}
	return filepath.Join(s.Home, FileName), nil
}

// Load returns the current state; a missing file is an empty, disabled state.
func (s *Store) Load() (State, error) {
	path, err := s.path()
	if err != nil {
		return State{}, err
	}
	return load(path)
}

func load(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{Computes: []Compute{}, Compitas: []Compita{}}, nil
		}
		return State{}, fmt.Errorf("compute: read %s: %w", path, err)
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}, fmt.Errorf("compute: parse %s: %w", path, err)
	}
	if st.Computes == nil {
		st.Computes = []Compute{}
	}
	if st.Compitas == nil {
		st.Compitas = []Compita{}
	}
	return st, nil
}

func (s *Store) update(change func(*State) error) error {
	path, err := s.path()
	if err != nil {
		return err
	}
	return config.WithFileLock(path, func() error {
		st, err := load(path)
		if err != nil {
			return err
		}
		st.Enrollments = dropExpired(st.Enrollments, s.now())
		if err := change(&st); err != nil {
			return err
		}
		data, err := json.MarshalIndent(st, "", "  ")
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		return fileutil.WriteFileAtomic(path, data, 0o600)
	})
}

func dropExpired(list []Enrollment, now time.Time) []Enrollment {
	kept := list[:0]
	for _, e := range list {
		if now.Before(e.ExpiresAt) {
			kept = append(kept, e)
		}
	}
	return kept
}

// SetEnabled turns Compitas support on or off. Turning it on adds this
// computer as a compute, with the capabilities it has now.
func (s *Store) SetEnabled(enabled bool) error {
	return s.update(func(st *State) error {
		st.Enabled = enabled
		if enabled && indexCompute(st, LocalID) < 0 {
			caps := Capabilities{}
			if s.Detect != nil {
				caps = s.Detect()
			}
			st.Computes = append(st.Computes, Compute{
				ID:           LocalID,
				Name:         "This computer",
				Kind:         KindLocal,
				Status:       StatusEnrolled,
				Capabilities: caps,
				CreatedAt:    s.now().UTC(),
			})
		}
		return nil
	})
}

// RefreshLocal detects what this computer offers again and updates its
// compute.
func (s *Store) RefreshLocal() error {
	return s.update(func(st *State) error {
		i := indexCompute(st, LocalID)
		if i < 0 {
			return ErrNotFound
		}
		if s.Detect != nil {
			st.Computes[i].Capabilities = s.Detect()
		}
		return nil
	})
}

func indexCompute(st *State, id string) int {
	for i := range st.Computes {
		if st.Computes[i].ID == id {
			return i
		}
	}
	return -1
}

func uniqueID(base string, taken func(string) bool) string {
	id := base
	for n := 2; taken(id); n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	return id
}

// NewToken makes a random secret and its hash.
func newSecret() (secret, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	secret = base64.RawURLEncoding.EncodeToString(b)
	return secret, hashSecret(secret), nil
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func cleanURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", errors.New("compute: the URL must be an http(s) address")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// AddRemote adds a remote compute in the pending state and returns its
// one-time enrollment token. The token is shown once; only its hash is kept.
// A dial-in compute needs the URL Compa will connect to.
func (s *Store) AddRemote(name string, mode Mode, rawURL string) (Compute, string, error) {
	name, err := cleanName(name)
	if err != nil {
		return Compute{}, "", err
	}
	var addr string
	switch mode {
	case ModeDialOut:
	case ModeDialIn:
		if addr, err = cleanURL(rawURL); err != nil {
			return Compute{}, "", err
		}
	default:
		return Compute{}, "", fmt.Errorf("compute: unknown mode %q", mode)
	}
	token, hash, err := newSecret()
	if err != nil {
		return Compute{}, "", err
	}
	var added Compute
	err = s.update(func(st *State) error {
		if !st.Enabled {
			return ErrDisabled
		}
		id := uniqueID(slug(name), func(id string) bool { return indexCompute(st, id) >= 0 })
		added = Compute{
			ID: id, Name: name, Kind: KindRemote, Mode: mode, URL: addr,
			Status: StatusPending, CreatedAt: s.now().UTC(),
		}
		if mode == ModeDialIn {
			// Compa is the client here, so it must keep the secret to present it.
			now := s.now().UTC()
			added.Status, added.EnrolledAt, added.DialSecret = StatusEnrolled, &now, token
			st.Computes = append(st.Computes, added)
			return nil
		}
		st.Computes = append(st.Computes, added)
		st.Enrollments = append(st.Enrollments, Enrollment{
			ComputeID: id, TokenHash: hash, ExpiresAt: s.now().Add(TokenTTL),
		})
		return nil
	})
	if err != nil {
		return Compute{}, "", err
	}
	return added, token, nil
}

// ReissueToken replaces the enrollment token of a pending compute.
func (s *Store) ReissueToken(computeID string) (string, error) {
	token, hash, err := newSecret()
	if err != nil {
		return "", err
	}
	err = s.update(func(st *State) error {
		i := indexCompute(st, computeID)
		if i < 0 {
			return ErrNotFound
		}
		if st.Computes[i].Status != StatusPending {
			return errors.New("compute: only a pending compute can be given a new token")
		}
		kept := st.Enrollments[:0]
		for _, e := range st.Enrollments {
			if e.ComputeID != computeID {
				kept = append(kept, e)
			}
		}
		st.Enrollments = append(kept, Enrollment{
			ComputeID: computeID, TokenHash: hash, ExpiresAt: s.now().Add(TokenTTL),
		})
		return nil
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// Redeem exchanges a valid enrollment token for the compute's credential. The
// token works once: it is gone when Redeem returns. The credential is returned
// in the clear exactly once; the store keeps its hash.
func (s *Store) Redeem(token string, caps Capabilities) (computeID, credential string, err error) {
	hash := hashSecret(strings.TrimSpace(token))
	credential, credHash, err := newSecret()
	if err != nil {
		return "", "", err
	}
	err = s.update(func(st *State) error {
		match := -1
		for i, e := range st.Enrollments {
			if subtle.ConstantTimeCompare([]byte(e.TokenHash), []byte(hash)) == 1 {
				match = i
			}
		}
		if match < 0 {
			return ErrInvalidToken
		}
		id := st.Enrollments[match].ComputeID
		st.Enrollments = append(st.Enrollments[:match], st.Enrollments[match+1:]...)
		i := indexCompute(st, id)
		if i < 0 {
			return ErrInvalidToken
		}
		at := s.now().UTC()
		st.Computes[i].Status = StatusEnrolled
		st.Computes[i].EnrolledAt = &at
		st.Computes[i].CredentialHash = credHash
		st.Computes[i].Capabilities = caps
		computeID = id
		return nil
	})
	if err != nil {
		return "", "", err
	}
	return computeID, credential, nil
}

// ValidEnrollment reports whether token is an enrollment token that has not
// been used or expired. It does not use the token up.
func (s *Store) ValidEnrollment(token string) bool {
	st, err := s.Load()
	if err != nil || strings.TrimSpace(token) == "" {
		return false
	}
	hash := hashSecret(strings.TrimSpace(token))
	for _, e := range st.Enrollments {
		if subtle.ConstantTimeCompare([]byte(e.TokenHash), []byte(hash)) == 1 && s.now().Before(e.ExpiresAt) {
			return true
		}
	}
	return false
}

// Authenticate reports whether credential belongs to the enrolled compute.
func (s *Store) Authenticate(computeID, credential string) bool {
	st, err := s.Load()
	if err != nil {
		return false
	}
	i := indexCompute(&st, computeID)
	if i < 0 || st.Computes[i].Status != StatusEnrolled || st.Computes[i].CredentialHash == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(st.Computes[i].CredentialHash), []byte(hashSecret(credential))) == 1
}

// AuthenticateDial reports whether secret is the dial secret of an enrolled
// dial-in compute.
func (s *Store) AuthenticateDial(computeID, secret string) bool {
	st, err := s.Load()
	if err != nil || secret == "" {
		return false
	}
	i := indexCompute(&st, computeID)
	return i >= 0 && st.Computes[i].Mode == ModeDialIn && st.Computes[i].Status == StatusEnrolled &&
		subtle.ConstantTimeCompare([]byte(st.Computes[i].DialSecret), []byte(secret)) == 1
}

// CheckBrowser reports whether a Compita with isolation level i on the compute
// can have a browser, so a request that cannot be met is refused before
// anything is created.
func (s *Store) CheckBrowser(computeID string, i Isolation) error {
	st, err := s.Load()
	if err != nil {
		return err
	}
	ci := indexCompute(&st, computeID)
	if ci < 0 {
		return ErrNotFound
	}
	return st.Computes[ci].AllowsBrowser(i)
}

// SetCompitaBrowser gives a Compita a browser, or takes it away, if its
// compute can provide one (see Compute.AllowsBrowser).
func (s *Store) SetCompitaBrowser(id string, on bool) error {
	return s.update(func(st *State) error {
		for i := range st.Compitas {
			if st.Compitas[i].ID == id {
				if on {
					ci := indexCompute(st, st.Compitas[i].ComputeID)
					if ci < 0 {
						return ErrNotFound
					}
					if err := st.Computes[ci].AllowsBrowser(st.Compitas[i].Isolation); err != nil {
						return err
					}
				}
				st.Compitas[i].Browser = on
				return nil
			}
		}
		return ErrNotFound
	})
}

// SetCompitaApprovals sets how much a Compita asks the owner before acting.
func (s *Store) SetCompitaApprovals(id, preset string) error {
	if !ValidApprovals(preset) {
		return fmt.Errorf("compute: approvals must be %q or %q", ApprovalsCareful, ApprovalsOpen)
	}
	return s.update(func(st *State) error {
		for i := range st.Compitas {
			if st.Compitas[i].ID == id {
				st.Compitas[i].Approvals = preset
				return nil
			}
		}
		return ErrNotFound
	})
}

// RemoveCompute deletes a compute that no Compita uses. This computer cannot
// be removed; turn Compitas support off instead.
func (s *Store) RemoveCompute(id string) error {
	return s.update(func(st *State) error {
		i := indexCompute(st, id)
		if i < 0 {
			return ErrNotFound
		}
		if st.Computes[i].Kind == KindLocal {
			return errors.New("compute: this computer cannot be removed")
		}
		for _, c := range st.Compitas {
			if c.ComputeID == id {
				return ErrInUse
			}
		}
		st.Computes = append(st.Computes[:i], st.Computes[i+1:]...)
		kept := st.Enrollments[:0]
		for _, e := range st.Enrollments {
			if e.ComputeID != id {
				kept = append(kept, e)
			}
		}
		st.Enrollments = kept
		return nil
	})
}

// AddCompita places a new Compita on a compute at an isolation level the
// compute can give.
func (s *Store) AddCompita(name, description, computeID string, iso Isolation) (Compita, error) {
	name, err := cleanName(name)
	if err != nil {
		return Compita{}, err
	}
	if !ValidIsolation(iso) {
		return Compita{}, fmt.Errorf("compute: unknown isolation level %q", iso)
	}
	var added Compita
	err = s.update(func(st *State) error {
		if !st.Enabled {
			return ErrDisabled
		}
		i := indexCompute(st, computeID)
		if i < 0 {
			return ErrNotFound
		}
		if st.Computes[i].Status != StatusEnrolled {
			return errors.New("compute: the compute has not enrolled yet")
		}
		if err := st.Computes[i].Allows(iso); err != nil {
			return err
		}
		id := uniqueID(slug(name), func(id string) bool {
			for _, c := range st.Compitas {
				if c.ID == id {
					return true
				}
			}
			return false
		})
		added = Compita{
			ID: id, Name: name, Description: strings.TrimSpace(description),
			ComputeID: computeID, Isolation: iso, CreatedAt: s.now().UTC(),
		}
		st.Compitas = append(st.Compitas, added)
		return nil
	})
	return added, err
}

// RemoveCompita deletes a Compita.
func (s *Store) RemoveCompita(id string) error {
	return s.update(func(st *State) error {
		for i, c := range st.Compitas {
			if c.ID == id {
				st.Compitas = append(st.Compitas[:i], st.Compitas[i+1:]...)
				return nil
			}
		}
		return ErrNotFound
	})
}

// SetCapabilities records what a compute reported about itself.
// SetPin pins the certificate a dial-in compute must present.
func (s *Store) SetPin(computeID, pin string) error {
	pin = normalizePin(pin)
	if pin != "" && len(pin) != 64 {
		return errors.New("compute: pin must be a SHA-256 fingerprint (64 hex characters)")
	}
	return s.update(func(st *State) error {
		i := indexCompute(st, computeID)
		if i < 0 {
			return ErrNotFound
		}
		st.Computes[i].PinSHA256 = pin
		return nil
	})
}

func (s *Store) SetCapabilities(computeID string, caps Capabilities) error {
	return s.update(func(st *State) error {
		i := indexCompute(st, computeID)
		if i < 0 {
			return ErrNotFound
		}
		st.Computes[i].Capabilities = caps
		return nil
	})
}
