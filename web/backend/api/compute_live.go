package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"time"

	"github.com/gorilla/websocket"

	"github.com/kayiik/compa/pkg/compute"
	"github.com/kayiik/compa/pkg/logger"
)

func (h *Handler) registerLiveRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/compitas/{id}/live/ws", h.handleCompitaLive)
	mux.HandleFunc("GET "+compute.LiveConnectPath+"{session}", h.handleComputeLiveConnect)
}

// liveDialer says how to reach the live view of the Compita's browser, wherever
// its compute is: directly on this computer, through the tunnel a dial-in
// compute offers, or through the one a dial-out compute opens when asked.
func liveDialer(p compute.Compita, c compute.Compute) func(context.Context) (net.Conn, error) {
	switch {
	case c.Kind == compute.KindLocal:
		return func(ctx context.Context) (net.Conn, error) {
			addr, ok := compute.LiveAddr(p.ID, p.Isolation)
			if !ok {
				return nil, compute.ErrNoLiveView
			}
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", addr)
		}
	case c.Mode == compute.ModeDialOut:
		return func(ctx context.Context) (net.Conn, error) {
			return computeBroker.OpenTunnel(ctx, c.ID, p.ID, p.Isolation)
		}
	default:
		return func(ctx context.Context) (net.Conn, error) {
			return compute.DialTunnel(ctx, c, p.ID, p.Isolation)
		}
	}
}

// handleCompitaLive carries the live view of a Compita's browser to the
// dashboard's own noVNC client. Only the WebSocket is carried: the machine that
// runs the browser could be compromised, and a page it served would run with
// the dashboard's rights. So the request is rewritten to the one WebSocket
// endpoint of the display, and anything but an upgrade is refused.
func (h *Handler) handleCompitaLive(w http.ResponseWriter, r *http.Request) {
	p, c, err := h.findCompita(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, computeErrorStatus(err), err.Error())
		return
	}
	if !p.Browser {
		writeJSONError(w, http.StatusNotFound, "this Compita has no browser")
		return
	}
	if !websocket.IsWebSocketUpgrade(r) {
		writeJSONError(w, http.StatusBadRequest, "the live view is a WebSocket")
		return
	}
	dial := liveDialer(p, c)
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) },
	}
	defer transport.CloseIdleConnections()
	proxy := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme, pr.Out.URL.Host = "http", "live"
			pr.Out.URL.Path, pr.Out.URL.RawPath, pr.Out.URL.RawQuery = "/websockify", "", ""
			pr.Out.Host = "live"
			// What the browser sent for the dashboard is not for the machine.
			stripBrowserCredentials(pr.Out.Header)
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			if errors.Is(err, context.Canceled) {
				return
			}
			logger.Debugf("Compita live view of %s: %v", p.ID, err)
			writeJSONError(w, http.StatusBadGateway, "the Compita's browser is not reachable right now")
		},
	}
	proxy.ServeHTTP(w, r)
}

// handleComputeLiveConnect is where a dial-out compute connects back with the
// tunnel Compa asked it for.
func (h *Handler) handleComputeLiveConnect(w http.ResponseWriter, r *http.Request) {
	cid, ok := h.authCompute(r)
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "invalid compute credential")
		return
	}
	computeBroker.HandleTunnel(w, r, cid, r.PathValue("session"))
}
