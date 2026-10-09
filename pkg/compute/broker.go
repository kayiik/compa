package compute

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"
)

// Job is one message for a Compita, handed to the compute that hosts it.
type Job struct {
	ID        string    `json:"id"`
	CompitaID string    `json:"compita_id"`
	Isolation Isolation `json:"isolation"`
	Session   string    `json:"session"`
	Message   string    `json:"message"`
	Depth     int       `json:"depth,omitempty"`
	Approvals string    `json:"approvals,omitempty"`
	Browser   bool      `json:"browser,omitempty"`
	// Connections are the Compita's own MCP servers.
	Connections map[string]Connection `json:"connections,omitempty"`
	// Events asks the compute to report the turn as it runs.
	Events bool `json:"events,omitempty"`
	// Tunnel, when set, makes this a control job rather than a turn: open a
	// tunnel to the host for this session (see Tunnel in tunnel.go).
	Tunnel string `json:"tunnel,omitempty"`
}

// Result is a compute's answer to a Job.
type Result struct {
	JobID string `json:"job_id"`
	Reply string `json:"reply"`
	Error string `json:"error,omitempty"`
}

// ErrOffline means no compute picked the job up in time.
var ErrOffline = errors.New("compute: the compute is offline or busy")

// onlineWindow is how long after its last poll a compute counts as online.
const onlineWindow = 45 * time.Second

// Broker hands jobs to dial-out computes. They poll for work, so the compute
// never needs an open port.
type Broker struct {
	mu       sync.Mutex
	queues   map[string]chan Job
	waiting  map[string]chan Result
	lastSeen map[string]time.Time
	// sinks receive the events of jobs that stream; owner says which compute
	// runs each job, so another cannot report for it or answer for it.
	sinks map[string]func(StreamEvent)
	owner map[string]string
	// tunnels are the live-view tunnels Compa waits for a compute to open.
	tunnels map[string]tunnelWaiter
}

type tunnelWaiter struct {
	computeID string
	conn      chan net.Conn
}

func NewBroker() *Broker {
	return &Broker{
		queues: map[string]chan Job{}, waiting: map[string]chan Result{}, lastSeen: map[string]time.Time{},
		sinks: map[string]func(StreamEvent){}, owner: map[string]string{}, tunnels: map[string]tunnelWaiter{},
	}
}

// OpenTunnel asks a dial-out compute to connect back with a tunnel to the live
// view of the Compita's browser, and waits for it.
func (b *Broker) OpenTunnel(ctx context.Context, computeID, compitaID string, iso Isolation) (net.Conn, error) {
	session := randomToken(18)
	w := tunnelWaiter{computeID: computeID, conn: make(chan net.Conn, 1)}
	b.mu.Lock()
	b.tunnels[session] = w
	b.mu.Unlock()
	defer func() {
		// Taking the entry out under the lock means nothing is sent after
		// this, so whatever arrived late can be closed.
		b.mu.Lock()
		delete(b.tunnels, session)
		b.mu.Unlock()
		select {
		case c := <-w.conn:
			_ = c.Close()
		default:
		}
	}()
	if err := b.Enqueue(computeID, Job{CompitaID: compitaID, Isolation: iso, Tunnel: session}); err != nil {
		return nil, err
	}
	t := time.NewTimer(tunnelWait)
	defer t.Stop()
	select {
	case c := <-w.conn:
		return c, nil
	case <-t.C:
		return nil, errors.New("compute: the compute did not open its live view in time")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// HandleTunnel serves the request a compute makes to connect back for a tunnel
// it was asked for: it upgrades the request to a WebSocket and hands that to
// whoever waits for session. The caller has authenticated computeID.
func (b *Broker) HandleTunnel(w http.ResponseWriter, r *http.Request, computeID, session string) {
	b.mu.Lock()
	wait, ok := b.tunnels[session]
	b.mu.Unlock()
	if !ok || wait.computeID != computeID {
		http.Error(w, "no such tunnel", http.StatusNotFound)
		return
	}
	ws, err := tunnelUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return // the upgrader has answered
	}
	conn := newWSConn(ws)
	b.mu.Lock()
	cur, ok := b.tunnels[session]
	sent := false
	if ok && cur.computeID == computeID {
		select {
		case cur.conn <- conn:
			sent = true
		default:
		}
	}
	b.mu.Unlock()
	if !sent {
		_ = conn.Close()
	}
}

func (b *Broker) queue(computeID string) chan Job {
	if b.queues[computeID] == nil {
		b.queues[computeID] = make(chan Job, 16)
	}
	return b.queues[computeID]
}

// Online reports whether the compute polled recently.
func (b *Broker) Online(computeID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return time.Since(b.lastSeen[computeID]) < onlineWindow
}

// Submit queues job for the compute and waits for its result.
func (b *Broker) Submit(ctx context.Context, computeID string, job Job) (Result, error) {
	return b.SubmitStream(ctx, computeID, job, nil)
}

// SubmitStream is Submit that also hands onEvent what the turn does as the
// compute reports it.
func (b *Broker) SubmitStream(ctx context.Context, computeID string, job Job, onEvent func(StreamEvent)) (Result, error) {
	b.mu.Lock()
	if time.Since(b.lastSeen[computeID]) >= onlineWindow {
		b.mu.Unlock()
		return Result{}, ErrOffline
	}
	job.ID = randomToken(18)
	job.Events = onEvent != nil
	done := make(chan Result, 1)
	b.waiting[job.ID], b.owner[job.ID] = done, computeID
	if onEvent != nil {
		b.sinks[job.ID] = onEvent
	}
	q := b.queue(computeID)
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		delete(b.waiting, job.ID)
		delete(b.sinks, job.ID)
		delete(b.owner, job.ID)
		b.mu.Unlock()
	}()
	select {
	case q <- job:
	default:
		return Result{}, ErrOffline
	}
	select {
	case r := <-done:
		return r, nil
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}

// Events delivers events a compute reports for one of its running jobs. It
// reports whether the job is one that compute runs and streams.
func (b *Broker) Events(computeID, jobID string, evs []StreamEvent) bool {
	b.mu.Lock()
	sink, mine := b.sinks[jobID], b.owner[jobID] == computeID
	b.mu.Unlock()
	if sink == nil || !mine {
		return false
	}
	for _, e := range evs {
		sink(e)
	}
	return true
}

// Enqueue hands a control job to the compute without waiting for an answer.
func (b *Broker) Enqueue(computeID string, job Job) error {
	b.mu.Lock()
	if time.Since(b.lastSeen[computeID]) >= onlineWindow {
		b.mu.Unlock()
		return ErrOffline
	}
	job.ID = randomToken(18)
	q := b.queue(computeID)
	b.mu.Unlock()
	select {
	case q <- job:
		return nil
	default:
		return ErrOffline
	}
}

// Next blocks until the compute has a job, wait elapses, or ctx ends.
func (b *Broker) Next(ctx context.Context, computeID string, wait time.Duration) (Job, bool) {
	b.mu.Lock()
	b.lastSeen[computeID] = time.Now()
	q := b.queue(computeID)
	b.mu.Unlock()
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case j := <-q:
		return j, true
	case <-t.C:
	case <-ctx.Done():
	}
	b.mu.Lock()
	b.lastSeen[computeID] = time.Now()
	b.mu.Unlock()
	return Job{}, false
}

// Complete delivers what computeID reports for one of its jobs to whoever
// waits for it. A result for a job that compute does not run is dropped, so
// no compute can answer for another. It reports whether it was delivered.
func (b *Broker) Complete(computeID string, r Result) bool {
	b.mu.Lock()
	ch, mine := b.waiting[r.JobID], b.owner[r.JobID] == computeID
	b.mu.Unlock()
	if ch == nil || !mine {
		return false
	}
	select {
	case ch <- r:
		return true
	default:
		return false
	}
}
