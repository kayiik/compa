package compute

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Paths a dial-in compute serves. Compa calls them with the dial secret.
const (
	InfoPath = "/info"
	RunPath  = "/run"
)

// DialInHandler is what `compute serve --listen` exposes: Compa connects to
// it, so the machine needs a reachable address but never reaches out.
func DialInHandler(secret string, runner Runner) http.Handler {
	authed := func(r *http.Request) bool {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		return subtle.ConstantTimeCompare([]byte(got), []byte(secret)) == 1
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+InfoPath, func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(Detect())
	})
	mux.HandleFunc("GET "+LiveTunnelPath, func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		id, iso := r.URL.Query().Get("compita"), Isolation(r.URL.Query().Get("isolation"))
		if !compitaIDPattern.MatchString(id) || !ValidIsolation(iso) {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		up, err := dialLive(id, iso)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		ws, err := tunnelUpgrader.Upgrade(w, r, nil)
		if err != nil {
			_ = up.Close()
			return // the upgrader has answered
		}
		relay(newWSConn(ws), up)
	})
	mux.HandleFunc("POST "+RunPath, func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var t Turn
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&t); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), jobTimeout)
		defer cancel()
		rn := runner // a copy: requests run side by side
		if t.PeerURL != "" {
			rn.Peer = &Peer{URL: t.PeerURL, ComputeID: t.PeerCompute, Credential: t.PeerToken}
		}
		t.PeerToken = ""
		if t.Events {
			// One JSON object per line: the turn's events as they happen, then
			// the result.
			w.Header().Set("Content-Type", "application/x-ndjson")
			var mu sync.Mutex
			enc := json.NewEncoder(w)
			flusher, _ := w.(http.Flusher)
			send := func(v any) {
				mu.Lock()
				defer mu.Unlock()
				_ = enc.Encode(v)
				if flusher != nil {
					flusher.Flush()
				}
			}
			reply, err := rn.RunStream(ctx, t, func(e StreamEvent) { send(e) })
			line := resultLine{Type: "result", Result: Result{Reply: reply}}
			if err != nil {
				line.Error = err.Error()
			}
			send(line)
			return
		}
		reply, err := rn.Run(ctx, t)
		res := Result{Reply: reply}
		if err != nil {
			res.Error = err.Error()
		}
		_ = json.NewEncoder(w).Encode(res)
	})
	return mux
}

// resultLine is the last line of a streamed dial-in run.
type resultLine struct {
	Type string `json:"type"`
	Result
}

func client(c Compute) *http.Client {
	cl := &http.Client{Timeout: jobTimeout + time.Minute}
	if c.PinSHA256 != "" {
		cl.Transport = &http.Transport{TLSClientConfig: pinnedTLS(c.PinSHA256)}
	}
	return cl
}

func dialRequest(ctx context.Context, c Compute, method, path string, body any) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.URL, "/")+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.DialSecret)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client(c).Do(req)
	if err != nil {
		return nil, fmt.Errorf("compute: reach %s: %w", c.URL, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("compute: %s answered %d", c.URL, resp.StatusCode)
	}
	return raw, nil
}

// DialRun asks a dial-in compute to run a turn. With onEvent it also hears what
// the turn does as it happens.
func DialRun(ctx context.Context, c Compute, t Turn, onEvent func(StreamEvent)) (Result, error) {
	if c.Mode != ModeDialIn || c.DialSecret == "" {
		return Result{}, errors.New("compute: not a dial-in compute")
	}
	if onEvent == nil {
		raw, err := dialRequest(ctx, c, http.MethodPost, RunPath, t)
		if err != nil {
			return Result{}, err
		}
		var res Result
		return res, json.Unmarshal(raw, &res)
	}
	t.Events = true
	b, _ := json.Marshal(t)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.URL, "/")+RunPath, bytes.NewReader(b))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.DialSecret)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client(c).Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("compute: reach %s: %w", c.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("compute: %s answered %d", c.URL, resp.StatusCode)
	}
	var res Result
	got := false
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
	for sc.Scan() {
		var line struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		switch line.Type {
		case "result", "": // "" is a server that answered whole, with a bare Result
			if err := json.Unmarshal(sc.Bytes(), &res); err != nil {
				return Result{}, err
			}
			got = true
		default:
			var ev StreamEvent
			if json.Unmarshal(sc.Bytes(), &ev) == nil {
				onEvent(ev)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return Result{}, err
	}
	if !got {
		return Result{}, errors.New("compute: the compute ended the turn without a result")
	}
	return res, nil
}

// DialInfo asks a dial-in compute what it can do.
func DialInfo(ctx context.Context, c Compute) (Capabilities, error) {
	raw, err := dialRequest(ctx, c, http.MethodGet, InfoPath, nil)
	if err != nil {
		return Capabilities{}, err
	}
	var caps Capabilities
	return caps, json.Unmarshal(raw, &caps)
}
