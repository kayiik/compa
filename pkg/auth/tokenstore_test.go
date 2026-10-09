package auth

import (
	"path/filepath"
	"testing"

	"github.com/kayiik/llm-provider-auth/tokenstore"
	"github.com/kayiik/llm-provider-auth/tokenstore/storetest"
)

func TestTokenStoreConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) storetest.Opener {
		path := filepath.Join(t.TempDir(), "auth.json")
		return func(t *testing.T) tokenstore.Store { return OpenTokenStore(path) }
	})
}
