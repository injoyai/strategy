package synthetic

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/ports"
)

// configSchema is the JSON Schema a synthetic connection must satisfy. Beyond
// the fixture selection it carries a deterministic fault plan so retry
// semantics, rate-limit handling and job failure paths can be exercised
// without mocking the DataProvider port itself.
const configSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "properties": {
    "include_bad_data": {"type": "boolean", "default": false, "description": "When true, the provider serves the bad-data fixtures alongside the good ones so quality checks have real issues to surface."},
    "faults": {
      "type": "object",
      "properties": {
        "fail_first_n_fetches": {"type": "integer", "minimum": 0, "default": 0, "description": "The first N Fetch calls fail with a retryable upstream error (internal.unavailable)."},
        "rate_limit_after_n_fetches": {"type": "integer", "minimum": 0, "default": 0, "description": "After N successful Fetch calls, further calls fail with a retryable rate.limited error."}
      },
      "additionalProperties": false
    }
  },
  "additionalProperties": false
}`

// Factory builds synthetic providers. It is registered at startup so the HTTP
// layer can list "synthetic" as an available provider and validate connection
// settings against configSchema.
type Factory struct{}

// ConfigSchema returns the JSON Schema for a synthetic connection. The
// server never persists arbitrary settings: only values that validate against
// this schema reach Open.
func (Factory) ConfigSchema() json.RawMessage { return json.RawMessage(configSchema) }

// Open validates settings and returns a bound provider. The fixtures are
// static; the settings only select the bad-data fixtures and a fault plan.
func (Factory) Open(ctx context.Context, cfg domain.ConnectionConfig) (ports.DataProvider, error) {
	if cfg.Provider.ID != ProviderID {
		return nil, domain.NewError(domain.CodeValidationInvalid,
			"synthetic: connection provider_ref %q does not match %q",
			cfg.Provider.ID, ProviderID)
	}
	var settings struct {
		IncludeBadData bool `json:"include_bad_data"`
		Faults         struct {
			FailFirstNFetches      int `json:"fail_first_n_fetches"`
			RateLimitAfterNFetches int `json:"rate_limit_after_n_fetches"`
		} `json:"faults"`
	}
	if len(cfg.Settings) > 0 {
		dec := json.NewDecoder(bytes.NewReader(cfg.Settings))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&settings); err != nil {
			return nil, domain.Wrap(err, domain.CodeValidationInvalid,
				"synthetic: invalid settings: %v", err)
		}
	}
	if settings.Faults.FailFirstNFetches < 0 || settings.Faults.RateLimitAfterNFetches < 0 {
		return nil, domain.NewError(domain.CodeValidationInvalid,
			"synthetic: fault counts must be non-negative")
	}
	return &Provider{
		connectionRef: cfg.Ref,
		includeBad:    settings.IncludeBadData,
		failFirstN:    settings.Faults.FailFirstNFetches,
		rateAfterN:    settings.Faults.RateLimitAfterNFetches,
		clock:         ports.SystemClock{},
	}, nil
}

// Provider is a bound synthetic DataProvider. It serves fixed-seed fixtures
// deterministically; the same request always returns the same content. Force
// mode is honored at the store layer (the provider cannot tell whether the
// caller is forcing a re-fetch, but the store records a new batch either way).
//
// The fault plan (failFirstN, rateAfterN) is part of the connection settings,
// so failure behavior is deterministic for a given provider instance and call
// sequence — the property tests need to assert retry outcomes.
type Provider struct {
	connectionRef domain.VersionRef
	includeBad    bool
	failFirstN    int
	rateAfterN    int

	mu        sync.Mutex
	calls     int // Fetch calls attempted
	successes int // Fetch calls that passed the fault gate

	clock ports.Clock
}

// faultGate applies the configured fault plan and counts the call. It must be
// called at the top of Fetch: failures are injected before any work, and both
// error classes are retryable (internal.unavailable, rate.limited) so callers
// can exercise backoff and job-failure paths.
func (p *Provider) faultGate() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.failFirstN > 0 && p.calls <= p.failFirstN {
		return domain.NewError(domain.CodeInternalUnavailable,
			"synthetic: injected upstream failure %d of %d", p.calls, p.failFirstN)
	}
	if p.rateAfterN > 0 && p.successes >= p.rateAfterN {
		return domain.NewError(domain.CodeRateLimited,
			"synthetic: injected rate limit after %d fetches", p.successes)
	}
	p.successes++
	return nil
}

// Describe returns the provider's capabilities: three datasets (instrument,
// calendar, bar) with their field schemas, frequencies, coverage and
// max_page_size. Coverage is the union of fixture event times.
func (p *Provider) Describe(ctx context.Context) (domain.ProviderDescriptor, error) {
	desc := domain.ProviderDescriptor{
		Ref:          domain.VersionRef{ID: ProviderID, Version: ProviderVersion},
		Name:         ProviderName,
		ConfigSchema: json.RawMessage(configSchema),
		Capabilities: []domain.Capability{
			{
				Dataset:     DatasetInstrument,
				Fields:      instrumentFields,
				Frequencies: []string{"static"},
				Coverage:    coverageOf(fixtureInstruments),
				PITLevel:    domain.PITVerified,
				MaxPageSize: MaxPageSize,
			},
			{
				Dataset:     DatasetCalendar,
				Fields:      calendarFields,
				Frequencies: []string{"daily"},
				Coverage:    coverageOf(fixtureCalendar),
				PITLevel:    domain.PITVerified,
				MaxPageSize: MaxPageSize,
			},
			{
				Dataset:     DatasetBar,
				Fields:      barFields,
				Frequencies: []string{"daily"},
				Coverage:    coverageOf(fixtureBars),
				PITLevel:    domain.PITVerified,
				MaxPageSize: MaxPageSize,
			},
		},
	}
	return desc, nil
}

// Check is a no-op for synthetic: there is no upstream to probe. It returns
// no issues; a real provider would surface connectivity or permission issues
// without writing data.
func (p *Provider) Check(ctx context.Context) ([]domain.Issue, error) {
	return nil, nil
}

// Fetch returns one bounded page of raw observations matching the request.
// Pagination is by integer offset into the deterministic fixture slice; the
// cursor carries the offset as a decimal string. The payload is JSON so the
// normalizer can parse it without a format-specific decoder. Fetch honors
// the request range (half-open [From, To)) and instrument filter; bad-data
// fixtures are appended only when includeBadData is true (the default
// provider created from Open with include_bad_data=true).
func (p *Provider) Fetch(ctx context.Context, req domain.FetchRequest) (domain.RawPage, error) {
	if err := p.faultGate(); err != nil {
		return domain.RawPage{}, err
	}
	if err := req.Validate(); err != nil {
		return domain.RawPage{}, err
	}
	fields := datasetFields(req.Dataset)
	if fields == nil {
		return domain.RawPage{}, domain.NewError(domain.CodeValidationInvalid,
			"synthetic: unknown dataset %q", req.Dataset)
	}
	fixture := p.fixtureFor(req.Dataset)
	if fixture == nil {
		return domain.RawPage{}, domain.NewError(domain.CodeValidationInvalid,
			"synthetic: dataset %q has no fixture", req.Dataset)
	}

	filtered := make([]domain.Observation, 0, len(fixture))
	wantIDs := make(map[domain.ID]bool, len(req.InstrumentIDs))
	for _, id := range req.InstrumentIDs {
		wantIDs[id] = true
	}
	for _, obs := range fixture {
		// Calendar dataset has no instrument; include it unconditionally.
		if obs.InstrumentID != nil && !wantIDs[*obs.InstrumentID] {
			continue
		}
		if !req.Range.Contains(obs.EventTime) {
			continue
		}
		filtered = append(filtered, obs)
	}

	// Project to the requested fields only. The contract says providers
	// honor the fields selector; a field the dataset lacks is not silently
	// dropped — the normalizer reports an unknown_field issue, but the
	// payload carries what the provider actually has.
	projected := make([]pageRow, 0, len(filtered))
	for _, obs := range filtered {
		row := pageRow{
			InstrumentID: obs.InstrumentID,
			EventTime:    obs.EventTime,
			Provenance:   obs.Provenance,
			Values:       make(map[string]domain.Value, len(req.Fields)),
		}
		for _, f := range req.Fields {
			if v, ok := obs.Values[f]; ok {
				row.Values[f] = v
			}
		}
		projected = append(projected, row)
	}

	// Paginate. The cursor is the next offset; an empty cursor starts at 0.
	// A malformed or out-of-range cursor fails closed: providers must never
	// silently rewind on bad input, or a paging caller could loop forever.
	offset, err := parseCursor(req.Page.Cursor)
	if err != nil {
		return domain.RawPage{}, err
	}
	limit := req.Page.Limit
	if limit <= 0 || limit > MaxPageSize {
		limit = MaxPageSize
	}
	if offset > len(projected) {
		return domain.RawPage{}, domain.NewError(domain.CodeValidationInvalid,
			"synthetic: page cursor %q is past the end of the result set", req.Page.Cursor)
	}
	end := offset + limit
	if end > len(projected) {
		end = len(projected)
	}
	page := projected[offset:end]
	nextCursor := ""
	if end < len(projected) {
		nextCursor = fmt.Sprintf("%d", end)
	}

	payload, err := json.Marshal(struct {
		Dataset string    `json:"dataset"`
		Rows    []pageRow `json:"rows"`
	}{Dataset: req.Dataset, Rows: page})
	if err != nil {
		return domain.RawPage{}, domain.Wrap(err, domain.CodeInternalError,
			"synthetic: encode page: %v", err)
	}

	sum := sha256.Sum256(payload)
	return domain.RawPage{
		Payload:          payload,
		ContentType:      "application/json",
		NextCursor:       nextCursor,
		ProviderRevision: ProviderVersion,
		Checksum:         hex.EncodeToString(sum[:]),
		FetchedAt:        p.clock.Now(),
	}, nil
}

// fixtureFor returns the observations for one dataset, including bad-data
// bars only when the connection opted in.
func (p *Provider) fixtureFor(dataset string) []domain.Observation {
	if dataset == DatasetBar && p.includeBad {
		out := make([]domain.Observation, 0, len(fixtureBars)+len(fixtureBadBars))
		out = append(out, fixtureBars...)
		out = append(out, fixtureBadBars...)
		return out
	}
	return datasetFixture(dataset)
}

// coverageOf returns the half-open interval spanning the min and max event
// times of a fixture. Used by Describe; nil when the fixture is empty.
func coverageOf(fixture []domain.Observation) *domain.Interval {
	if len(fixture) == 0 {
		return nil
	}
	from := fixture[0].EventTime
	to := fixture[0].EventTime
	for _, obs := range fixture[1:] {
		if obs.EventTime.Before(from) {
			from = obs.EventTime
		}
		if obs.EventTime.After(to) {
			to = obs.EventTime
		}
	}
	iv, err := domain.NewInterval(from, to.Add(time.Second))
	if err != nil {
		return nil
	}
	return &iv
}

// parseCursor decodes a page cursor as a decimal offset. An empty cursor is
// the start; anything else must be a non-negative decimal integer.
func parseCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(cursor)
	if err != nil || n < 0 {
		return 0, domain.NewError(domain.CodeValidationInvalid,
			"synthetic: malformed page cursor")
	}
	return n, nil
}
