package tdxprovider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	upstream "github.com/injoyai/tdx"
	"github.com/injoyai/tdx/protocol"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/ports"
)

const configSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "properties": {
    "hosts": {
      "type": "array",
      "items": {"type": "string", "minLength": 1},
      "minItems": 1,
      "uniqueItems": true,
      "description": "Optional ordered TDX 7709 endpoints. When omitted the pinned library host list is used."
    },
    "timeout_ms": {
      "type": "integer",
      "minimum": 100,
      "maximum": 60000,
      "default": 5000,
      "description": "Per-request socket timeout in milliseconds."
    }
  },
  "additionalProperties": false
}`

var shanghaiLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

type settings struct {
	Hosts     []string
	TimeoutMS int
}

type settingsJSON struct {
	Hosts     *[]string `json:"hosts"`
	TimeoutMS *int      `json:"timeout_ms"`
}

type client interface {
	GetCodeAll(protocol.Exchange) (*protocol.CodeResp, error)
	GetIndexDay(string, uint16, uint16) (*protocol.KlineResp, error)
	GetKlineDay(string, uint16, uint16) (*protocol.KlineResp, error)
	SetTimeout(time.Duration)
}

type dialFunc func([]string) (client, error)

type Factory struct {
	dial  dialFunc
	clock ports.Clock
}

func (Factory) ConfigSchema() json.RawMessage { return json.RawMessage(configSchema) }

func (f Factory) Open(ctx context.Context, cfg domain.ConnectionConfig) (ports.DataProvider, error) {
	if cfg.Provider.ID != ProviderID {
		return nil, domain.NewError(domain.CodeValidationInvalid,
			"tdx: connection provider_ref %q does not match %q", cfg.Provider.ID, ProviderID)
	}
	if cfg.Provider.Version != ProviderVersion {
		return nil, domain.NewError(domain.CodeResourceVersionMismatch,
			"tdx: provider version %q does not match %q", cfg.Provider.Version, ProviderVersion)
	}
	parsed, err := parseSettings(cfg.Settings)
	if err != nil {
		return nil, err
	}
	if f.dial == nil {
		f.dial = func(hosts []string) (client, error) {
			return upstream.DialHostsRange(hosts, upstream.WithDebug(false), upstream.WithRedial(false))
		}
	}
	if f.clock == nil {
		f.clock = ports.SystemClock{}
	}
	return &Provider{settings: parsed, dial: f.dial, clock: f.clock}, nil
}

func parseSettings(raw json.RawMessage) (settings, error) {
	var wire settingsJSON
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wire); err != nil {
		return settings{}, domain.Wrap(err, domain.CodeValidationInvalid, "tdx: invalid settings")
	}
	if err := ensureJSONEOF(dec); err != nil {
		return settings{}, err
	}
	out := settings{TimeoutMS: 5000}
	if wire.TimeoutMS != nil {
		out.TimeoutMS = *wire.TimeoutMS
	}
	if wire.Hosts != nil {
		if len(*wire.Hosts) == 0 {
			return settings{}, domain.NewError(domain.CodeValidationInvalid,
				"tdx: hosts must contain at least one endpoint when provided")
		}
		out.Hosts = append([]string(nil), (*wire.Hosts)...)
	}
	if out.TimeoutMS < 100 || out.TimeoutMS > 60000 {
		return settings{}, domain.NewError(domain.CodeValidationInvalid,
			"tdx: timeout_ms must be between 100 and 60000")
	}
	seen := make(map[string]bool, len(out.Hosts))
	for i, host := range out.Hosts {
		if strings.TrimSpace(host) != host || host == "" {
			return settings{}, domain.NewError(domain.CodeValidationInvalid,
				"tdx: hosts[%d] must be a non-empty endpoint without surrounding whitespace", i)
		}
		_, port, err := net.SplitHostPort(host)
		if err != nil || port != "7709" {
			return settings{}, domain.NewError(domain.CodeValidationInvalid,
				"tdx: hosts[%d] must use host:7709", i)
		}
		if seen[host] {
			return settings{}, domain.NewError(domain.CodeValidationInvalid,
				"tdx: hosts[%d] duplicates an earlier endpoint", i)
		}
		seen[host] = true
	}
	return out, nil
}

func ensureJSONEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return domain.NewError(domain.CodeValidationInvalid, "tdx: settings must contain one JSON object")
		}
		return domain.Wrap(err, domain.CodeValidationInvalid, "tdx: invalid settings")
	}
	return nil
}

type Provider struct {
	settings settings
	dial     dialFunc
	clock    ports.Clock

	mu     sync.Mutex
	client client
}

func (p *Provider) Describe(context.Context) (domain.ProviderDescriptor, error) {
	return domain.ProviderDescriptor{
		Ref:          domain.VersionRef{ID: ProviderID, Version: ProviderVersion},
		Name:         ProviderName,
		ConfigSchema: json.RawMessage(configSchema),
		Capabilities: []domain.Capability{
			{Dataset: DatasetInstrument, Fields: instrumentFields, Frequencies: []string{"static"}, PITLevel: domain.PITUnverified, MaxPageSize: MaxPageSize},
			{Dataset: DatasetCalendar, Fields: calendarFields, Frequencies: []string{"daily"}, PITLevel: domain.PITUnverified, MaxPageSize: MaxPageSize},
			{Dataset: DatasetBar, Fields: barFields, Frequencies: []string{"daily"}, PITLevel: domain.PITUnverified, MaxPageSize: MaxPageSize},
		},
	}, nil
}

func (p *Provider) Check(ctx context.Context) ([]domain.Issue, error) {
	c, err := p.open(ctx)
	if err != nil {
		return nil, err
	}
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	resp, err := c.GetIndexDay("sh000001", 0, 1)
	if err != nil {
		return nil, domain.Wrap(err, domain.CodeInternalUnavailable, "tdx: connectivity probe failed")
	}
	if resp == nil || len(resp.List) == 0 {
		return []domain.Issue{{
			Code: domain.CodeInternalUnavailable, Path: "connection", Severity: domain.SeverityError,
			Message: "TDX connectivity probe returned no Shanghai Composite daily bar",
		}}, nil
	}
	return []domain.Issue{{
		Code: domain.CodeQualityPITUnverified, Path: "connection", Severity: domain.SeverityWarning,
		Message: "TDX public protocol provides no authoritative publication or historical revision timestamps",
	}}, nil
}

func (p *Provider) Fetch(ctx context.Context, req domain.FetchRequest) (domain.RawPage, error) {
	if err := req.Validate(); err != nil {
		return domain.RawPage{}, err
	}
	fields := fieldsFor(req.Dataset)
	if fields == nil {
		return domain.RawPage{}, domain.NewError(domain.CodeValidationInvalid,
			"tdx: unsupported dataset %q", req.Dataset)
	}
	if err := requireFields(req.Fields, fields); err != nil {
		return domain.RawPage{}, err
	}
	if err := requireFrequency(req.Dataset, req.Frequency); err != nil {
		return domain.RawPage{}, err
	}
	if err := validateInstruments(req.InstrumentIDs); err != nil {
		return domain.RawPage{}, err
	}
	c, err := p.open(ctx)
	if err != nil {
		return domain.RawPage{}, err
	}
	fetchedAt := p.clock.Now().UTC()
	var payload rawPayload
	var next string
	switch req.Dataset {
	case DatasetInstrument:
		payload, next, err = p.fetchInstruments(ctx, c, req, fetchedAt)
	case DatasetCalendar:
		payload, next, err = p.fetchCalendar(ctx, c, req)
	case DatasetBar:
		payload, next, err = p.fetchBars(ctx, c, req)
	}
	if err != nil {
		return domain.RawPage{}, err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return domain.RawPage{}, domain.Wrap(err, domain.CodeInternalError, "tdx: encode raw page")
	}
	sum := sha256.Sum256(raw)
	return domain.RawPage{
		Payload:     raw,
		ContentType: rawContentType,
		NextCursor:  next,
		Checksum:    hex.EncodeToString(sum[:]),
		FetchedAt:   fetchedAt,
	}, nil
}

func (p *Provider) open(ctx context.Context) (client, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.client != nil {
		return p.client, nil
	}
	c, err := p.dial(p.settings.Hosts)
	if err != nil {
		return nil, domain.Wrap(err, domain.CodeInternalUnavailable, "tdx: connect failed")
	}
	c.SetTimeout(time.Duration(p.settings.TimeoutMS) * time.Millisecond)
	p.client = c
	return c, nil
}

func (p *Provider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.client == nil {
		return nil
	}
	var err error
	if closer, ok := p.client.(io.Closer); ok {
		err = closer.Close()
	}
	p.client = nil
	return err
}

type rawValue struct {
	Value         string `json:"value,omitempty"`
	MissingReason string `json:"missing_reason,omitempty"`
}

type rawRow struct {
	InstrumentID string              `json:"instrument_id,omitempty"`
	EventTime    string              `json:"event_time"`
	Values       map[string]rawValue `json:"values"`
}

type rawPayload struct {
	Dataset string   `json:"dataset"`
	Rows    []rawRow `json:"rows"`
}

type cursor struct {
	Instrument int `json:"instrument,omitempty"`
	Start      int `json:"start,omitempty"`
}

func encodeCursor(c cursor) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(raw string) (cursor, error) {
	if raw == "" {
		return cursor{}, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return cursor{}, domain.NewError(domain.CodeValidationInvalid, "tdx: malformed page cursor")
	}
	var out cursor
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil || out.Instrument < 0 || out.Start < 0 || out.Start > 65535 {
		return cursor{}, domain.NewError(domain.CodeValidationInvalid, "tdx: malformed page cursor")
	}
	if err := ensureJSONEOF(dec); err != nil {
		return cursor{}, domain.NewError(domain.CodeValidationInvalid, "tdx: malformed page cursor")
	}
	return out, nil
}

func (p *Provider) fetchInstruments(ctx context.Context, c client, req domain.FetchRequest, fetchedAt time.Time) (rawPayload, string, error) {
	if !req.Range.Contains(fetchedAt) {
		return rawPayload{Dataset: DatasetInstrument, Rows: []rawRow{}}, "", nil
	}
	wanted := make(map[string]bool, len(req.InstrumentIDs))
	for _, id := range req.InstrumentIDs {
		wanted[id.String()] = true
	}
	byID := make(map[string]*protocol.Code, len(wanted))
	for _, exchange := range requestedExchanges(req.InstrumentIDs) {
		if err := checkContext(ctx); err != nil {
			return rawPayload{}, "", err
		}
		resp, err := c.GetCodeAll(exchange)
		if err != nil {
			return rawPayload{}, "", domain.Wrap(err, domain.CodeInternalUnavailable,
				"tdx: fetch instrument list for %s", exchange.String())
		}
		for _, item := range resp.List {
			id := exchange.String() + item.Code
			if wanted[id] && protocol.IsStock(id) {
				byID[id] = item
			}
		}
	}
	rows := make([]rawRow, 0, len(req.InstrumentIDs))
	for _, requested := range req.InstrumentIDs {
		id := requested.String()
		item := byID[id]
		if item == nil {
			return rawPayload{}, "", domain.NewError(domain.CodeValidationInvalid,
				"tdx: A-share instrument %s was not present in the current code list", id)
		}
		exchange, _, _ := protocol.DecodeCode(id)
		values := map[string]rawValue{
			"code":         {Value: item.Code},
			"name":         {Value: item.Name},
			"market":       {Value: mic(exchange)},
			"asset_class":  {Value: "equity"},
			"currency":     {Value: "CNY"},
			"listing_from": {MissingReason: "not_provided"},
		}
		rows = append(rows, rawRow{InstrumentID: id, EventTime: fetchedAt.Format(time.RFC3339Nano), Values: project(values, req.Fields)})
	}
	page, next, err := sliceRows(rows, req.Page)
	return rawPayload{Dataset: DatasetInstrument, Rows: page}, next, err
}

func (p *Provider) fetchCalendar(ctx context.Context, c client, req domain.FetchRequest) (rawPayload, string, error) {
	cur, err := decodeCursor(req.Page.Cursor)
	if err != nil || cur.Instrument != 0 {
		if err == nil {
			err = domain.NewError(domain.CodeValidationInvalid, "tdx: malformed calendar cursor")
		}
		return rawPayload{}, "", err
	}
	limit := normalizedLimit(req.Page.Limit)
	resp, err := c.GetIndexDay("sh000001", uint16(cur.Start), uint16(limit))
	if err != nil {
		return rawPayload{}, "", domain.Wrap(err, domain.CodeInternalUnavailable, "tdx: fetch trading calendar")
	}
	rows := make([]rawRow, 0, len(resp.List))
	exhausted := len(resp.List) < limit
	for _, bar := range resp.List {
		if bar == nil {
			continue
		}
		eventTime := marketTime(bar.Time)
		if eventTime.Before(req.Range.From) {
			exhausted = true
			continue
		}
		if !req.Range.Contains(eventTime) {
			continue
		}
		day := eventTime
		start := time.Date(day.Year(), day.Month(), day.Day(), 9, 30, 0, 0, day.Location())
		end := time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, day.Location())
		values := map[string]rawValue{
			"session_start": {Value: start.Format(time.RFC3339)},
			"session_end":   {Value: end.Format(time.RFC3339)},
			"is_half_day":   {Value: "false"},
		}
		rows = append(rows, rawRow{EventTime: end.Format(time.RFC3339), Values: project(values, req.Fields)})
	}
	sortRows(rows)
	next := nextOffset(cur.Start, len(resp.List), exhausted)
	return rawPayload{Dataset: DatasetCalendar, Rows: rows}, next, nil
}

func (p *Provider) fetchBars(ctx context.Context, c client, req domain.FetchRequest) (rawPayload, string, error) {
	cur, err := decodeCursor(req.Page.Cursor)
	if err != nil {
		return rawPayload{}, "", err
	}
	if cur.Instrument > len(req.InstrumentIDs) {
		return rawPayload{}, "", domain.NewError(domain.CodeValidationInvalid, "tdx: page cursor is past the instrument list")
	}
	limit := normalizedLimit(req.Page.Limit)
	for cur.Instrument < len(req.InstrumentIDs) {
		if err := checkContext(ctx); err != nil {
			return rawPayload{}, "", err
		}
		id := req.InstrumentIDs[cur.Instrument].String()
		resp, err := c.GetKlineDay(id, uint16(cur.Start), uint16(limit))
		if err != nil {
			return rawPayload{}, "", domain.Wrap(err, domain.CodeInternalUnavailable, "tdx: fetch daily bars for %s", id)
		}
		rows := make([]rawRow, 0, len(resp.List))
		exhausted := len(resp.List) < limit
		for _, bar := range resp.List {
			if bar == nil {
				continue
			}
			eventTime := marketTime(bar.Time)
			if eventTime.Before(req.Range.From) {
				exhausted = true
				continue
			}
			if !req.Range.Contains(eventTime) {
				continue
			}
			values := map[string]rawValue{
				"open":   {Value: milli(int64(bar.Open))},
				"high":   {Value: milli(int64(bar.High))},
				"low":    {Value: milli(int64(bar.Low))},
				"close":  {Value: milli(int64(bar.Close))},
				"volume": {Value: strconv.FormatInt(bar.Volume*100, 10)},
				"amount": {Value: milli(int64(bar.Amount))},
				"unit":   {Value: "CNY"},
			}
			rows = append(rows, rawRow{InstrumentID: id, EventTime: eventTime.Format(time.RFC3339), Values: project(values, req.Fields)})
		}
		sortRows(rows)
		if exhausted || cur.Start+len(resp.List) > 65535 {
			cur.Instrument++
			cur.Start = 0
		} else {
			cur.Start += len(resp.List)
		}
		if len(rows) > 0 {
			next := ""
			if cur.Instrument < len(req.InstrumentIDs) {
				next = encodeCursor(cur)
			}
			return rawPayload{Dataset: DatasetBar, Rows: rows}, next, nil
		}
	}
	return rawPayload{Dataset: DatasetBar, Rows: []rawRow{}}, "", nil
}

func sliceRows(rows []rawRow, page domain.Page) ([]rawRow, string, error) {
	cur, err := decodeCursor(page.Cursor)
	if err != nil || cur.Instrument != 0 {
		return nil, "", domain.NewError(domain.CodeValidationInvalid, "tdx: malformed page cursor")
	}
	if cur.Start > len(rows) {
		return nil, "", domain.NewError(domain.CodeValidationInvalid, "tdx: page cursor is past the result set")
	}
	end := cur.Start + normalizedLimit(page.Limit)
	if end > len(rows) {
		end = len(rows)
	}
	next := ""
	if end < len(rows) {
		next = encodeCursor(cursor{Start: end})
	}
	return rows[cur.Start:end], next, nil
}

func normalizedLimit(limit int) int {
	if limit <= 0 || limit > MaxPageSize {
		return MaxPageSize
	}
	return limit
}

func nextOffset(start, received int, exhausted bool) string {
	if exhausted || received == 0 || start+received > 65535 {
		return ""
	}
	return encodeCursor(cursor{Start: start + received})
}

func project(values map[string]rawValue, fields []string) map[string]rawValue {
	out := make(map[string]rawValue, len(fields))
	for _, field := range fields {
		if value, ok := values[field]; ok {
			out[field] = value
		}
	}
	return out
}

func sortRows(rows []rawRow) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].InstrumentID != rows[j].InstrumentID {
			return rows[i].InstrumentID < rows[j].InstrumentID
		}
		return rows[i].EventTime < rows[j].EventTime
	})
}

func milli(value int64) string {
	negative := value < 0
	if negative {
		value = -value
	}
	out := fmt.Sprintf("%d.%03d", value/1000, value%1000)
	if negative {
		return "-" + out
	}
	return out
}

func marketTime(value time.Time) time.Time {
	year, month, day := value.Date()
	hour, minute, second := value.Clock()
	return time.Date(year, month, day, hour, minute, second, value.Nanosecond(), shanghaiLocation)
}

func requestedExchanges(ids []domain.ID) []protocol.Exchange {
	seen := map[protocol.Exchange]bool{}
	var out []protocol.Exchange
	for _, id := range ids {
		exchange, _, _ := protocol.DecodeCode(id.String())
		if !seen[exchange] {
			seen[exchange] = true
			out = append(out, exchange)
		}
	}
	return out
}

func validateInstruments(ids []domain.ID) error {
	for i, id := range ids {
		if _, err := domain.ParseID(id.String()); err != nil {
			return domain.Wrap(err, domain.CodeValidationInvalid, "tdx: instrument_ids[%d] invalid", i)
		}
		if _, _, err := protocol.DecodeCode(id.String()); err != nil || !protocol.IsStock(id.String()) {
			return domain.NewError(domain.CodeValidationInvalid,
				"tdx: instrument_ids[%d] %q is not a prefixed A-share stock code", i, id)
		}
	}
	return nil
}

func requireFrequency(dataset, frequency string) error {
	want := "daily"
	if dataset == DatasetInstrument {
		want = "static"
	}
	if frequency != want {
		return domain.NewError(domain.CodeValidationInvalid,
			"tdx: dataset %s supports frequency %s, got %s", dataset, want, frequency)
	}
	return nil
}

func requireFields(requested []string, schema []domain.Field) error {
	known := make(map[string]bool, len(schema))
	for _, field := range schema {
		known[field.Name] = true
	}
	for _, field := range requested {
		if !known[field] {
			return domain.NewError(domain.CodeValidationUnknownField, "tdx: unknown field %q", field)
		}
	}
	return nil
}

func mic(exchange protocol.Exchange) string {
	switch exchange {
	case protocol.ExchangeSH:
		return "XSHG"
	case protocol.ExchangeSZ:
		return "XSHE"
	case protocol.ExchangeBJ:
		return "XBEI"
	default:
		return ""
	}
}

func checkContext(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
