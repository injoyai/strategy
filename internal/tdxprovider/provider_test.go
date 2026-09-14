package tdxprovider

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/injoyai/tdx/protocol"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/ports"
)

var (
	_ ports.ProviderFactory = Factory{}
	_ ports.DataProvider    = (*Provider)(nil)
	_ ports.Normalizer      = (*Normalizer)(nil)
)

type fakeClient struct {
	codes     map[protocol.Exchange][]*protocol.Code
	indexBars []*protocol.Kline
	bars      map[string][]*protocol.Kline
	timeout   time.Duration
	closed    bool
}

func (f *fakeClient) GetCodeAll(exchange protocol.Exchange) (*protocol.CodeResp, error) {
	list := append([]*protocol.Code(nil), f.codes[exchange]...)
	return &protocol.CodeResp{Count: uint16(len(list)), List: list}, nil
}

func (f *fakeClient) GetIndexDay(_ string, start, count uint16) (*protocol.KlineResp, error) {
	return klinePage(f.indexBars, start, count), nil
}

func (f *fakeClient) GetKlineDay(code string, start, count uint16) (*protocol.KlineResp, error) {
	return klinePage(f.bars[code], start, count), nil
}

func (f *fakeClient) SetTimeout(timeout time.Duration) { f.timeout = timeout }
func (f *fakeClient) Close() error                     { f.closed = true; return nil }

func klinePage(all []*protocol.Kline, start, count uint16) *protocol.KlineResp {
	from := int(start)
	if from >= len(all) {
		return &protocol.KlineResp{}
	}
	to := from + int(count)
	if to > len(all) {
		to = len(all)
	}
	list := append([]*protocol.Kline(nil), all[from:to]...)
	return &protocol.KlineResp{Count: uint16(len(list)), List: list}
}

func openFake(t *testing.T, fake *fakeClient, now time.Time, settings json.RawMessage) *Provider {
	t.Helper()
	provider, err := (Factory{
		dial:  func([]string) (client, error) { return fake, nil },
		clock: ports.NewFixedClock(now),
	}).Open(context.Background(), domain.ConnectionConfig{
		Provider: domain.VersionRef{ID: ProviderID, Version: ProviderVersion},
		Settings: settings,
	})
	if err != nil {
		t.Fatalf("open provider: %v", err)
	}
	return provider.(*Provider)
}

func mustInterval(t *testing.T, from, to time.Time) domain.Interval {
	t.Helper()
	interval, err := domain.NewInterval(from, to)
	if err != nil {
		t.Fatalf("new interval: %v", err)
	}
	return interval
}

func TestSettingsAndDescriptorFailClosed(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "unknown field", raw: `{"debug":true}`},
		{name: "empty hosts", raw: `{"hosts":[]}`},
		{name: "wrong port", raw: `{"hosts":["127.0.0.1:7710"]}`},
		{name: "duplicate host", raw: `{"hosts":["127.0.0.1:7709","127.0.0.1:7709"]}`},
		{name: "timeout below minimum", raw: `{"timeout_ms":0}`},
		{name: "trailing json", raw: `{} {}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseSettings(json.RawMessage(tt.raw))
			if err == nil || domain.ErrorCode(err) != domain.CodeValidationInvalid {
				t.Fatalf("parseSettings(%s) error = %v, want validation.invalid", tt.raw, err)
			}
		})
	}

	parsed, err := parseSettings(json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("parse default settings: %v", err)
	}
	if parsed.TimeoutMS != 5000 || len(parsed.Hosts) != 0 {
		t.Fatalf("default settings = %+v", parsed)
	}

	_, err = (Factory{}).Open(context.Background(), domain.ConnectionConfig{
		Provider: domain.VersionRef{ID: ProviderID, Version: "stale"}, Settings: json.RawMessage(`{}`),
	})
	if err == nil || domain.ErrorCode(err) != domain.CodeResourceVersionMismatch {
		t.Fatalf("stale provider version error = %v", err)
	}

	p := openFake(t, &fakeClient{}, time.Now(), json.RawMessage(`{}`))
	descriptor, err := p.Describe(context.Background())
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if descriptor.Ref.ID != ProviderID || descriptor.Ref.Version != ProviderVersion || len(descriptor.Capabilities) != 3 {
		t.Fatalf("descriptor identity/capabilities = %+v", descriptor)
	}
	for _, capability := range descriptor.Capabilities {
		if capability.PITLevel != domain.PITUnverified {
			t.Errorf("capability %s PIT = %s, want unverified", capability.Dataset, capability.PITLevel)
		}
	}
}

func TestDailyBarsPaginateAndNormalizeWithoutInventingPIT(t *testing.T) {
	fetchedAt := time.Date(2026, 9, 14, 4, 0, 0, 0, time.UTC)
	fake := &fakeClient{bars: map[string][]*protocol.Kline{
		"sh600000": {
			{Time: time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC), Open: 11390, High: 11500, Low: 11200, Close: 11450, Volume: 123, Amount: 4567890},
			{Time: time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC), Open: 11000, High: 11400, Low: 10900, Close: 11390, Volume: 100, Amount: 3300000},
		},
	}}
	p := openFake(t, fake, fetchedAt, json.RawMessage(`{"timeout_ms":900}`))
	request := domain.FetchRequest{
		Dataset: DatasetBar, Frequency: "daily", InstrumentIDs: []domain.ID{"sh600000"},
		Fields: []string{"open", "high", "low", "close", "volume", "amount", "unit"},
		Range:  mustInterval(t, time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)),
		Page:   domain.Page{Limit: 1},
	}
	first, err := p.Fetch(context.Background(), request)
	if err != nil {
		t.Fatalf("fetch first page: %v", err)
	}
	if first.NextCursor == "" {
		t.Fatal("first page has no resume cursor")
	}
	if first.FetchedAt != fetchedAt || first.Checksum == "" || fake.timeout != 900*time.Millisecond {
		t.Fatalf("raw page metadata = fetched_at %s checksum %q timeout %s", first.FetchedAt, first.Checksum, fake.timeout)
	}

	observations, issues, err := NewNormalizer(DatasetBar).Normalize(context.Background(), first)
	if err != nil {
		t.Fatalf("normalize first page: %v", err)
	}
	if len(observations) != 1 || len(issues) != 1 || issues[0].Code != domain.CodeQualityPITUnverified {
		t.Fatalf("normalized rows/issues = %d/%+v", len(observations), issues)
	}
	observation := observations[0]
	if observation.EventTime.Format(time.RFC3339) != "2026-09-12T00:00:00+08:00" {
		t.Errorf("event time = %s", observation.EventTime.Format(time.RFC3339))
	}
	if got := observation.Values["open"].Encoded; got != "11.390" {
		t.Errorf("open = %q, want 11.390", got)
	}
	if got := observation.Values["volume"].Encoded; got != "12300" {
		t.Errorf("volume = %q, want 12300 shares", got)
	}
	if observation.Provenance.PublishedAt != nil {
		t.Errorf("published_at = %s, want absent", observation.Provenance.PublishedAt)
	}
	if observation.Provenance.AvailableAt != fetchedAt || observation.Provenance.IngestedAt != fetchedAt {
		t.Errorf("first-seen provenance = %+v", observation.Provenance)
	}
	if len(observation.Provenance.QualityFlags) != 1 || observation.Provenance.QualityFlags[0] != "pit_unverified" {
		t.Errorf("quality flags = %v", observation.Provenance.QualityFlags)
	}

	request.Page.Cursor = first.NextCursor
	second, err := p.Fetch(context.Background(), request)
	if err != nil {
		t.Fatalf("fetch second page: %v", err)
	}
	if second.NextCursor == "" {
		t.Fatal("a full upstream page must retain a probe cursor because TDX does not return a total count")
	}
	secondRows, _, err := NewNormalizer(DatasetBar).Normalize(context.Background(), second)
	if err != nil || len(secondRows) != 1 || secondRows[0].Values["close"].Encoded != "11.390" {
		t.Fatalf("second normalized page = %+v, err %v", secondRows, err)
	}
	request.Page.Cursor = second.NextCursor
	terminal, err := p.Fetch(context.Background(), request)
	if err != nil || terminal.NextCursor != "" {
		t.Fatalf("terminal probe page cursor = %q, err %v", terminal.NextCursor, err)
	}
	terminalRows, _, err := NewNormalizer(DatasetBar).Normalize(context.Background(), terminal)
	if err != nil || len(terminalRows) != 0 {
		t.Fatalf("terminal normalized page = %+v, err %v", terminalRows, err)
	}

	if err := p.Close(); err != nil || !fake.closed {
		t.Fatalf("close = %v, fake.closed=%v", err, fake.closed)
	}
}

func TestInstrumentAndCalendarMappings(t *testing.T) {
	fetchedAt := time.Date(2026, 9, 14, 4, 0, 0, 0, time.UTC)
	fake := &fakeClient{
		codes: map[protocol.Exchange][]*protocol.Code{
			protocol.ExchangeSH: {{Code: "600000", Name: "浦发银行"}},
		},
		indexBars: []*protocol.Kline{{Time: time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)}},
	}
	p := openFake(t, fake, fetchedAt, json.RawMessage(`{}`))
	rangeAll := mustInterval(t, time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC))

	instrumentPage, err := p.Fetch(context.Background(), domain.FetchRequest{
		Dataset: DatasetInstrument, Frequency: "static", InstrumentIDs: []domain.ID{"sh600000"},
		Fields: []string{"code", "name", "market", "asset_class", "currency", "listing_from"}, Range: rangeAll,
	})
	if err != nil {
		t.Fatalf("fetch instruments: %v", err)
	}
	instruments, _, err := NewNormalizer(DatasetInstrument).Normalize(context.Background(), instrumentPage)
	if err != nil || len(instruments) != 1 {
		t.Fatalf("normalize instruments = %+v, err %v", instruments, err)
	}
	if got := instruments[0].Values["market"].Encoded; got != "XSHG" {
		t.Errorf("market = %q", got)
	}
	if got := instruments[0].Values["listing_from"].MissingReason; got != "not_provided" {
		t.Errorf("listing_from missing reason = %q", got)
	}

	calendarPage, err := p.Fetch(context.Background(), domain.FetchRequest{
		Dataset: DatasetCalendar, Frequency: "daily", InstrumentIDs: []domain.ID{"sh600000"},
		Fields: []string{"session_start", "session_end", "is_half_day"}, Range: rangeAll,
	})
	if err != nil {
		t.Fatalf("fetch calendar: %v", err)
	}
	calendar, _, err := NewNormalizer(DatasetCalendar).Normalize(context.Background(), calendarPage)
	if err != nil || len(calendar) != 1 {
		t.Fatalf("normalize calendar = %+v, err %v", calendar, err)
	}
	if got := calendar[0].EventTime.Format(time.RFC3339); got != "2026-09-11T15:00:00+08:00" {
		t.Errorf("calendar event time = %q", got)
	}
	if got := calendar[0].Values["session_start"].Encoded; got != "2026-09-11T09:30:00+08:00" {
		t.Errorf("session start = %q", got)
	}
}

func TestFetchRejectsMalformedCursorAndUnsupportedInstrument(t *testing.T) {
	p := openFake(t, &fakeClient{}, time.Now(), json.RawMessage(`{}`))
	rangeAll := mustInterval(t, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	_, err := p.Fetch(context.Background(), domain.FetchRequest{
		Dataset: DatasetBar, Frequency: "daily", InstrumentIDs: []domain.ID{"hk00700"},
		Fields: []string{"close"}, Range: rangeAll,
	})
	if err == nil || domain.ErrorCode(err) != domain.CodeValidationInvalid {
		t.Fatalf("unsupported instrument error = %v", err)
	}
	_, err = p.Fetch(context.Background(), domain.FetchRequest{
		Dataset: DatasetBar, Frequency: "daily", InstrumentIDs: []domain.ID{"sh600000"},
		Fields: []string{"close"}, Range: rangeAll, Page: domain.Page{Cursor: "not-base64"},
	})
	if err == nil || domain.ErrorCode(err) != domain.CodeValidationInvalid {
		t.Fatalf("malformed cursor error = %v", err)
	}
}

func TestLiveTDXDailyBar(t *testing.T) {
	if os.Getenv("TDX_LIVE") != "1" {
		t.Skip("set TDX_LIVE=1 to exercise the public TDX endpoint")
	}
	now := time.Now().UTC()
	provider, err := (Factory{}).Open(context.Background(), domain.ConnectionConfig{
		Provider: domain.VersionRef{ID: ProviderID, Version: ProviderVersion},
		Settings: json.RawMessage(`{"hosts":["124.71.187.122:7709"],"timeout_ms":5000}`),
	})
	if err != nil {
		t.Fatalf("open live provider: %v", err)
	}
	defer func() { _ = provider.(*Provider).Close() }()
	page, err := provider.Fetch(context.Background(), domain.FetchRequest{
		Dataset: DatasetBar, Frequency: "daily", InstrumentIDs: []domain.ID{"bj920992"}, Fields: []string{"close", "volume"},
		Range: mustInterval(t, now.AddDate(0, -2, 0), now.AddDate(0, 0, 1)), Page: domain.Page{Limit: 20},
	})
	if err != nil {
		t.Fatalf("fetch live daily bar: %v", err)
	}
	rows, issues, err := NewNormalizer(DatasetBar).Normalize(context.Background(), page)
	if err != nil {
		t.Fatalf("normalize live daily bar: %v", err)
	}
	if len(rows) == 0 || len(issues) != 1 || issues[0].Code != domain.CodeQualityPITUnverified {
		t.Fatalf("live rows/issues = %d/%+v", len(rows), issues)
	}
}
