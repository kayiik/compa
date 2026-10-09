package compute

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// Paths a dial-out compute calls on Compa. All authenticate with the
// credential issued at enrollment.
const (
	PollPath   = "/api/compute/poll"
	ResultPath = "/api/compute/result"
	EventsPath = "/api/compute/events"
)

// EventsReport is what a compute posts to EventsPath: events of a running job.
type EventsReport struct {
	JobID  string        `json:"job_id"`
	Events []StreamEvent `json:"events"`
}

// batchEvents returns an onEvent that queues events and a stop that flushes
// the rest, and a goroutine that posts them to the host a few times a second:
// a turn writes many small deltas, which are merged into one. Events are
// best-effort; the result still carries the whole answer.
func batchEvents(ctx context.Context, do func(context.Context, string, any) (*http.Response, error), jobID string, logf func(string, ...any)) (onEvent func(StreamEvent), stop func()) {
	ch := make(chan StreamEvent, 1024)
	done := make(chan struct{})
	post := func(batch []StreamEvent) {
		pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		r, err := do(pctx, EventsPath, EventsReport{JobID: jobID, Events: batch})
		if err != nil {
			logf("events post failed: %v", err)
			return
		}
		r.Body.Close()
	}
	go func() {
		defer close(done)
		tick := time.NewTicker(200 * time.Millisecond)
		defer tick.Stop()
		var batch []StreamEvent
		flush := func() {
			if len(batch) > 0 {
				post(batch)
				batch = nil
			}
		}
		for {
			select {
			case ev, ok := <-ch:
				if !ok {
					flush()
					return
				}
				if n := len(batch); n > 0 && ev.Type == "delta" && batch[n-1].Type == "delta" {
					batch[n-1].Text += ev.Text
				} else {
					batch = append(batch, ev)
				}
				if ev.Type == "tool" || len(batch) >= 50 {
					flush()
				}
			case <-tick.C:
				flush()
			}
		}
	}()
	return func(ev StreamEvent) {
			select {
			case ch <- ev:
			default: // a flood: the final answer still arrives whole
			}
		}, func() {
			close(ch)
			<-done
		}
}

// maxParallelJobs bounds the turns one compute runs at once; the host already
// serializes turns of the same Compita, so this is about different Compitas.
const maxParallelJobs = 16

// jobTimeout outlasts the longest turn the host will wait for.
const jobTimeout = 35 * time.Minute

// Serve is the loop an enrolled compute runs: poll Compa for jobs, run them,
// post the results. Jobs run alongside each other and polling goes on while
// they do, so the compute stays reachable and several Compitas can work at
// once. It returns only when ctx ends.
func Serve(ctx context.Context, id Identity, runner Runner, logf func(string, ...any)) {
	client := &http.Client{Timeout: 60 * time.Second}
	do := func(ctx context.Context, path string, body any) (*http.Response, error) {
		var buf bytes.Buffer
		_ = json.NewEncoder(&buf).Encode(body)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, id.CompaURL+path, &buf)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Compute-ID", id.ComputeID)
		req.Header.Set("Authorization", "Bearer "+id.Credential)
		return client.Do(req)
	}
	slots := make(chan struct{}, maxParallelJobs)
	var jobs sync.WaitGroup
	defer jobs.Wait()
	backoff := time.Second
	for ctx.Err() == nil {
		resp, err := do(ctx, PollPath, struct{}{})
		if err != nil || resp.StatusCode >= 300 && resp.StatusCode != http.StatusNoContent {
			if resp != nil {
				resp.Body.Close()
			}
			logf("poll failed (%v); retrying in %s", err, backoff)
			select {
			case <-ctx.Done():
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		if resp.StatusCode == http.StatusNoContent {
			resp.Body.Close()
			continue
		}
		var job Job
		err = json.NewDecoder(resp.Body).Decode(&job)
		resp.Body.Close()
		if err != nil || job.ID == "" {
			continue
		}
		if job.Tunnel != "" {
			// A control job: Compa wants to watch a Compita's browser.
			go connectBack(ctx, id, job.Tunnel, job.CompitaID, job.Isolation, logf)
			continue
		}
		jobs.Add(1)
		go func() {
			defer jobs.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				return
			}
			logf("job %s for %s", job.ID, job.CompitaID)
			jctx, cancel := context.WithTimeout(ctx, jobTimeout)
			var onEvent func(StreamEvent)
			stopEvents := func() {}
			if job.Events {
				onEvent, stopEvents = batchEvents(ctx, do, job.ID, logf)
			}
			reply, runErr := runner.withPeer(id).RunStream(jctx, Turn{CompitaID: job.CompitaID, Isolation: job.Isolation, Session: job.Session, Message: job.Message, Depth: job.Depth, Approvals: job.Approvals, Browser: job.Browser, Connections: job.Connections}, onEvent)
			cancel()
			stopEvents()
			res := Result{JobID: job.ID, Reply: reply}
			if runErr != nil {
				res.Error = runErr.Error()
			}
			// The answer must get out even if the turn timed out.
			pctx, pcancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
			defer pcancel()
			if r, err := do(pctx, ResultPath, res); err != nil {
				logf("result post failed: %v", err)
			} else {
				r.Body.Close()
			}
		}()
	}
}

func (r Runner) withPeer(id Identity) Runner {
	r.Peer = &Peer{URL: id.CompaURL, ComputeID: id.ComputeID, Credential: id.Credential}
	return r
}
