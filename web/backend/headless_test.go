package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type fakePasswordStore struct{ password string }

func (s *fakePasswordStore) IsInitialized(context.Context) (bool, error) {
	return s.password != "", nil
}
func (s *fakePasswordStore) InitializePassword(_ context.Context, plain string) (bool, error) {
	if s.password != "" {
		return false, nil
	}
	s.password = plain
	return true, nil
}
func (s *fakePasswordStore) SetPassword(_ context.Context, plain string) error {
	s.password = plain
	return nil
}
func (s *fakePasswordStore) VerifyPassword(_ context.Context, plain string) (bool, error) {
	return plain == s.password, nil
}

func writePasswordFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInitialPasswordFromFile(t *testing.T) {
	ctx := context.Background()

	t.Run("sets the first password, trimmed", func(t *testing.T) {
		store := &fakePasswordStore{}
		set, err := initialPasswordFromFile(ctx, store, writePasswordFile(t, "  long-enough-secret\n"))
		if err != nil || !set || store.password != "long-enough-secret" {
			t.Fatalf("set=%v err=%v password=%q", set, err, store.password)
		}
	})
	t.Run("never replaces a password set already", func(t *testing.T) {
		store := &fakePasswordStore{password: "chosen-in-the-browser"}
		set, err := initialPasswordFromFile(ctx, store, writePasswordFile(t, "from-the-file-123"))
		if err != nil || set || store.password != "chosen-in-the-browser" {
			t.Fatalf("set=%v err=%v password=%q", set, err, store.password)
		}
	})
	t.Run("only the first line is the password", func(t *testing.T) {
		store := &fakePasswordStore{}
		if _, err := initialPasswordFromFile(ctx, store, writePasswordFile(t, "first-line-secret\r\nsecond line\n")); err != nil || store.password != "first-line-secret" {
			t.Fatalf("err=%v password=%q", err, store.password)
		}
	})
	t.Run("a file named with no store open is an error", func(t *testing.T) {
		if set, err := initialPasswordFromFile(ctx, nil, writePasswordFile(t, "long-enough-secret")); err == nil || set {
			t.Fatalf("set=%v err=%v", set, err)
		}
	})
	t.Run("no file named, nothing to do", func(t *testing.T) {
		store := &fakePasswordStore{}
		if set, err := initialPasswordFromFile(ctx, store, " "); err != nil || set {
			t.Fatalf("set=%v err=%v", set, err)
		}
	})
	t.Run("a missing file or a short password is an error", func(t *testing.T) {
		for _, path := range []string{filepath.Join(t.TempDir(), "missing"), writePasswordFile(t, "short")} {
			store := &fakePasswordStore{}
			if set, err := initialPasswordFromFile(ctx, store, path); err == nil || set || store.password != "" {
				t.Fatalf("%s: set=%v err=%v password=%q", path, set, err, store.password)
			}
		}
	})
}

func TestAllowedHostsJoinsSavedFlagAndEnvironment(t *testing.T) {
	got := allowedHosts([]string{"proxy.example"}, "Compa.Example, 10.0.0.5:18800", "", "compa.example,https://lan.host/compa")
	want := []string{"proxy.example", "compa.example", "10.0.0.5", "lan.host"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("allowedHosts = %v, want %v", got, want)
	}
	if got := allowedHosts(nil, "", ""); len(got) != 0 {
		t.Fatalf("no hosts = %v", got)
	}
}
