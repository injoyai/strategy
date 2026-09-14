// Package analysis turns factor frames plus caller-computed
// future-return labels into the D6 factor-evidence artifact: per-date
// coverage, missing-reason, distribution and correlation series,
// quantile group returns, horizon decay and train/validation/test
// segment accounting (m1-data-factor.md §7).
//
// The package is pure computation: no storage, no network, no wall
// clock. Determinism of the artifact is a property, not an accident —
// dates, horizons and buckets are ordered, and maps reach JSON only
// through encoding/json's sorted-key marshaling. Labels are the only
// future information it ever sees, and they enter exclusively through
// the explicit LabelSet input (§6.2 label isolation); the import
// allowlist in architecture_test.go structurally bans the data stack so
// that boundary cannot regress silently.
package analysis

import (
	"sort"
	"time"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/factor"
	"github.com/injoyai/strategy/internal/ports"
)

// Engine turns factor frames plus labels into sealed analysis
// artifacts. It borrows the factor registry for fail-closed parameter
// canonicalization and definition hashing, and the checksummer that
// seals the artifact.
type Engine struct {
	registry    *factor.Registry
	checksummer ports.Checksummer
}

// NewEngine binds the registry and checksummer; both are required.
func NewEngine(registry *factor.Registry, checksummer ports.Checksummer) (*Engine, error) {
	if registry == nil {
		return nil, domain.NewError(domain.CodeValidationInvalid, "analysis: factor registry is required")
	}
	if checksummer == nil {
		return nil, domain.NewError(domain.CodeValidationInvalid, "analysis: checksummer is required")
	}
	return &Engine{registry: registry, checksummer: checksummer}, nil
}

// CrossSection is one decision date's factor frame. Frames come from
// the factor engine's Run (AsOf = decision time); analysis never
// recomputes factors, it only consumes published frames.
type CrossSection struct {
	Time  time.Time
	Frame *factor.Frame
}

// Request is one analysis run: the fixed configuration, the frame
// series and the label sets. Future information enters exclusively
// through Labels (§6.2 label isolation).
type Request struct {
	Config Config
	Series []CrossSection
	Labels map[int]LabelSet
}

// Run validates the request fail-closed, computes the per-date series
// and the summary, and seals the canonical artifact with its checksum.
// Factor-side errors (unregistered ref, rejected params) pass through
// with their factor.* codes and precise messages — wrapping them would
// discard the cause on the wire (domain.Error renders Message only).
func (e *Engine) Run(req Request) (*Artifact, error) {
	cfg := req.Config
	if err := cfg.Ref.Validate(); err != nil {
		return nil, domain.Wrap(err, codeRequestInvalid, "analysis: config ref invalid")
	}
	if cfg.LabelPrice == "" || cfg.LabelEntry == "" || cfg.LabelCost == "" {
		return nil, domain.NewError(codeRequestInvalid, "analysis: label price, entry and cost must be fixed explicitly")
	}
	if cfg.Method != MethodPearson && cfg.Method != MethodSpearman {
		return nil, domain.NewError(codeRequestInvalid, "analysis: method must be %q or %q", MethodPearson, MethodSpearman)
	}
	if cfg.Groups < 2 || cfg.Groups > 10 {
		return nil, domain.NewError(codeRequestInvalid, "analysis: groups must be between 2 and 10, got %d", cfg.Groups)
	}
	if cfg.MinSamples < 2 {
		return nil, domain.NewError(codeRequestInvalid, "analysis: min samples must be at least 2, got %d", cfg.MinSamples)
	}
	window, err := domain.NewInterval(cfg.Range.From, cfg.Range.To)
	if err != nil {
		return nil, domain.Wrap(err, codeRequestInvalid, "analysis: range invalid")
	}
	segments, err := validateSegments(cfg.Segments, window)
	if err != nil {
		return nil, err
	}
	params, err := e.registry.CanonicalParams(cfg.Ref, cfg.Params)
	if err != nil {
		return nil, err
	}
	definitionHash, err := e.registry.DefinitionHash(cfg.Ref)
	if err != nil {
		return nil, err
	}
	series, err := normalizeSeries(req.Series, cfg.Ref, window)
	if err != nil {
		return nil, err
	}
	labels, err := normalizeLabels(req.Labels, window)
	if err != nil {
		return nil, err
	}
	horizons := sortedHorizons(labels)

	acc := newRun(cfg, horizons, labels, segments)
	for i := range series {
		acc.addDate(series[i])
	}

	artifact := &Artifact{
		SchemaVersion: artifactSchemaVersion,
		Config: ArtifactConfig{
			Ref:            cfg.Ref,
			DefinitionHash: definitionHash,
			Params:         params,
			LabelPrice:     cfg.LabelPrice,
			LabelEntry:     cfg.LabelEntry,
			LabelCost:      cfg.LabelCost,
			Horizons:       horizons,
			Method:         cfg.Method,
			Groups:         cfg.Groups,
			MinSamples:     cfg.MinSamples,
			Range:          window,
			Segments:       segments,
		},
		Series:       acc.daily,
		Summary:      acc.summary(),
		EvidenceNote: EvidenceNote,
	}
	if err := artifact.seal(e.checksummer); err != nil {
		return nil, err
	}
	return artifact, nil
}

// normalizeSeries sorts the cross-sections by decision time and checks
// the frame contract fail-closed: the frame must belong to the
// configured factor, its AsOf must equal the cross-section time (a
// misaligned pair silently changes label joins), every date must be
// inside the analysis window, dates must be unique, and no member may
// carry both a value and a missing reason (the Frame invariant). Frame
// completeness (every member in exactly one map) cannot be re-derived
// here without the member set — that stays the factor engine's gate.
func normalizeSeries(series []CrossSection, ref factor.FactorRef, window domain.Interval) ([]CrossSection, error) {
	if len(series) == 0 {
		return nil, domain.NewError(codeSeriesInvalid, "analysis: at least one cross-section is required")
	}
	out := make([]CrossSection, len(series))
	copy(out, series)
	sort.Slice(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	for i := range out {
		frame := out[i].Frame
		if frame == nil {
			return nil, domain.NewError(codeSeriesInvalid, "analysis: cross-section %d has a nil frame", i)
		}
		if frame.Ref != ref {
			return nil, domain.NewError(codeSeriesInvalid, "analysis: frame ref %s does not match the configured factor %s", frame.Ref, ref)
		}
		if !out[i].Time.Equal(frame.AsOf) {
			return nil, domain.NewError(codeSeriesInvalid, "analysis: cross-section time %s does not equal the frame's as_of %s",
				out[i].Time.UTC().Format(time.RFC3339Nano), frame.AsOf.UTC().Format(time.RFC3339Nano))
		}
		if i > 0 && !out[i].Time.After(out[i-1].Time) {
			return nil, domain.NewError(codeSeriesInvalid, "analysis: duplicate decision time %s", out[i].Time.UTC().Format(time.RFC3339Nano))
		}
		out[i].Time = out[i].Time.UTC() // stable artifact dates
		if !window.Contains(out[i].Time) {
			return nil, domain.NewError(codeSeriesInvalid, "analysis: decision time %s is outside the analysis range", out[i].Time.Format(time.RFC3339Nano))
		}
		for id := range frame.Values {
			if _, has := frame.Missing[id]; has {
				return nil, domain.NewError(codeSeriesInvalid, "analysis: frame at %s has member %s in both values and missing",
					out[i].Time.Format(time.RFC3339Nano), id)
			}
		}
	}
	return out, nil
}
