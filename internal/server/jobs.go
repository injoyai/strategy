// The /jobs contract surface: list, get, cancel, retry and the SSE event
// stream. Handlers only translate transport to store calls; every state
// rule lives in the jobs package.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/jobs"
)

// eventReplayBatch is how many stored events one replay query fetches; the
// handler walks batches until a short page ends the replay.
const eventReplayBatch = 200

// sseHeartbeat bounds the silent gap on an idle stream. Heartbeats are SSE
// comment lines and never consume a business sequence (contract note).
const sseHeartbeat = 15 * time.Second

// RegisterJobs mounts the /jobs routes on the API.
func (a *API) RegisterJobs() {
	a.Handle(http.MethodGet, "/jobs", a.listJobs, RouteOptions{})
	a.Handle(http.MethodGet, "/jobs/{id}", a.getJob, RouteOptions{})
	a.Handle(http.MethodPost, "/jobs/{id}/cancel", a.cancelJob, RouteOptions{IdempotencyRequired: true})
	a.Handle(http.MethodPost, "/jobs/{id}/retry", a.retryJob, RouteOptions{IdempotencyRequired: true})
	a.Handle(http.MethodGet, "/jobs/{id}/events", a.streamJobEvents, RouteOptions{})
}

// jobWireError maps store failures to contract errors. jobs.ErrNotFound is
// a plain sentinel, so it becomes a 404 here; domain errors keep their code.
func (a *API) jobWireError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, jobs.ErrNotFound) {
		a.writeError(w, r, domain.NewError(domain.CodeResourceNotFound, "no such job"))
		return
	}
	a.writeError(w, r, err)
}

// listJobs answers GET /jobs with one keyset page.
func (a *API) listJobs(w http.ResponseWriter, r *http.Request) {
	page, ok := ParsePage(w, r)
	if !ok {
		return
	}
	q := jobs.ListQuery{
		Limit:  page.Limit,
		Sort:   page.Sort,
		Kind:   r.URL.Query().Get("kind"),
		Search: r.URL.Query().Get("q"),
	}
	if raw := r.URL.Query().Get("state"); raw != "" {
		state := jobs.State(raw)
		switch state {
		case jobs.StateQueued, jobs.StateRunning, jobs.StateCancelRequested,
			jobs.StateSucceeded, jobs.StateFailed, jobs.StateCancelled:
			q.State = state
		default:
			writeBoundaryError(w, r, domain.CodeValidationInvalid,
				"state must be one of: queued, running, cancel_requested, succeeded, failed, cancelled")
			return
		}
	}
	if page.Cursor != "" {
		cur, ok := ParseCursor(w, r, page.Cursor, page.Sort, a.clock.Now())
		if !ok {
			return
		}
		q.Cursor = cur.LastID
	}
	res, err := a.jobs.List(r.Context(), q)
	if err != nil {
		a.jobWireError(w, r, err)
		return
	}
	out := jobPage{Items: res.Items}
	if res.NextCursor != "" {
		cur := EncodeCursor(page.Sort, res.NextCursor, a.clock.Now())
		out.NextCursor = &cur
	}
	WriteJSON(w, http.StatusOK, out)
}

// jobPage mirrors the JobPage schema: items is required and always an
// array; next_cursor is required but nullable.
type jobPage struct {
	Items      []jobs.Job `json:"items"`
	NextCursor *string    `json:"next_cursor"`
}

// getJob answers GET /jobs/{id}.
func (a *API) getJob(w http.ResponseWriter, r *http.Request) {
	job, err := a.jobs.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		a.jobWireError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, job)
}

// cancelJob answers POST /jobs/{id}/cancel. Repeating a cancel returns the
// current state unchanged (idempotent per contract).
func (a *API) cancelJob(w http.ResponseWriter, r *http.Request) {
	job, err := a.jobs.RequestCancel(r.Context(), r.PathValue("id"))
	if err != nil {
		a.jobWireError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, job)
}

// retryJob answers POST /jobs/{id}/retry with 202 and the contract Location
// header pointing at the new job.
func (a *API) retryJob(w http.ResponseWriter, r *http.Request) {
	job, err := a.jobs.Retry(r.Context(), r.PathValue("id"))
	if err != nil {
		a.jobWireError(w, r, err)
		return
	}
	WriteAccepted(w, r, job.ID, job)
}

// streamJobEvents answers GET /jobs/{id}/events with an SSE stream: stored
// events replay first (resuming at Last-Event-ID), then live events fan out.
// A Last-Event-ID that fell out of the retention window answers 410.
func (a *API) streamJobEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	after, err := jobs.ParseSequence(r.Header.Get("Last-Event-ID"))
	if err != nil {
		writeBoundaryError(w, r, domain.CodeValidationInvalid,
			"Last-Event-ID must be a non-negative decimal integer")
		return
	}
	if _, err := a.jobs.Get(r.Context(), id); err != nil {
		a.jobWireError(w, r, err)
		return
	}
	if after > 0 {
		min, err := a.jobs.MinSequence(r.Context(), id)
		if err != nil {
			a.jobWireError(w, r, err)
			return
		}
		if after+1 < min {
			// The client's position is gone; the contract says re-fetch the
			// job and subscribe again from the current state.
			a.writeError(w, r, domain.NewError(domain.CodeResourceGone,
				"Replay window expired"))
			return
		}
	}

	// Subscribe before replaying so no committed event falls into the gap;
	// duplicates (replay overlap with the live channel) are filtered by
	// sequence below.
	ch, cancel := a.jobs.Subscribe(id)
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, canFlush := w.(http.Flusher)
	if canFlush {
		flusher.Flush()
	}
	writeSSE := func(frame string) {
		_, _ = fmt.Fprint(w, frame)
		if canFlush {
			flusher.Flush()
		}
	}

	last := after
	for {
		events, err := a.jobs.EventsAfter(r.Context(), id, last, eventReplayBatch)
		if err != nil {
			return // stream already started; the client reconnects and replays
		}
		for _, ev := range events {
			writeSSE(sseFrame(ev))
			last = ev.Sequence
		}
		if len(events) < eventReplayBatch {
			break
		}
	}

	heartbeat := time.NewTicker(sseHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if ev.Sequence <= last {
				continue // already replayed
			}
			writeSSE(sseFrame(ev))
			last = ev.Sequence
		case <-heartbeat.C:
			writeSSE(": ping\n\n")
		}
	}
}

// sseFrame renders one contract-shaped event:
//
//	id: <sequence>\nevent: job.updated\ndata: <JobEvent JSON>\n\n
//
// The JSON is exactly the marshaled Event, matching the stored payload shape
// (Event.MarshalJSON emits the contract wireEvent, sequence as string).
func sseFrame(ev jobs.Event) string {
	payload, err := json.Marshal(ev)
	if err != nil {
		return "" // unreachable: Event marshaling cannot fail
	}
	return fmt.Sprintf("id: %d\nevent: job.updated\ndata: %s\n\n", ev.Sequence, payload)
}
