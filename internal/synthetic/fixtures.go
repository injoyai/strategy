// Package synthetic implements a deterministic DataProvider for M0-06. It
// serves instrument, calendar and bar datasets from fixed-seed fixtures so
// the same request always returns the same content (verifying fetch/normalize
// and quality pipelines without depending on a real market or upstream).
//
// Bad-data fixtures are deliberately embedded so the quality engine has real
// issues to surface: a duplicate natural key, an OHLC violation, a negative
// volume, a missing trading day, an unknown unit, a future available_at and a
// same-key revision. The provider never silently fixes these; it reports
// them as Issues and lets the snapshot decision reject or accept.
package synthetic

import (
	"time"

	"github.com/injoyai/strategy/internal/domain"
)

// Fixed provider identity. The version is pinned so a snapshot that
// references "synthetic@v1" stays reproducible across releases.
const (
	ProviderID      domain.ID = "synthetic"
	ProviderVersion           = "v1"
	ProviderName              = "Synthetic Data Provider"

	// Datasets served by the synthetic provider.
	DatasetInstrument = "instrument"
	DatasetCalendar   = "calendar"
	DatasetBar        = "bar"
)

// fixtureInstruments is the static, deterministic instrument list. Each
// instrument has a stable ID, market, code, asset class, currency and
// listing/delisting range. No live data is implied.
var fixtureInstruments = []domain.Observation{
	{
		InstrumentID: idPtr("INST_A"),
		Dataset:      DatasetInstrument,
		EventTime:    parseTime("2026-01-05T09:30:00Z"),
		Values: map[string]domain.Value{
			"code":         {Kind: domain.ValueString, Encoded: "000001"},
			"market":       {Kind: domain.ValueString, Encoded: "XSHE"},
			"asset_class":  {Kind: domain.ValueString, Encoded: "equity"},
			"currency":     {Kind: domain.ValueString, Encoded: "CNY"},
			"listing_from": {Kind: domain.ValueTimestamp, Encoded: "2026-01-05T09:30:00Z"},
		},
		Provenance: domain.Provenance{
			SourceID:       ProviderID,
			SourceRecordID: "inst-A",
			RevisionID:     "rev-001",
			AvailableAt:    parseTime("2026-01-05T10:00:00Z"),
			SchemaVersion:  "instrument/v1",
			PolicyVersion:  "availability/v1",
		},
	},
	{
		InstrumentID: idPtr("INST_B"),
		Dataset:      DatasetInstrument,
		EventTime:    parseTime("2026-01-05T09:30:00Z"),
		Values: map[string]domain.Value{
			"code":         {Kind: domain.ValueString, Encoded: "000002"},
			"market":       {Kind: domain.ValueString, Encoded: "XSHE"},
			"asset_class":  {Kind: domain.ValueString, Encoded: "equity"},
			"currency":     {Kind: domain.ValueString, Encoded: "CNY"},
			"listing_from": {Kind: domain.ValueTimestamp, Encoded: "2026-01-05T09:30:00Z"},
		},
		Provenance: domain.Provenance{
			SourceID:       ProviderID,
			SourceRecordID: "inst-B",
			RevisionID:     "rev-001",
			AvailableAt:    parseTime("2026-01-05T10:00:00Z"),
			SchemaVersion:  "instrument/v1",
			PolicyVersion:  "availability/v1",
		},
	},
}

// fixtureCalendar is 5 consecutive trading days (Mon–Fri, 2026-01-05 to
// 2026-01-09). It is the bar dataset's reference for "missing trading day"
// detection.
var fixtureCalendar = []domain.Observation{
	{
		Dataset:   DatasetCalendar,
		EventTime: parseTime("2026-01-05T00:00:00Z"),
		Values: map[string]domain.Value{
			"session_start": {Kind: domain.ValueTimestamp, Encoded: "2026-01-05T09:30:00Z"},
			"session_end":   {Kind: domain.ValueTimestamp, Encoded: "2026-01-05T15:00:00Z"},
			"is_half_day":   {Kind: domain.ValueBoolean, Encoded: "false"},
		},
		Provenance: domain.Provenance{
			SourceID:       ProviderID,
			SourceRecordID: "cal-2026-01-05",
			RevisionID:     "rev-001",
			AvailableAt:    parseTime("2026-01-05T00:00:00Z"),
			SchemaVersion:  "calendar/v1",
			PolicyVersion:  "availability/v1",
		},
	},
	{
		Dataset:   DatasetCalendar,
		EventTime: parseTime("2026-01-06T00:00:00Z"),
		Values: map[string]domain.Value{
			"session_start": {Kind: domain.ValueTimestamp, Encoded: "2026-01-06T09:30:00Z"},
			"session_end":   {Kind: domain.ValueTimestamp, Encoded: "2026-01-06T15:00:00Z"},
			"is_half_day":   {Kind: domain.ValueBoolean, Encoded: "false"},
		},
		Provenance: domain.Provenance{
			SourceID:       ProviderID,
			SourceRecordID: "cal-2026-01-06",
			RevisionID:     "rev-001",
			AvailableAt:    parseTime("2026-01-06T00:00:00Z"),
			SchemaVersion:  "calendar/v1",
			PolicyVersion:  "availability/v1",
		},
	},
	{
		Dataset:   DatasetCalendar,
		EventTime: parseTime("2026-01-07T00:00:00Z"),
		Values: map[string]domain.Value{
			"session_start": {Kind: domain.ValueTimestamp, Encoded: "2026-01-07T09:30:00Z"},
			"session_end":   {Kind: domain.ValueTimestamp, Encoded: "2026-01-07T15:00:00Z"},
			"is_half_day":   {Kind: domain.ValueBoolean, Encoded: "false"},
		},
		Provenance: domain.Provenance{
			SourceID:       ProviderID,
			SourceRecordID: "cal-2026-01-07",
			RevisionID:     "rev-001",
			AvailableAt:    parseTime("2026-01-07T00:00:00Z"),
			SchemaVersion:  "calendar/v1",
			PolicyVersion:  "availability/v1",
		},
	},
	{
		Dataset:   DatasetCalendar,
		EventTime: parseTime("2026-01-08T00:00:00Z"),
		Values: map[string]domain.Value{
			"session_start": {Kind: domain.ValueTimestamp, Encoded: "2026-01-08T09:30:00Z"},
			"session_end":   {Kind: domain.ValueTimestamp, Encoded: "2026-01-08T15:00:00Z"},
			"is_half_day":   {Kind: domain.ValueBoolean, Encoded: "false"},
		},
		Provenance: domain.Provenance{
			SourceID:       ProviderID,
			SourceRecordID: "cal-2026-01-08",
			RevisionID:     "rev-001",
			AvailableAt:    parseTime("2026-01-08T00:00:00Z"),
			SchemaVersion:  "calendar/v1",
			PolicyVersion:  "availability/v1",
		},
	},
	{
		Dataset:   DatasetCalendar,
		EventTime: parseTime("2026-01-09T00:00:00Z"),
		Values: map[string]domain.Value{
			"session_start": {Kind: domain.ValueTimestamp, Encoded: "2026-01-09T09:30:00Z"},
			"session_end":   {Kind: domain.ValueTimestamp, Encoded: "2026-01-09T15:00:00Z"},
			"is_half_day":   {Kind: domain.ValueBoolean, Encoded: "false"},
		},
		Provenance: domain.Provenance{
			SourceID:       ProviderID,
			SourceRecordID: "cal-2026-01-09",
			RevisionID:     "rev-001",
			AvailableAt:    parseTime("2026-01-09T00:00:00Z"),
			SchemaVersion:  "calendar/v1",
			PolicyVersion:  "availability/v1",
		},
	},
}

// fixtureBars is the deterministic bar set. Each instrument has 5 daily bars
// (2026-01-05..09). The values are picked so OHLC is internally consistent
// (low <= open,close <= high; volume positive).
var fixtureBars = []domain.Observation{
	barObs("INST_A", "2026-01-05", "10.00", "10.50", "9.90", "10.40", "1000"),
	barObs("INST_A", "2026-01-06", "10.40", "10.80", "10.30", "10.70", "1100"),
	barObs("INST_A", "2026-01-07", "10.70", "11.00", "10.60", "10.90", "1200"),
	barObs("INST_A", "2026-01-08", "10.90", "11.20", "10.80", "11.10", "1300"),
	barObs("INST_A", "2026-01-09", "11.10", "11.50", "11.00", "11.40", "1400"),
	barObs("INST_B", "2026-01-05", "20.00", "20.50", "19.90", "20.40", "2000"),
	barObs("INST_B", "2026-01-06", "20.40", "20.80", "20.30", "20.70", "2100"),
	barObs("INST_B", "2026-01-07", "20.70", "21.00", "20.60", "20.90", "2200"),
	// INST_B is missing 2026-01-08 and 2026-01-09 — a "missing trading day"
	// fixture the quality engine must surface.
}

// fixtureBadBars carries deliberately broken rows so the quality engine has
// real issues to catch. Each row violates exactly one rule so tests can
// assert which Issue fired.
var fixtureBadBars = []domain.Observation{
	// OHLC violation: high < low.
	{
		InstrumentID: idPtr("INST_A"),
		Dataset:      DatasetBar,
		EventTime:    parseTime("2026-01-05T15:00:00Z"),
		Values: map[string]domain.Value{
			"open":   {Kind: domain.ValueDecimal, Encoded: "10.00"},
			"high":   {Kind: domain.ValueDecimal, Encoded: "9.50"}, // < low
			"low":    {Kind: domain.ValueDecimal, Encoded: "10.00"},
			"close":  {Kind: domain.ValueDecimal, Encoded: "10.00"},
			"volume": {Kind: domain.ValueDecimal, Encoded: "1000"},
			"unit":   {Kind: domain.ValueString, Encoded: "CNY"},
		},
		Provenance: domain.Provenance{
			SourceID:       ProviderID,
			SourceRecordID: "bar-bad-ohlc",
			RevisionID:     "rev-001",
			AvailableAt:    parseTime("2026-01-05T15:30:00Z"),
			SchemaVersion:  "bar/v1",
			PolicyVersion:  "availability/v1",
		},
	},
	// Negative volume.
	{
		InstrumentID: idPtr("INST_A"),
		Dataset:      DatasetBar,
		EventTime:    parseTime("2026-01-06T15:00:00Z"),
		Values: map[string]domain.Value{
			"open":   {Kind: domain.ValueDecimal, Encoded: "10.40"},
			"high":   {Kind: domain.ValueDecimal, Encoded: "10.80"},
			"low":    {Kind: domain.ValueDecimal, Encoded: "10.30"},
			"close":  {Kind: domain.ValueDecimal, Encoded: "10.70"},
			"volume": {Kind: domain.ValueDecimal, Encoded: "-100"}, // negative
			"unit":   {Kind: domain.ValueString, Encoded: "CNY"},
		},
		Provenance: domain.Provenance{
			SourceID:       ProviderID,
			SourceRecordID: "bar-bad-negvol",
			RevisionID:     "rev-001",
			AvailableAt:    parseTime("2026-01-06T15:30:00Z"),
			SchemaVersion:  "bar/v1",
			PolicyVersion:  "availability/v1",
		},
	},
	// Unknown unit.
	{
		InstrumentID: idPtr("INST_A"),
		Dataset:      DatasetBar,
		EventTime:    parseTime("2026-01-07T15:00:00Z"),
		Values: map[string]domain.Value{
			"open":   {Kind: domain.ValueDecimal, Encoded: "10.70"},
			"high":   {Kind: domain.ValueDecimal, Encoded: "11.00"},
			"low":    {Kind: domain.ValueDecimal, Encoded: "10.60"},
			"close":  {Kind: domain.ValueDecimal, Encoded: "10.90"},
			"volume": {Kind: domain.ValueDecimal, Encoded: "1200"},
			"unit":   {Kind: domain.ValueString, Encoded: "ZZZ"}, // unknown
		},
		Provenance: domain.Provenance{
			SourceID:       ProviderID,
			SourceRecordID: "bar-bad-unit",
			RevisionID:     "rev-001",
			AvailableAt:    parseTime("2026-01-07T15:30:00Z"),
			SchemaVersion:  "bar/v1",
			PolicyVersion:  "availability/v1",
		},
	},
	// Future available_at: available_at is later than the fetch time.
	{
		InstrumentID: idPtr("INST_A"),
		Dataset:      DatasetBar,
		EventTime:    parseTime("2026-01-08T15:00:00Z"),
		Values: map[string]domain.Value{
			"open":   {Kind: domain.ValueDecimal, Encoded: "10.90"},
			"high":   {Kind: domain.ValueDecimal, Encoded: "11.20"},
			"low":    {Kind: domain.ValueDecimal, Encoded: "10.80"},
			"close":  {Kind: domain.ValueDecimal, Encoded: "11.10"},
			"volume": {Kind: domain.ValueDecimal, Encoded: "1300"},
			"unit":   {Kind: domain.ValueString, Encoded: "CNY"},
		},
		Provenance: domain.Provenance{
			SourceID:       ProviderID,
			SourceRecordID: "bar-bad-future",
			RevisionID:     "rev-001",
			AvailableAt:    parseTime("2026-02-01T00:00:00Z"), // future
			SchemaVersion:  "bar/v1",
			PolicyVersion:  "availability/v1",
		},
	},
	// Same-key revision: a second row for INST_A on 2026-01-05 with a new
	// revision_id. The quality engine reports the superseded revision.
	{
		InstrumentID: idPtr("INST_A"),
		Dataset:      DatasetBar,
		EventTime:    parseTime("2026-01-05T15:00:00Z"),
		Values: map[string]domain.Value{
			"open":   {Kind: domain.ValueDecimal, Encoded: "10.00"},
			"high":   {Kind: domain.ValueDecimal, Encoded: "10.60"},
			"low":    {Kind: domain.ValueDecimal, Encoded: "9.90"},
			"close":  {Kind: domain.ValueDecimal, Encoded: "10.50"},
			"volume": {Kind: domain.ValueDecimal, Encoded: "1050"},
			"unit":   {Kind: domain.ValueString, Encoded: "CNY"},
		},
		Provenance: domain.Provenance{
			SourceID:             ProviderID,
			SourceRecordID:       "bar-A-2026-01-05",
			RevisionID:           "rev-002",
			SupersedesRevisionID: "rev-001",
			AvailableAt:          parseTime("2026-01-06T09:00:00Z"),
			SchemaVersion:        "bar/v1",
			PolicyVersion:        "availability/v1",
		},
	},
}

// barObs builds one well-formed bar observation. Used only by fixtures.
func barObs(inst, date, open, high, low, close, volume string) domain.Observation {
	return domain.Observation{
		InstrumentID: idPtr(inst),
		Dataset:      DatasetBar,
		EventTime:    parseTime(date + "T15:00:00Z"),
		Values: map[string]domain.Value{
			"open":   {Kind: domain.ValueDecimal, Encoded: open},
			"high":   {Kind: domain.ValueDecimal, Encoded: high},
			"low":    {Kind: domain.ValueDecimal, Encoded: low},
			"close":  {Kind: domain.ValueDecimal, Encoded: close},
			"volume": {Kind: domain.ValueDecimal, Encoded: volume},
			"unit":   {Kind: domain.ValueString, Encoded: "CNY"},
		},
		Provenance: domain.Provenance{
			SourceID:       ProviderID,
			SourceRecordID: "bar-" + inst + "-" + date,
			RevisionID:     "rev-001",
			AvailableAt:    parseTime(date + "T15:30:00Z"),
			SchemaVersion:  "bar/v1",
			PolicyVersion:  "availability/v1",
		},
	}
}

// idPtr returns a pointer to a parsed ID; panics only if the fixture ID is
// malformed, which is a programming error caught at test time.
func idPtr(s string) *domain.ID {
	id, err := domain.ParseID(s)
	if err != nil {
		panic("synthetic: fixture id " + s + ": " + err.Error())
	}
	return &id
}

// parseTime parses a fixed RFC3339 timestamp from a fixture. Panicking on a
// bad fixture is intentional: fixtures are static, so a parse error is a
// programming bug, not a runtime failure.
func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic("synthetic: fixture time " + s + ": " + err.Error())
	}
	return t.UTC()
}

// datasetFixture returns the deterministic observation set for one dataset.
// Bad bars are not part of the base set; fixtureFor appends them only when
// the connection opts in via include_bad_data.
func datasetFixture(dataset string) []domain.Observation {
	switch dataset {
	case DatasetInstrument:
		return fixtureInstruments
	case DatasetCalendar:
		return fixtureCalendar
	case DatasetBar:
		return append([]domain.Observation{}, fixtureBars...)
	}
	return nil
}

// instrumentFields is the schema declared for the instrument dataset.
var instrumentFields = []domain.Field{
	{Name: "code", Type: domain.FieldString, Unit: "code", Nullable: false},
	{Name: "market", Type: domain.FieldString, Unit: "mic", Nullable: false},
	{Name: "asset_class", Type: domain.FieldString, Unit: "asset_class", Nullable: false},
	{Name: "currency", Type: domain.FieldString, Unit: "iso4217", Nullable: false},
	{Name: "listing_from", Type: domain.FieldTimestamp, Unit: "rfc3339", Nullable: false},
}

// calendarFields is the schema declared for the calendar dataset.
var calendarFields = []domain.Field{
	{Name: "session_start", Type: domain.FieldTimestamp, Unit: "rfc3339", Nullable: false},
	{Name: "session_end", Type: domain.FieldTimestamp, Unit: "rfc3339", Nullable: false},
	{Name: "is_half_day", Type: domain.FieldBoolean, Unit: "boolean", Nullable: false},
}

// barFields is the schema declared for the bar dataset.
var barFields = []domain.Field{
	{Name: "open", Type: domain.FieldDecimal, Unit: "price", Nullable: false},
	{Name: "high", Type: domain.FieldDecimal, Unit: "price", Nullable: false},
	{Name: "low", Type: domain.FieldDecimal, Unit: "price", Nullable: false},
	{Name: "close", Type: domain.FieldDecimal, Unit: "price", Nullable: false},
	{Name: "volume", Type: domain.FieldDecimal, Unit: "shares", Nullable: false},
	{Name: "unit", Type: domain.FieldString, Unit: "iso4217", Nullable: false},
}

// datasetFields returns the declared schema for a dataset, or nil if the
// provider does not serve it.
func datasetFields(dataset string) []domain.Field {
	switch dataset {
	case DatasetInstrument:
		return instrumentFields
	case DatasetCalendar:
		return calendarFields
	case DatasetBar:
		return barFields
	}
	return nil
}

// MaxPageSize bounds how many observations one Fetch page returns. The
// contract requires a provider to declare this; callers must not assume
// unbounded pages.
const MaxPageSize = 50
