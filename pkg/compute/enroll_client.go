package compute

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kayiik/compa/pkg/fileutil"
)

// EnrollPath is the Compa endpoint a compute redeems its token at.
const EnrollPath = "/api/compute/enroll"

// IdentityFileName is where an enrolled compute keeps who it belongs to.
const IdentityFileName = "compute-identity.json"

// EnrollRequest is what a compute sends to redeem its token.
type EnrollRequest struct {
	Token        string       `json:"token"`
	Capabilities Capabilities `json:"capabilities"`
}

// EnrollResponse is what Compa answers with. Credential is shown once.
type EnrollResponse struct {
	ComputeID  string `json:"compute_id"`
	Credential string `json:"credential"`
}

// Identity is the compute's own record of the Compa it enrolled with.
type Identity struct {
	CompaURL   string `json:"compa_url"`
	ComputeID  string `json:"compute_id"`
	Credential string `json:"credential"`
}

// Enroll redeems token at the Compa at baseURL, probing local capabilities
// first, and returns the identity to keep. It does not store it: see
// SaveIdentity.
func Enroll(ctx context.Context, client *http.Client, baseURL, token string, caps Capabilities) (Identity, error) {
	base, err := cleanURL(baseURL)
	if err != nil {
		return Identity{}, err
	}
	if strings.TrimSpace(token) == "" {
		return Identity{}, errors.New("compute: an enrollment token is required")
	}
	body, err := json.Marshal(EnrollRequest{Token: strings.TrimSpace(token), Capabilities: caps})
	if err != nil {
		return Identity{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+EnrollPath, bytes.NewReader(body))
	if err != nil {
		return Identity{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Identity{}, fmt.Errorf("compute: reach %s: %w", base, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return Identity{}, fmt.Errorf("compute: enrollment refused: %s", e.Error)
		}
		return Identity{}, fmt.Errorf("compute: enrollment failed with status %d", resp.StatusCode)
	}
	var out EnrollResponse
	if err := json.Unmarshal(raw, &out); err != nil || out.ComputeID == "" || out.Credential == "" {
		return Identity{}, errors.New("compute: enrollment answer is not valid")
	}
	return Identity{CompaURL: base, ComputeID: out.ComputeID, Credential: out.Credential}, nil
}

// SaveIdentity writes the identity to home, readable by its owner only.
func SaveIdentity(home string, id Identity) error {
	data, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(filepath.Join(home, IdentityFileName), data, 0o600)
}

// LoadIdentity reads the identity saved by SaveIdentity. A compute that has
// not enrolled returns os.ErrNotExist.
func LoadIdentity(home string) (Identity, error) {
	data, err := os.ReadFile(filepath.Join(home, IdentityFileName))
	if err != nil {
		return Identity{}, err
	}
	var id Identity
	if err := json.Unmarshal(data, &id); err != nil {
		return Identity{}, fmt.Errorf("compute: parse %s: %w", IdentityFileName, err)
	}
	return id, nil
}
