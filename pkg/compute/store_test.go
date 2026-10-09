package compute

import (
	"errors"
	"testing"
	"time"
)

func testStore(t *testing.T, docker bool) *Store {
	t.Helper()
	s := NewStore(t.TempDir())
	s.Detect = func() Capabilities { return Capabilities{Docker: docker} }
	return s
}

func TestDisabledByDefaultAndGatesChanges(t *testing.T) {
	s := testStore(t, true)
	st, err := s.Load()
	if err != nil || st.Enabled || len(st.Computes) != 0 {
		t.Fatalf("fresh state = %+v, %v", st, err)
	}
	if _, _, err := s.AddRemote("vps", ModeDialOut, ""); !errors.Is(err, ErrDisabled) {
		t.Fatalf("AddRemote while disabled: %v", err)
	}
	if _, err := s.AddCompita("carlos", "", LocalID, IsolationShared); !errors.Is(err, ErrDisabled) {
		t.Fatalf("AddCompita while disabled: %v", err)
	}
}

func TestEnableAddsThisComputerOnce(t *testing.T) {
	s := testStore(t, true)
	for range 2 {
		if err := s.SetEnabled(true); err != nil {
			t.Fatal(err)
		}
	}
	st, _ := s.Load()
	if len(st.Computes) != 1 || st.Computes[0].ID != LocalID || !st.Computes[0].Capabilities.Docker {
		t.Fatalf("computes = %+v", st.Computes)
	}
}

func TestIsolationRules(t *testing.T) {
	noDocker := testStore(t, false)
	if err := noDocker.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if _, err := noDocker.AddCompita("a", "", LocalID, IsolationContainer); err == nil {
		t.Fatal("container isolation accepted without Docker")
	}
	if _, err := noDocker.AddCompita("a", "", LocalID, IsolationMachine); err == nil {
		t.Fatal("whole-machine isolation accepted on the local computer")
	}
	if _, err := noDocker.AddCompita("a", "", LocalID, Isolation("vm")); err == nil {
		t.Fatal("unknown isolation accepted")
	}
	c, err := noDocker.AddCompita("Carlos Scraper", "tracks prices", LocalID, IsolationProfile)
	if err != nil || c.ID != "carlos-scraper" {
		t.Fatalf("AddCompita = %+v, %v", c, err)
	}
	c2, _ := noDocker.AddCompita("Carlos Scraper", "", LocalID, IsolationShared)
	if c2.ID != "carlos-scraper-2" {
		t.Fatalf("duplicate name id = %q", c2.ID)
	}
}

func TestEnrollmentIsSingleUseAndGrantsCredential(t *testing.T) {
	s := testStore(t, false)
	_ = s.SetEnabled(true)
	c, token, err := s.AddRemote("My VPS", ModeDialOut, "")
	if err != nil || c.Status != StatusPending || token == "" {
		t.Fatalf("AddRemote = %+v %q %v", c, token, err)
	}
	if _, err := s.AddCompita("x", "", c.ID, IsolationShared); err == nil {
		t.Fatal("Compita placed on a compute that has not enrolled")
	}
	id, cred, err := s.Redeem(token, Capabilities{Docker: true, DockerVersion: "27"})
	if err != nil || id != c.ID || cred == "" {
		t.Fatalf("Redeem = %q %q %v", id, cred, err)
	}
	if _, _, err := s.Redeem(token, Capabilities{}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("second Redeem: %v", err)
	}
	if !s.Authenticate(id, cred) || s.Authenticate(id, cred+"x") || s.Authenticate("nope", cred) {
		t.Fatal("Authenticate wrong")
	}
	st, _ := s.Load()
	got := st.Computes[1]
	if got.Status != StatusEnrolled || !got.Capabilities.Docker || got.EnrolledAt == nil {
		t.Fatalf("compute after enroll = %+v", got)
	}
	if got.CredentialHash == cred {
		t.Fatal("credential stored in the clear")
	}
	if _, err := s.AddCompita("x", "", id, IsolationContainer); err != nil {
		t.Fatalf("container Compita on enrolled Docker compute: %v", err)
	}
}

func TestTokenExpires(t *testing.T) {
	s := testStore(t, false)
	_ = s.SetEnabled(true)
	base := time.Now()
	s.now = func() time.Time { return base }
	_, token, _ := s.AddRemote("vps", ModeDialOut, "")
	s.now = func() time.Time { return base.Add(TokenTTL + time.Second) }
	if _, _, err := s.Redeem(token, Capabilities{}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expired token: %v", err)
	}
}

func TestReissueReplacesToken(t *testing.T) {
	s := testStore(t, false)
	_ = s.SetEnabled(true)
	c, old, _ := s.AddRemote("vps", ModeDialOut, "")
	fresh, err := s.ReissueToken(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Redeem(old, Capabilities{}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("old token still works: %v", err)
	}
	if _, _, err := s.Redeem(fresh, Capabilities{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReissueToken(c.ID); err == nil {
		t.Fatal("enrolled compute accepted a new token")
	}
}

func TestDialInNeedsURL(t *testing.T) {
	s := testStore(t, false)
	_ = s.SetEnabled(true)
	if _, _, err := s.AddRemote("vps", ModeDialIn, "ftp://x"); err == nil {
		t.Fatal("bad dial-in URL accepted")
	}
	c, _, err := s.AddRemote("vps", ModeDialIn, "https://vps.example.com/")
	if err != nil || c.URL != "https://vps.example.com" {
		t.Fatalf("AddRemote = %+v, %v", c, err)
	}
	if _, _, err := s.AddRemote("vps", Mode("x"), ""); err == nil {
		t.Fatal("unknown mode accepted")
	}
}

func TestRemoveRules(t *testing.T) {
	s := testStore(t, false)
	_ = s.SetEnabled(true)
	if err := s.RemoveCompute(LocalID); err == nil {
		t.Fatal("this computer was removed")
	}
	c, token, _ := s.AddRemote("vps", ModeDialOut, "")
	_, _, _ = s.Redeem(token, Capabilities{})
	cp, _ := s.AddCompita("bot", "", c.ID, IsolationShared)
	if err := s.RemoveCompute(c.ID); !errors.Is(err, ErrInUse) {
		t.Fatalf("remove compute in use: %v", err)
	}
	if err := s.RemoveCompita(cp.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveCompute(c.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveCompute(c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second remove: %v", err)
	}
}
