package synthetic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/ports"
)

var testNow = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

var (
	_ ports.DataProvider    = (*Provider)(nil)
	_ ports.ProviderFactory = Factory{}
)

func testProvider(t *testing.T, settings string) *Provider {
	t.Helper()
	p, err := Factory{}.Open(context.Background(), domain.ConnectionConfig{
		Provider: domain.VersionRef{ID: ProviderID, Version: ProviderVersion},
		Settings: json.RawMessage(settings),
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	prov, ok := p.(*Provider)
	if !ok {
		t.Fatalf("Open() returned %T, want *Provider", p)
	}
	prov.clock = ports.NewFixedClock(testNow)
	return prov
}

func mustID(t *testing.T, s string) domain.ID {
	t.Helper()
	id, err := domain.ParseID(s)
	if err != nil {
		t.Fatalf("ParseID(%q) error = %v", s, err)
	}
	return id
}

func baseReq(t *testing.T, dataset string) domain.FetchRequest {
	t.Helper()
	fields := make([]string, 0, 8)
	for _, f := range datasetFields(dataset) {
		fields = append(fields, f.Name)
	}
	return domain.FetchRequest{
		Dataset:       dataset,
		Frequency:     "daily",
		InstrumentIDs: []domain.ID{mustID(t, "INST_A"), mustID(t, "INST_B")},
		Range: domain.Interval{
			From: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			To:   time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		},
		Fields: fields,
	}
}

func fetchOne(t *testing.T, p *Provider, req domain.FetchRequest) domain.RawPage {
	t.Helper()
	page, err := p.Fetch(context.Background(), req)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	return page
}

func decodePage(t *testing.T, page domain.RawPage) pagePayload {
	t.Helper()
	var payload pagePayload
	if err := json.Unmarshal(page.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	return payload
}

func assertCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %s, got nil", want)
	}
	var de *domain.Error
	if !errors.As(err, &de) {
		t.Fatalf("error %v is not *domain.Error", err)
	}
	if de.Code != want {
		t.Fatalf("error code = %s, want %s (message: %s)", de.Code, want, de.Message)
	}
}

func TestFactoryConfigSchema(t *testing.T) {
	schema := Factory{}.ConfigSchema()
	if !json.Valid(schema) {
		t.Fatalf("ConfigSchema() is not valid JSON: %s", schema)
	}
	if !strings.Contains(string(schema), "include_bad_data") {
		t.Fatalf("ConfigSchema() missing include_bad_data: %s", schema)
	}
	if !strings.Contains(string(schema), "fail_first_n_fetches") {
		t.Fatalf("ConfigSchema() missing fail_first_n_fetches: %s", schema)
	}
	if !strings.Contains(string(schema), "rate_limit_after_n_fetches") {
		t.Fatalf("ConfigSchema() missing rate_limit_after_n_fetches: %s", schema)
	}
}

func TestOpenRejectsForeignProviderRef(t *testing.T) {
	_, err := Factory{}.Open(context.Background(), domain.ConnectionConfig{
		Provider: domain.VersionRef{ID: "other", Version: "v1"},
		Settings: json.RawMessage("{}"),
	})
	assertCode(t, err, domain.CodeValidationInvalid)
	if !strings.Contains(err.Error(), `does not match "synthetic"`) {
		t.Fatalf("Open() error = %v, want mismatch message", err)
	}
}

func TestOpenRejectsUnknownSettingsField(t *testing.T) {
	_, err := Factory{}.Open(context.Background(), domain.ConnectionConfig{
		Provider: domain.VersionRef{ID: ProviderID, Version: ProviderVersion},
		Settings: json.RawMessage(`{"nope":1}`),
	})
	assertCode(t, err, domain.CodeValidationInvalid)
	if !strings.Contains(err.Error(), "synthetic: invalid settings:") {
		t.Fatalf("Open() error = %v, want invalid settings message", err)
	}
}

func TestOpenRejectsNegativeFaults(t *testing.T) {
	_, err := Factory{}.Open(context.Background(), domain.ConnectionConfig{
		Provider: domain.VersionRef{ID: ProviderID, Version: ProviderVersion},
		Settings: json.RawMessage(`{"faults":{"fail_first_n_fetches":-1}}`),
	})
	assertCode(t, err, domain.CodeValidationInvalid)
	if got := err.Error(); got != "synthetic: fault counts must be non-negative" {
		t.Fatalf("Open() error = %q, want fault counts message", got)
	}
}

func TestDescribeCapabilities(t *testing.T) {
	p := testProvider(t, "{}")
	desc, err := p.Describe(context.Background())
	if err != nil {
		t.Fatalf("Describe() error = %v", err)
	}
	if desc.Ref.ID != ProviderID || desc.Ref.Version != ProviderVersion {
		t.Fatalf("Describe() ref = %s@%s, want %s@%s", desc.Ref.ID, desc.Ref.Version, ProviderID, ProviderVersion)
	}
	if desc.Name != ProviderName {
		t.Fatalf("Describe() name = %q, want %q", desc.Name, ProviderName)
	}
	if len(desc.Capabilities) != 3 {
		t.Fatalf("Describe() capabilities = %d, want 3", len(desc.Capabilities))
	}
	wantDatasets := []string{DatasetInstrument, DatasetCalendar, DatasetBar}
	wantFreq := map[string]string{DatasetInstrument: "static", DatasetCalendar: "daily", DatasetBar: "daily"}
	for i, c := range desc.Capabilities {
		if c.Dataset != wantDatasets[i] {
			t.Fatalf("capabilities[%d].dataset = %q, want %q", i, c.Dataset, wantDatasets[i])
		}
		if len(c.Frequencies) != 1 || c.Frequencies[0] != wantFreq[c.Dataset] {
			t.Fatalf("capabilities[%d] frequencies = %v, want [%s]", i, c.Frequencies, wantFreq[c.Dataset])
		}
		if c.MaxPageSize != MaxPageSize {
			t.Fatalf("capabilities[%d] max_page_size = %d, want %d", i, c.MaxPageSize, MaxPageSize)
		}
		if c.PITLevel != domain.PITVerified {
			t.Fatalf("capabilities[%d] pit_level = %q, want %q", i, c.PITLevel, domain.PITVerified)
		}
		if len(c.Fields) != len(datasetFields(c.Dataset)) {
			t.Fatalf("capabilities[%d] fields = %d, want %d", i, len(c.Fields), len(datasetFields(c.Dataset)))
		}
	}
	inst := desc.Capabilities[0]
	if inst.Coverage == nil {
		t.Fatal("instrument coverage is nil")
	}
	if !inst.Coverage.From.Equal(parseTime("2026-01-05T09:30:00Z")) || !inst.Coverage.To.Equal(parseTime("2026-01-05T09:30:01Z")) {
		t.Fatalf("instrument coverage = %s..%s", inst.Coverage.From, inst.Coverage.To)
	}
	cal := desc.Capabilities[1]
	if cal.Coverage == nil {
		t.Fatal("calendar coverage is nil")
	}
	if !cal.Coverage.From.Equal(parseTime("2026-01-05T00:00:00Z")) || !cal.Coverage.To.Equal(parseTime("2026-01-09T00:00:01Z")) {
		t.Fatalf("calendar coverage = %s..%s", cal.Coverage.From, cal.Coverage.To)
	}
	bar := desc.Capabilities[2]
	if bar.Coverage == nil {
		t.Fatal("bar coverage is nil")
	}
	if !bar.Coverage.From.Equal(parseTime("2026-01-05T15:00:00Z")) || !bar.Coverage.To.Equal(parseTime("2026-01-09T15:00:01Z")) {
		t.Fatalf("bar coverage = %s..%s", bar.Coverage.From, bar.Coverage.To)
	}
}

func TestCheckReturnsNoIssues(t *testing.T) {
	p := testProvider(t, "{}")
	issues, err := p.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("Check() issues = %v, want none", issues)
	}
}

func TestFetchDefaultExcludesBadData(t *testing.T) {
	p := testProvider(t, "{}")
	payload := decodePage(t, fetchOne(t, p, baseReq(t, DatasetBar)))
	if payload.Dataset != DatasetBar {
		t.Fatalf("payload dataset = %q, want %q", payload.Dataset, DatasetBar)
	}
	if len(payload.Rows) != 8 {
		t.Fatalf("rows = %d, want 8", len(payload.Rows))
	}
	for _, row := range payload.Rows {
		if row.Provenance.SourceRecordID == "bar-bad-ohlc" {
			t.Fatal("default fetch must not serve bad-data fixtures")
		}
	}
	again := decodePage(t, fetchOne(t, p, baseReq(t, DatasetBar)))
	if len(again.Rows) != 8 {
		t.Fatalf("second fetch rows = %d, want 8 (fixture must not accumulate)", len(again.Rows))
	}
}

func TestFetchIsDeterministic(t *testing.T) {
	p := testProvider(t, "{}")
	req := baseReq(t, DatasetBar)
	a := fetchOne(t, p, req)
	b := fetchOne(t, p, req)
	if a.Checksum != b.Checksum {
		t.Fatalf("checksums differ: %s vs %s", a.Checksum, b.Checksum)
	}
	if !bytes.Equal(a.Payload, b.Payload) {
		t.Fatal("payloads differ for identical requests")
	}
	if !a.FetchedAt.Equal(b.FetchedAt) {
		t.Fatalf("fetched_at differs: %s vs %s", a.FetchedAt, b.FetchedAt)
	}
	if a.ContentType != "application/json" {
		t.Fatalf("content_type = %q", a.ContentType)
	}
	if a.ProviderRevision != ProviderVersion {
		t.Fatalf("provider_revision = %q, want %q", a.ProviderRevision, ProviderVersion)
	}
}

func TestFetchFiltersByRangeAndInstrument(t *testing.T) {
	p := testProvider(t, "{}")
	req := baseReq(t, DatasetBar)
	req.Range = domain.Interval{From: parseTime("2026-01-07T00:00:00Z"), To: parseTime("2026-01-09T00:00:00Z")}
	req.InstrumentIDs = []domain.ID{mustID(t, "INST_A")}
	payload := decodePage(t, fetchOne(t, p, req))
	if len(payload.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(payload.Rows))
	}
	for _, row := range payload.Rows {
		if row.InstrumentID == nil || *row.InstrumentID != domain.ID("INST_A") {
			t.Fatalf("unexpected instrument %v", row.InstrumentID)
		}
	}
}

func TestFetchHalfOpenRangeBoundary(t *testing.T) {
	p := testProvider(t, "{}")
	req := baseReq(t, DatasetBar)
	req.Range = domain.Interval{From: parseTime("2026-01-05T15:00:00Z"), To: parseTime("2026-01-06T15:00:00Z")}
	req.InstrumentIDs = []domain.ID{mustID(t, "INST_A")}
	payload := decodePage(t, fetchOne(t, p, req))
	if len(payload.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(payload.Rows))
	}
	if !payload.Rows[0].EventTime.Equal(parseTime("2026-01-05T15:00:00Z")) {
		t.Fatalf("event_time = %s, want 2026-01-05T15:00:00Z", payload.Rows[0].EventTime)
	}
}

func TestFetchCalendarIgnoresInstrumentFilter(t *testing.T) {
	p := testProvider(t, "{}")
	req := baseReq(t, DatasetCalendar)
	req.InstrumentIDs = []domain.ID{mustID(t, "INST_A")}
	payload := decodePage(t, fetchOne(t, p, req))
	if len(payload.Rows) != 5 {
		t.Fatalf("rows = %d, want 5", len(payload.Rows))
	}
	for _, row := range payload.Rows {
		if row.InstrumentID != nil {
			t.Fatalf("calendar row has instrument %v, want nil", row.InstrumentID)
		}
	}
}

func TestFetchProjectsFields(t *testing.T) {
	p := testProvider(t, "{}")
	req := baseReq(t, DatasetBar)
	req.Fields = []string{"open", "close"}
	page := fetchOne(t, p, req)
	payload := decodePage(t, page)
	if len(payload.Rows) != 8 {
		t.Fatalf("rows = %d, want 8", len(payload.Rows))
	}
	for i, row := range payload.Rows {
		if len(row.Values) != 2 {
			t.Fatalf("row %d has %d values, want 2", i, len(row.Values))
		}
		if _, ok := row.Values["open"]; !ok {
			t.Fatalf("row %d missing open", i)
		}
		if _, ok := row.Values["close"]; !ok {
			t.Fatalf("row %d missing close", i)
		}
	}
	full := fetchOne(t, p, baseReq(t, DatasetBar))
	if full.Checksum == page.Checksum {
		t.Fatal("projected checksum equals full checksum")
	}
}

func TestFetchPagination(t *testing.T) {
	p := testProvider(t, "{}")
	req := baseReq(t, DatasetBar)
	req.Page = domain.Page{Limit: 3}
	p1 := fetchOne(t, p, req)
	rows1 := decodePage(t, p1)
	if len(rows1.Rows) != 3 || p1.NextCursor != "3" {
		t.Fatalf("page1 rows=%d cursor=%q, want 3/3", len(rows1.Rows), p1.NextCursor)
	}
	if rows1.Rows[0].Provenance.SourceRecordID != "bar-INST_A-2026-01-05" {
		t.Fatalf("page1 first row = %q", rows1.Rows[0].Provenance.SourceRecordID)
	}
	req.Page.Cursor = "3"
	p2 := fetchOne(t, p, req)
	if len(decodePage(t, p2).Rows) != 3 || p2.NextCursor != "6" {
		t.Fatalf("page2 rows=%d cursor=%q, want 3/6", len(decodePage(t, p2).Rows), p2.NextCursor)
	}
	req.Page.Cursor = "6"
	p3 := fetchOne(t, p, req)
	if len(decodePage(t, p3).Rows) != 2 || p3.NextCursor != "" {
		t.Fatalf("page3 rows=%d cursor=%q, want 2/empty", len(decodePage(t, p3).Rows), p3.NextCursor)
	}
	req.Page.Cursor = "8"
	p4 := fetchOne(t, p, req)
	if len(decodePage(t, p4).Rows) != 0 || p4.NextCursor != "" {
		t.Fatalf("page4 rows=%d cursor=%q, want 0/empty", len(decodePage(t, p4).Rows), p4.NextCursor)
	}
	req.Page.Cursor = "9"
	_, err := p.Fetch(context.Background(), req)
	assertCode(t, err, domain.CodeValidationInvalid)
	if !strings.Contains(err.Error(), "is past the end of the result set") {
		t.Fatalf("cursor past end error = %v", err)
	}
	for _, bad := range []string{"abc", "-1"} {
		req.Page.Cursor = bad
		_, err = p.Fetch(context.Background(), req)
		assertCode(t, err, domain.CodeValidationInvalid)
		if !strings.Contains(err.Error(), "malformed page cursor") {
			t.Fatalf("cursor %q error = %v", bad, err)
		}
	}
	req.Page.Cursor = ""
	req.Page.Limit = 100
	pAll := fetchOne(t, p, req)
	if len(decodePage(t, pAll).Rows) != 8 {
		t.Fatalf("limit clamp rows = %d, want 8", len(decodePage(t, pAll).Rows))
	}
}

func TestFetchUnknownDataset(t *testing.T) {
	p := testProvider(t, "{}")
	req := domain.FetchRequest{
		Dataset:       "tick",
		Frequency:     "daily",
		InstrumentIDs: []domain.ID{mustID(t, "INST_A")},
		Range: domain.Interval{
			From: parseTime("2026-01-01T00:00:00Z"),
			To:   parseTime("2026-02-01T00:00:00Z"),
		},
		Fields: []string{"close"},
	}
	_, err := p.Fetch(context.Background(), req)
	assertCode(t, err, domain.CodeValidationInvalid)
	if got := err.Error(); got != `synthetic: unknown dataset "tick"` {
		t.Fatalf("error = %q", got)
	}
}

func TestFetchRejectsInvalidRequest(t *testing.T) {
	p := testProvider(t, "{}")
	req := baseReq(t, DatasetBar)
	req.InstrumentIDs = nil
	_, err := p.Fetch(context.Background(), req)
	assertCode(t, err, domain.CodeValidationInvalid)
	if got := err.Error(); got != "fetch: instrument_ids is required" {
		t.Fatalf("error = %q", got)
	}
}

func TestFetchBadDataOptIn(t *testing.T) {
	p := testProvider(t, `{"include_bad_data": true}`)
	payload := decodePage(t, fetchOne(t, p, baseReq(t, DatasetBar)))
	if len(payload.Rows) != 13 {
		t.Fatalf("rows = %d, want 13", len(payload.Rows))
	}
	found := make(map[string]bool)
	for _, row := range payload.Rows {
		found[row.Provenance.SourceRecordID] = true
	}
	for _, want := range []string{"bar-bad-ohlc", "bar-bad-negvol", "bar-bad-unit", "bar-bad-future", "bar-A-2026-01-05"} {
		if !found[want] {
			t.Fatalf("bad-data row %q missing from payload", want)
		}
	}
}

func TestFaultGateFailFirstN(t *testing.T) {
	p := testProvider(t, `{"faults":{"fail_first_n_fetches":2}}`)
	req := baseReq(t, DatasetBar)
	_, err := p.Fetch(context.Background(), req)
	assertCode(t, err, domain.CodeInternalUnavailable)
	if got := err.Error(); got != "synthetic: injected upstream failure 1 of 2" {
		t.Fatalf("error = %q", got)
	}
	_, err = p.Fetch(context.Background(), req)
	assertCode(t, err, domain.CodeInternalUnavailable)
	if got := err.Error(); got != "synthetic: injected upstream failure 2 of 2" {
		t.Fatalf("error = %q", got)
	}
	page := fetchOne(t, p, req)
	if len(decodePage(t, page).Rows) != 8 {
		t.Fatalf("third fetch rows = %d, want 8", len(decodePage(t, page).Rows))
	}
}

func TestFaultGateRateLimit(t *testing.T) {
	p := testProvider(t, `{"faults":{"rate_limit_after_n_fetches":1}}`)
	req := baseReq(t, DatasetBar)
	fetchOne(t, p, req)
	_, err := p.Fetch(context.Background(), req)
	assertCode(t, err, domain.CodeRateLimited)
	if got := err.Error(); got != "synthetic: injected rate limit after 1 fetches" {
		t.Fatalf("error = %q", got)
	}
}

func TestNormalizerRoundTrip(t *testing.T) {
	p := testProvider(t, "{}")
	page := fetchOne(t, p, baseReq(t, DatasetInstrument))
	n := NewNormalizer(DatasetInstrument)
	if n.Dataset() != DatasetInstrument {
		t.Fatalf("Dataset() = %q", n.Dataset())
	}
	if len(n.Schema()) != 5 {
		t.Fatalf("Schema() fields = %d, want 5", len(n.Schema()))
	}
	obs, issues, err := n.Normalize(context.Background(), page)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("Normalize() issues = %v, want none", issues)
	}
	if len(obs) != 2 {
		t.Fatalf("observations = %d, want 2", len(obs))
	}
	if obs[0].Dataset != DatasetInstrument {
		t.Fatalf("observation dataset = %q", obs[0].Dataset)
	}
	if got := obs[0].Values["code"].Encoded; got != "000001" {
		t.Fatalf("code = %q, want 000001", got)
	}
	if got := obs[1].Values["code"].Encoded; got != "000002" {
		t.Fatalf("code = %q, want 000002", got)
	}
	if !obs[0].Provenance.IngestedAt.Equal(testNow) {
		t.Fatalf("ingested_at = %s, want %s", obs[0].Provenance.IngestedAt, testNow)
	}
}

func TestNormalizerRejectsContentType(t *testing.T) {
	page := domain.RawPage{Payload: []byte("{}"), ContentType: "text/csv"}
	_, _, err := NewNormalizer(DatasetBar).Normalize(context.Background(), page)
	assertCode(t, err, domain.CodeValidationInvalid)
	if !strings.Contains(err.Error(), `expects application/json, got "text/csv"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestNormalizerDatasetMismatch(t *testing.T) {
	p := testProvider(t, "{}")
	page := fetchOne(t, p, baseReq(t, DatasetBar))
	_, _, err := NewNormalizer(DatasetInstrument).Normalize(context.Background(), page)
	assertCode(t, err, domain.CodeValidationInvalid)
	if !strings.Contains(err.Error(), `payload dataset "bar" does not match normalizer "instrument"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestNormalizerDecodeError(t *testing.T) {
	page := domain.RawPage{Payload: []byte("{not-json"), ContentType: "application/json"}
	_, _, err := NewNormalizer(DatasetBar).Normalize(context.Background(), page)
	assertCode(t, err, domain.CodeInternalError)
	if !strings.Contains(err.Error(), "synthetic: decode payload:") {
		t.Fatalf("error = %v", err)
	}
}

func TestNormalizerUnknownField(t *testing.T) {
	rows := []pageRow{{
		InstrumentID: idPtr("INST_A"),
		EventTime:    parseTime("2026-01-05T15:00:00Z"),
		Provenance: domain.Provenance{
			SourceID:       ProviderID,
			SourceRecordID: "bar-x",
			RevisionID:     "rev-001",
			AvailableAt:    parseTime("2026-01-05T15:30:00Z"),
			SchemaVersion:  "bar/v1",
			PolicyVersion:  "availability/v1",
		},
		Values: map[string]domain.Value{
			"close": {Kind: domain.ValueDecimal, Encoded: "10.00"},
			"zzz":   {Kind: domain.ValueString, Encoded: "1"},
		},
	}}
	payload, err := json.Marshal(pagePayload{Dataset: DatasetBar, Rows: rows})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	page := domain.RawPage{Payload: payload, ContentType: "application/json", FetchedAt: testNow}
	obs, issues, err := NewNormalizer(DatasetBar).Normalize(context.Background(), page)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if len(obs) != 1 {
		t.Fatalf("observations = %d, want 1", len(obs))
	}
	if obs[0].Values["close"].Encoded != "10.00" {
		t.Fatalf("close = %q", obs[0].Values["close"].Encoded)
	}
	if !obs[0].Provenance.IngestedAt.Equal(testNow) {
		t.Fatalf("ingested_at = %s, want %s", obs[0].Provenance.IngestedAt, testNow)
	}
	if len(issues) != 1 {
		t.Fatalf("issues = %d, want 1 (%v)", len(issues), issues)
	}
	if issues[0].Code != domain.CodeValidationUnknownField {
		t.Fatalf("issue code = %q", issues[0].Code)
	}
	if issues[0].Path != "row[0].zzz" {
		t.Fatalf("issue path = %q", issues[0].Path)
	}
	if issues[0].Severity != domain.SeverityWarning {
		t.Fatalf("issue severity = %q", issues[0].Severity)
	}
}
