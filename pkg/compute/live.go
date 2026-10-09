package compute

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Where a Compita's browser can be watched. The live view is the RFB stream of
// the browser's virtual display; Compa's own noVNC client shows it, so a
// machine never serves a page to the dashboard. The machine only carries bytes
// to the display's WebSocket endpoint (websockify), through a tunnel:
//
//   - the local computer is reached directly;
//   - a dial-in compute offers LiveTunnelPath, which Compa dials;
//   - a dial-out compute connects back to LiveConnectPath when Compa asks (a
//     control job), as it cannot take connections.
const (
	// LiveTunnelPath is where a dial-in compute offers the tunnel.
	LiveTunnelPath = "/live/tunnel"
	// LiveConnectPath is where a dial-out compute connects back; the tunnel's
	// session follows.
	LiveConnectPath = "/api/compute/live/"
	// DefaultLiveAddr is where the browser image serves the live view when the
	// compute itself runs inside that image.
	DefaultLiveAddr = "127.0.0.1:6080"
)

// ErrNoLiveView means no browser display of the Compita is up on this machine.
var ErrNoLiveView = errors.New("compute: the Compita's browser is not running on this machine right now")

// tunnelWait is how long Compa waits for a dial-out compute to connect back.
const tunnelWait = 20 * time.Second

var compitaIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,127}$`)

// LiveAddr says where the live view of the Compita's browser listens on this
// machine, if it is up. A browser in a container is published on a free
// loopback port while its turn runs. Otherwise the browser is the machine's own,
// and a compute that runs inside the browser image has the live view at
// COMPA_LIVE_ADDR, by default DefaultLiveAddr. The two never mix: a Compita in
// a container must not be shown whatever else listens on the default address.
func LiveAddr(id string, iso Isolation) (string, bool) {
	if !compitaIDPattern.MatchString(id) {
		return "", false
	}
	if iso == IsolationContainer {
		return publishedLiveAddr(id)
	}
	addr := os.Getenv("COMPA_LIVE_ADDR")
	if addr == "" {
		addr = DefaultLiveAddr
	}
	conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err != nil {
		return "", false
	}
	_ = conn.Close()
	return addr, true
}

// publishedLiveAddr finds the loopback port a running browser container of the
// Compita publishes its live view on.
func publishedLiveAddr(id string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "ps", "-q", "--filter", "label="+compitaLabel+"="+id).Output()
	if err != nil {
		return "", false
	}
	cid := strings.Fields(string(out))
	if len(cid) == 0 {
		return "", false
	}
	out, err = exec.CommandContext(ctx, "docker", "port", cid[0], NoVNCPort+"/tcp").Output()
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if host, ok := strings.CutPrefix(strings.TrimSpace(line), "127.0.0.1:"); ok && host != "" {
			return "127.0.0.1:" + host, true
		}
	}
	return "", false
}

// dialLive opens a connection to the live view of the Compita's browser on
// this machine.
func dialLive(compitaID string, iso Isolation) (net.Conn, error) {
	addr, ok := LiveAddr(compitaID, iso)
	if !ok {
		return nil, ErrNoLiveView
	}
	return net.DialTimeout("tcp", addr, 5*time.Second)
}

// relay copies between a and b until either ends, then closes both.
func relay(a, b io.ReadWriteCloser) {
	done := make(chan struct{}, 2)
	pipe := func(dst io.Writer, src io.Reader) {
		_, _ = io.Copy(dst, src)
		done <- struct{}{}
	}
	go pipe(a, b)
	go pipe(b, a)
	<-done
	_ = a.Close()
	_ = b.Close()
	<-done
}

// wsURL turns an http(s) base URL into the ws(s) URL of path on it.
func wsURL(base, path string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https", "wss":
		u.Scheme = "wss"
	default:
		u.Scheme = "ws"
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	return u.String(), nil
}

// DialTunnel opens a tunnel to the live view of a Compita's browser on a
// dial-in compute. The Compita's isolation tells the machine where to look.
func DialTunnel(ctx context.Context, c Compute, compitaID string, iso Isolation) (net.Conn, error) {
	if c.Mode != ModeDialIn || c.DialSecret == "" {
		return nil, errors.New("compute: not a dial-in compute")
	}
	target, err := wsURL(c.URL, LiveTunnelPath)
	if err != nil {
		return nil, err
	}
	d := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	if c.PinSHA256 != "" {
		d.TLSClientConfig = pinnedTLS(c.PinSHA256)
	}
	query := "?compita=" + url.QueryEscape(compitaID) + "&isolation=" + url.QueryEscape(string(iso))
	ws, _, err := d.DialContext(ctx, target+query, http.Header{"Authorization": {"Bearer " + c.DialSecret}})
	if err != nil {
		return nil, err
	}
	return newWSConn(ws), nil
}

// connectBack is how a dial-out compute answers a control job: it opens a
// tunnel to Compa for session and serves it to the Compita's live view.
func connectBack(ctx context.Context, id Identity, session, compitaID string, iso Isolation, logf func(string, ...any)) {
	target, err := wsURL(id.CompaURL, LiveConnectPath+session)
	if err != nil {
		logf("live view: %v", err)
		return
	}
	d := websocket.Dialer{HandshakeTimeout: 15 * time.Second, Proxy: http.ProxyFromEnvironment}
	ws, _, err := d.DialContext(ctx, target, http.Header{
		"X-Compute-Id":  {id.ComputeID},
		"Authorization": {"Bearer " + id.Credential},
	})
	if err != nil {
		logf("live view: connect back failed: %v", err)
		return
	}
	up, err := dialLive(compitaID, iso)
	if err != nil {
		// Closing at once tells Compa now, rather than after it gives up waiting.
		_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, err.Error()), time.Now().Add(5*time.Second))
		_ = ws.Close()
		logf("live view of %s: %v", compitaID, err)
		return
	}
	relay(newWSConn(ws), up)
}

var tunnelUpgrader = websocket.Upgrader{ReadBufferSize: 32 << 10, WriteBufferSize: 32 << 10}

// wsConn makes a WebSocket a net.Conn: what is written leaves as binary
// messages and what is read is the bytes of the messages that arrive. Pings
// keep it alive through proxies that drop quiet connections.
type wsConn struct {
	ws     *websocket.Conn
	r      io.Reader
	closed chan struct{}
	once   sync.Once
}

func newWSConn(ws *websocket.Conn) *wsConn {
	ws.SetReadLimit(1 << 20)
	c := &wsConn{ws: ws, closed: make(chan struct{})}
	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-c.closed:
				return
			case <-t.C:
				if ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)) != nil {
					return
				}
			}
		}
	}()
	return c
}

func (c *wsConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		if c.r == nil {
			_, r, err := c.ws.NextReader()
			if err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					return 0, io.EOF
				}
				return 0, err
			}
			c.r = r
		}
		n, err := c.r.Read(p)
		if err == io.EOF {
			c.r = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}

func (c *wsConn) Write(p []byte) (int, error) {
	if err := c.ws.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *wsConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.ws.Close()
}

func (c *wsConn) LocalAddr() net.Addr  { return c.ws.LocalAddr() }
func (c *wsConn) RemoteAddr() net.Addr { return c.ws.RemoteAddr() }

func (c *wsConn) SetDeadline(t time.Time) error {
	if err := c.ws.SetReadDeadline(t); err != nil {
		return err
	}
	return c.ws.SetWriteDeadline(t)
}
func (c *wsConn) SetReadDeadline(t time.Time) error  { return c.ws.SetReadDeadline(t) }
func (c *wsConn) SetWriteDeadline(t time.Time) error { return c.ws.SetWriteDeadline(t) }
