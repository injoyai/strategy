package server

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/injoyai/strategy/internal/artifacts"
	"github.com/injoyai/strategy/internal/data"
	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/factor"
	"github.com/injoyai/strategy/internal/jobs"
	"github.com/injoyai/strategy/internal/pipeline"
	"github.com/injoyai/strategy/internal/ports"
	"github.com/injoyai/strategy/internal/research"
	"github.com/injoyai/strategy/internal/screenrun"
	"github.com/injoyai/strategy/internal/store"
	"github.com/injoyai/strategy/internal/synthetic"
)

var acceptanceNow = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

type acceptanceStack struct {
	t       *testing.T
	dbPath  string
	db      *sql.DB
	data    *data.Store
	clk     *ports.FixedClock
	client  *http.Client
	ts      *httptest.Server
	cancels []context.CancelFunc
}

func bootAcceptance(t *testing.T) *acceptanceStack {
	t.Helper()
	s := &acceptanceStack{
		t:      t,
		dbPath: filepath.Join(t.TempDir(), "metadata.db"),
		clk:    ports.NewFixedClock(acceptanceNow),
	}
	s.start()
	t.Cleanup(s.stop)
	return s
}

func (s *acceptanceStack) start() {
	s.t.Helper()
	db, err := store.Open(s.dbPath)
	if err != nil {
		s.t.Fatalf("open store: %v", err)
	}
	baseCtx, cancelBase := context.WithCancel(context.Background())
	s.cancels = append(s.cancels, cancelBase)
	if err := store.Migrate(baseCtx, db, silentLogger()); err != nil {
		db.Close()
		s.t.Fatalf("migrate: %v", err)
	}
	jstore := jobs.NewStore(db, s.clk, jobs.NewHub())
	if err := jstore.RecoverOrphans(baseCtx); err != nil {
		db.Close()
		s.t.Fatalf("recover orphans: %v", err)
	}
	dataStore := data.New(db, s.clk)
	artStore := artifacts.New(s.t.TempDir(), db)
	provider, err := synthetic.Factory{}.Open(baseCtx, domain.ConnectionConfig{
		Provider: domain.VersionRef{ID: synthetic.ProviderID, Version: synthetic.ProviderVersion},
		Settings: json.RawMessage("{}"),
	})
	if err != nil {
		db.Close()
		s.t.Fatalf("open synthetic provider: %v", err)
	}
	// The stack mirrors the deployed assembly (cmd/researchd): a surface that is
	// only mounted when its dependency is supplied disappears silently, so the
	// acceptance run must mount what the binary mounts.
	registry, err := factor.NewRegistry(ports.SHA256Checksummer{})
	if err != nil {
		db.Close()
		s.t.Fatalf("build factor registry: %v", err)
	}
	if err := factor.RegisterDefaults(registry); err != nil {
		db.Close()
		s.t.Fatalf("register default factors: %v", err)
	}
	researchService, err := research.New(dataStore, registry, factor.NewCache(), artStore, ports.SHA256Checksummer{})
	if err != nil {
		db.Close()
		s.t.Fatalf("build research service: %v", err)
	}
	screenRuns, err := screenrun.New(dataStore, registry, factor.NewCache())
	if err != nil {
		db.Close()
		s.t.Fatalf("build screening run service: %v", err)
	}
	api := NewAPI(Options{
		Log:         silentLogger(),
		Auth:        LocalAuth{},
		Clock:       s.clk,
		Idempotency: store.NewIdempotencyStore(db),
		Jobs:        jstore,
		Data:        dataStore,
		Artifacts:   artStore,
		Research:    researchService,
		ScreenRuns:  screenRuns,
		Providers:   []ProviderRegistration{{Provider: provider, Factory: synthetic.Factory{}}},
	})
	handlers := (&pipeline.Handlers{
		Jobs:        jstore,
		Data:        dataStore,
		Factories:   map[domain.ID]ports.ProviderFactory{synthetic.ProviderID: synthetic.Factory{}},
		Normalizers: map[domain.ID]func(string) ports.Normalizer{synthetic.ProviderID: func(dataset string) ports.Normalizer { return synthetic.NewNormalizer(dataset) }},
		Clock:       s.clk,
		Artifacts:   artStore,
	}).Map()
	// The worker claims every registered kind; cmd/researchd merges the same two
	// maps in newRunHandlers.
	for kind, handler := range (&screenrun.Handlers{
		Jobs:      jstore,
		Runs:      dataStore,
		Service:   screenRuns,
		Artifacts: artStore,
	}).Map() {
		handlers[kind] = handler
	}
	loopCtx, cancelLoop := context.WithCancel(baseCtx)
	s.cancels = append(s.cancels, cancelLoop)
	go (&jobs.Loop{
		Store:    jstore,
		Owner:    "acceptance",
		Handlers: handlers,
		Lease:    5 * time.Second,
		Poll:     10 * time.Millisecond,
		Log:      silentLogger(),
	}).Run(loopCtx)
	s.db = db
	s.data = dataStore
	s.ts = httptest.NewServer(api.Handler())
	s.client = &http.Client{Timeout: 15 * time.Second}
}

func (s *acceptanceStack) stop() {
	if s.ts != nil {
		s.ts.Close()
		s.ts = nil
	}
	for _, cancel := range s.cancels {
		cancel()
	}
	if s.db != nil {
		s.db.Close()
		s.db = nil
	}
}

func (s *acceptanceStack) call(method, path string, body any, idem string) (int, http.Header, []byte) {
	s.t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			s.t.Fatalf("marshal %s %s body: %v", method, path, err)
		}
		rdr = bytes.NewReader(raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, s.ts.URL+"/api/v1"+path, rdr)
	if err != nil {
		s.t.Fatalf("build %s %s: %v", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idem != "" {
		req.Header.Set("Idempotency-Key", idem)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		s.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		s.t.Fatalf("%s %s read: %v", method, path, err)
	}
	return resp.StatusCode, resp.Header, raw
}

func decodeBody[T any](t *testing.T, label string, raw []byte) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v\nbody: %s", label, err, raw)
	}
	return out
}

type acceptanceWireError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

type acceptanceJobRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type acceptanceJob struct {
	ID          string               `json:"id"`
	Kind        string               `json:"kind"`
	State       string               `json:"state"`
	Phase       string               `json:"phase"`
	Completed   int64                `json:"completed"`
	Total       *int64               `json:"total"`
	Error       *acceptanceWireError `json:"error"`
	ResultRefs  []acceptanceJobRef   `json:"result_refs"`
	ParentJobID *string              `json:"parent_job_id"`
}

type acceptanceIssue struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Path     string `json:"path"`
	Message  string `json:"message"`
}

type acceptanceBatch struct {
	ID       string            `json:"id"`
	JobID    string            `json:"job_id"`
	Dataset  string            `json:"dataset_id"`
	RowCount int64             `json:"row_count"`
	Ready    bool              `json:"ready"`
	Issues   []acceptanceIssue `json:"issues"`
}

type acceptanceProvider struct {
	ID           string `json:"id"`
	Version      string `json:"version"`
	Capabilities []struct {
		Dataset     string   `json:"dataset"`
		Frequencies []string `json:"frequencies"`
		MaxPageSize int      `json:"max_page_size"`
	} `json:"capabilities"`
}

type acceptanceConnection struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Name    string `json:"name"`
}

type acceptanceSnapshot struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	BatchIDs     []string `json:"batch_ids"`
	StrictPIT    bool     `json:"strict_pit"`
	ManifestHash string   `json:"manifest_hash"`
}

type acceptanceValue struct {
	Kind          string  `json:"kind"`
	Value         any     `json:"value"`
	MissingReason *string `json:"missing_reason"`
}

type acceptanceObservation struct {
	InstrumentID *string                    `json:"instrument_id"`
	Dataset      string                     `json:"dataset"`
	EventTime    time.Time                  `json:"event_time"`
	Values       map[string]acceptanceValue `json:"values"`
}

type acceptancePage[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

type acceptanceSSEFrame struct {
	ID       string
	Sequence string
	State    string
}

func (s *acceptanceStack) waitJob(jobID, wantState string) acceptanceJob {
	s.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		code, _, raw := s.call(http.MethodGet, "/jobs/"+jobID, nil, "")
		if code != http.StatusOK {
			s.t.Fatalf("GET /jobs/%s: status %d body %s", jobID, code, raw)
		}
		job := decodeBody[acceptanceJob](s.t, "job "+jobID, raw)
		if job.State == wantState {
			return job
		}
		switch job.State {
		case string(jobs.StateSucceeded), string(jobs.StateFailed), string(jobs.StateCancelled):
			s.t.Fatalf("job %s reached terminal state %s while waiting for %s: phase %q, error %+v",
				jobID, job.State, wantState, job.Phase, job.Error)
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("timed out waiting for job %s to reach %s, last state %s phase %q", jobID, wantState, job.State, job.Phase)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (s *acceptanceStack) readJobEvents(jobID, lastEventID string, maxFrames int) []acceptanceSSEFrame {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.ts.URL+"/api/v1/jobs/"+jobID+"/events", nil)
	if err != nil {
		s.t.Fatalf("build events request %s: %v", jobID, err)
	}
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		s.t.Fatalf("events %s: %v", jobID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		s.t.Fatalf("events %s: status %d body %s", jobID, resp.StatusCode, raw)
	}
	var frames []acceptanceSSEFrame
	scanner := bufio.NewScanner(resp.Body)
	curID := ""
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "id: "):
			curID = strings.TrimSpace(line[4:])
		case strings.HasPrefix(line, "data: "):
			var payload struct {
				Sequence string `json:"sequence"`
				Job      struct {
					State string `json:"state"`
				} `json:"job"`
			}
			if err := json.Unmarshal([]byte(line[6:]), &payload); err != nil {
				s.t.Fatalf("events %s: malformed frame %q: %v", jobID, line, err)
			}
			frames = append(frames, acceptanceSSEFrame{ID: curID, Sequence: payload.Sequence, State: payload.Job.State})
			if len(frames) >= maxFrames {
				return frames
			}
			switch payload.Job.State {
			case string(jobs.StateSucceeded), string(jobs.StateFailed), string(jobs.StateCancelled):
				return frames
			}
		}
	}
	return frames
}

func (s *acceptanceStack) createConnection(name, settings, idem string) acceptanceConnection {
	s.t.Helper()
	code, _, raw := s.call(http.MethodPost, "/connections", map[string]any{
		"provider_ref": map[string]any{"id": synthetic.ProviderID, "version": synthetic.ProviderVersion},
		"name":         name,
		"settings":     json.RawMessage(settings),
	}, idem)
	if code != http.StatusCreated {
		s.t.Fatalf("POST /connections: status %d body %s", code, raw)
	}
	return decodeBody[acceptanceConnection](s.t, "connection", raw)
}

func acceptanceIngestionBody(connID, version string) map[string]any {
	return map[string]any{
		"connection_ref": map[string]any{"id": connID, "version": version},
		"dataset":        synthetic.DatasetBar,
		"frequency":      "daily",
		"instrument_ids": []string{"INST_A", "INST_B"},
		"range":          map[string]any{"from": "2026-01-01T00:00:00Z", "to": "2026-02-01T00:00:00Z"},
		"mode":           "backfill",
		// target_unit declares the unit the normalized value is stored in, so it
		// is the platform's vocabulary (the bar schema stores close as a price),
		// not the source's currency label. The unit is what a factor's declared
		// input unit is checked against, so a declaration in the wrong vocabulary
		// blocks every run that reads the field.
		"mapping": []map[string]any{
			{"source_field": "close", "target_field": "close", "source_unit": "CNY", "target_unit": "price", "scale": "1"},
			{"source_field": "price", "target_field": "price", "source_unit": "CNY", "target_unit": "price", "scale": "1"},
		},
		"timezone":                "UTC",
		"availability_policy_ref": map[string]any{"id": "default-policy", "version": "v1"},
	}
}

func (s *acceptanceStack) startIngestion(connID, version, idem string) acceptanceJob {
	s.t.Helper()
	code, hdr, raw := s.call(http.MethodPost, "/ingestions", acceptanceIngestionBody(connID, version), idem)
	if code != http.StatusAccepted {
		s.t.Fatalf("POST /ingestions: status %d body %s", code, raw)
	}
	job := decodeBody[acceptanceJob](s.t, "ingestion job", raw)
	if hdr.Get("Location") != "/api/v1/jobs/"+job.ID {
		s.t.Fatalf("ingestion job Location header %q does not match job id %q", hdr.Get("Location"), job.ID)
	}
	if job.Kind != jobKindIngestionRun {
		s.t.Fatalf("ingestion job kind %q, want %q", job.Kind, jobKindIngestionRun)
	}
	return job
}

func (s *acceptanceStack) queryData(snapID, asOf string) acceptancePage[acceptanceObservation] {
	s.t.Helper()
	code, _, raw := s.call(http.MethodPost, "/data/query", map[string]any{
		"snapshot_id":    snapID,
		"as_of":          asOf,
		"dataset":        synthetic.DatasetBar,
		"frequency":      "daily",
		"instrument_ids": []string{"INST_A", "INST_B"},
		"fields":         []string{"close"},
		"range":          map[string]any{"from": "2026-01-01T00:00:00Z", "to": "2026-02-01T00:00:00Z"},
	}, "")
	if code != http.StatusOK {
		s.t.Fatalf("POST /data/query: status %d body %s", code, raw)
	}
	return decodeBody[acceptancePage[acceptanceObservation]](s.t, "observation page", raw)
}

func assertObservationPage(t *testing.T, page acceptancePage[acceptanceObservation], want int) {
	t.Helper()
	if len(page.Items) != want {
		t.Fatalf("query returned %d items, want %d", len(page.Items), want)
	}
	if want == 0 {
		return
	}
	first := page.Items[0]
	if first.InstrumentID == nil || *first.InstrumentID != "INST_A" {
		t.Fatalf("first item instrument %v, want INST_A", first.InstrumentID)
	}
	if got := first.EventTime.UTC().Format(time.RFC3339); got != "2026-01-05T15:00:00Z" {
		t.Fatalf("first item event_time %s, want 2026-01-05T15:00:00Z", got)
	}
	closeVal, ok := first.Values["close"]
	if !ok {
		t.Fatalf("first item has no close value: %+v", first.Values)
	}
	if closeVal.Kind != string(domain.ValueDecimal) || closeVal.Value != "10.40" {
		t.Fatalf("first item close = %+v, want decimal 10.40", closeVal)
	}
}

func TestM0VerticalSliceAcceptance(t *testing.T) {
	s := bootAcceptance(t)

	code, _, raw := s.call(http.MethodGet, "/providers", nil, "")
	if code != http.StatusOK {
		t.Fatalf("GET /providers: status %d body %s", code, raw)
	}
	providers := decodeBody[acceptancePage[acceptanceProvider]](t, "providers", raw)
	barFound := false
	for _, p := range providers.Items {
		if p.ID != synthetic.ProviderID.String() {
			continue
		}
		for _, c := range p.Capabilities {
			if c.Dataset != synthetic.DatasetBar {
				continue
			}
			barFound = true
			if c.Frequencies[0] != "daily" && !containsString(c.Frequencies, "daily") {
				t.Fatalf("bar capability frequencies %v, want daily", c.Frequencies)
			}
			if c.MaxPageSize <= 0 {
				t.Fatalf("bar capability max_page_size %d, want > 0", c.MaxPageSize)
			}
		}
	}
	if !barFound {
		t.Fatalf("synthetic provider with bar capability missing: %s", raw)
	}

	conn := s.createConnection("acceptance-demo", "{}", "acceptance-conn")
	if conn.Version != "1" || conn.ID == "" {
		t.Fatalf("created connection unexpected: %+v", conn)
	}
	code, _, replayRaw := s.call(http.MethodPost, "/connections", map[string]any{
		"provider_ref": map[string]any{"id": synthetic.ProviderID, "version": synthetic.ProviderVersion},
		"name":         "acceptance-demo",
		"settings":     json.RawMessage("{}"),
	}, "acceptance-conn")
	if code != http.StatusCreated {
		t.Fatalf("idempotent connection replay: status %d body %s", code, replayRaw)
	}
	replayed := decodeBody[acceptanceConnection](t, "connection replay", replayRaw)
	if replayed.ID != conn.ID {
		t.Fatalf("idempotent replay created new connection %s, want %s", replayed.ID, conn.ID)
	}

	ingestJob := s.startIngestion(conn.ID, conn.Version, "acceptance-ingest-clean")
	done := s.waitJob(ingestJob.ID, string(jobs.StateSucceeded))
	if done.Phase != "appended" {
		t.Fatalf("ingestion phase %q, want appended", done.Phase)
	}
	if done.Completed != 8 {
		t.Fatalf("ingestion completed %d, want 8", done.Completed)
	}
	if len(done.ResultRefs) == 0 || done.ResultRefs[0].Kind != "batch" {
		t.Fatalf("ingestion result refs %+v, want batch ref", done.ResultRefs)
	}
	batchID := done.ResultRefs[0].ID

	frames := s.readJobEvents(ingestJob.ID, "", 100)
	if len(frames) < 2 {
		t.Fatalf("expected at least 2 job events, got %+v", frames)
	}
	if frames[len(frames)-1].State != string(jobs.StateSucceeded) {
		t.Fatalf("last streamed event state %q, want succeeded", frames[len(frames)-1].State)
	}
	resumed := s.readJobEvents(ingestJob.ID, "1", 1)
	if len(resumed) == 0 || resumed[0].Sequence != "2" || resumed[0].ID != "2" {
		t.Fatalf("reconnect after Last-Event-ID 1 resumed at %+v, want sequence 2", resumed)
	}

	code, _, raw = s.call(http.MethodGet, "/batches?dataset_id="+synthetic.DatasetBar, nil, "")
	if code != http.StatusOK {
		t.Fatalf("GET /batches: status %d body %s", code, raw)
	}
	batches := decodeBody[acceptancePage[acceptanceBatch]](t, "batches", raw)
	if len(batches.Items) != 1 {
		t.Fatalf("expected 1 batch, got %d: %s", len(batches.Items), raw)
	}
	batch := batches.Items[0]
	if batch.ID != batchID || batch.RowCount != 8 || !batch.Ready {
		t.Fatalf("batch %+v, want id %s with 8 ready rows", batch, batchID)
	}
	if len(batch.Issues) != 2 {
		t.Fatalf("batch issues %+v, want 2 missing-trading-day warnings", batch.Issues)
	}
	for _, issue := range batch.Issues {
		if issue.Severity != "warning" || issue.Code != "quality.missing_trading_day" {
			t.Fatalf("batch issue %+v, want warning quality.missing_trading_day", issue)
		}
	}

	faultConn := s.createConnection("acceptance-fault", `{"faults":{"fail_first_n_fetches":1}}`, "acceptance-conn-fault")
	faultJob := s.startIngestion(faultConn.ID, faultConn.Version, "acceptance-ingest-fault")
	failed := s.waitJob(faultJob.ID, string(jobs.StateFailed))
	if failed.Error == nil || !strings.Contains(failed.Error.Message, "synthetic: injected upstream failure 1 of 1") {
		t.Fatalf("failed job error %+v, want injected upstream failure", failed.Error)
	}
	if len(failed.ResultRefs) != 0 {
		t.Fatalf("failed job produced result refs %+v, want none", failed.ResultRefs)
	}
	code, _, raw = s.call(http.MethodPost, "/jobs/"+faultJob.ID+"/retry", nil, "acceptance-retry")
	if code != http.StatusAccepted {
		t.Fatalf("POST /jobs/%s/retry: status %d body %s", faultJob.ID, code, raw)
	}
	retried := decodeBody[acceptanceJob](t, "retried job", raw)
	if retried.ID == faultJob.ID {
		t.Fatalf("retry reused job id %s, want a new job", faultJob.ID)
	}
	retryDone := s.waitJob(retried.ID, string(jobs.StateFailed))
	if retryDone.Error == nil || !strings.Contains(retryDone.Error.Message, "synthetic: injected upstream failure 1 of 1") {
		t.Fatalf("retried job error %+v, want injected upstream failure", retryDone.Error)
	}

	code, _, raw = s.call(http.MethodPost, "/snapshots", map[string]any{
		"name":       "acceptance-snapshot",
		"batch_ids":  []string{batchID},
		"strict_pit": true,
	}, "acceptance-snapshot")
	if code != http.StatusAccepted {
		t.Fatalf("POST /snapshots: status %d body %s", code, raw)
	}
	snapJob := decodeBody[acceptanceJob](t, "snapshot job", raw)
	if snapJob.Kind != jobKindSnapshotPublish {
		t.Fatalf("snapshot job kind %q, want %q", snapJob.Kind, jobKindSnapshotPublish)
	}
	snapDone := s.waitJob(snapJob.ID, string(jobs.StateSucceeded))
	if len(snapDone.ResultRefs) == 0 || snapDone.ResultRefs[0].Kind != "snapshot" {
		t.Fatalf("snapshot result refs %+v, want snapshot ref", snapDone.ResultRefs)
	}
	snapID := snapDone.ResultRefs[0].ID

	code, _, raw = s.call(http.MethodGet, "/snapshots/"+snapID, nil, "")
	if code != http.StatusOK {
		t.Fatalf("GET /snapshots/%s: status %d body %s", snapID, code, raw)
	}
	snapshot := decodeBody[acceptanceSnapshot](t, "snapshot", raw)
	if snapshot.Name != "acceptance-snapshot" || !snapshot.StrictPIT || snapshot.ManifestHash == "" {
		t.Fatalf("snapshot %+v, want strict PIT snapshot with manifest hash", snapshot)
	}
	if len(snapshot.BatchIDs) != 1 || snapshot.BatchIDs[0] != batchID {
		t.Fatalf("snapshot batch_ids %v, want [%s]", snapshot.BatchIDs, batchID)
	}

	visible := s.queryData(snapID, "2026-01-15T12:00:00Z")
	assertObservationPage(t, visible, 8)
	mid := s.queryData(snapID, "2026-01-06T12:00:00Z")
	assertObservationPage(t, mid, 2)
	before := s.queryData(snapID, "2026-01-05T12:00:00Z")
	assertObservationPage(t, before, 0)

	s.stop()
	s.start()

	code, _, raw = s.call(http.MethodGet, "/connections/"+conn.ID, nil, "")
	if code != http.StatusOK {
		t.Fatalf("GET /connections after restart: status %d body %s", code, raw)
	}
	if got := decodeBody[acceptanceConnection](t, "connection after restart", raw); got.Version != "1" || got.ID != conn.ID {
		t.Fatalf("connection after restart %+v, want version 1", got)
	}
	code, _, raw = s.call(http.MethodGet, "/batches/"+batchID, nil, "")
	if code != http.StatusOK {
		t.Fatalf("GET /batches/%s after restart: status %d body %s", batchID, code, raw)
	}
	batchAfter := decodeBody[acceptanceBatch](t, "batch after restart", raw)
	if batchAfter.RowCount != 8 || !batchAfter.Ready || len(batchAfter.Issues) != 2 {
		t.Fatalf("batch after restart %+v, want 8 ready rows with 2 issues", batchAfter)
	}
	code, _, raw = s.call(http.MethodGet, "/snapshots/"+snapID, nil, "")
	if code != http.StatusOK {
		t.Fatalf("GET /snapshots/%s after restart: status %d body %s", snapID, code, raw)
	}
	snapshotAfter := decodeBody[acceptanceSnapshot](t, "snapshot after restart", raw)
	if snapshotAfter.ManifestHash != snapshot.ManifestHash {
		t.Fatalf("snapshot manifest hash changed after restart: %s != %s", snapshotAfter.ManifestHash, snapshot.ManifestHash)
	}
	code, _, raw = s.call(http.MethodGet, "/jobs/"+ingestJob.ID, nil, "")
	if code != http.StatusOK {
		t.Fatalf("GET /jobs/%s after restart: status %d body %s", ingestJob.ID, code, raw)
	}
	jobAfter := decodeBody[acceptanceJob](t, "job after restart", raw)
	if jobAfter.State != string(jobs.StateSucceeded) || jobAfter.Phase != "appended" || len(jobAfter.ResultRefs) != 1 || jobAfter.ResultRefs[0].ID != batchID {
		t.Fatalf("job after restart %+v, want succeeded appended with batch %s", jobAfter, batchID)
	}
	code, _, raw = s.call(http.MethodGet, "/jobs/"+faultJob.ID, nil, "")
	if code != http.StatusOK {
		t.Fatalf("GET /jobs/%s after restart: status %d body %s", faultJob.ID, code, raw)
	}
	faultAfter := decodeBody[acceptanceJob](t, "failed job after restart", raw)
	if faultAfter.State != string(jobs.StateFailed) || faultAfter.Error == nil || !strings.Contains(faultAfter.Error.Message, "synthetic: injected upstream failure 1 of 1") {
		t.Fatalf("failed job after restart %+v, want persisted failure", faultAfter)
	}
	visibleAfter := s.queryData(snapID, "2026-01-15T12:00:00Z")
	assertObservationPage(t, visibleAfter, 8)
	framesAfter := s.readJobEvents(ingestJob.ID, "", 100)
	if len(framesAfter) == 0 || framesAfter[len(framesAfter)-1].State != string(jobs.StateSucceeded) {
		t.Fatalf("job events after restart %+v, want replayed terminal event", framesAfter)
	}
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
