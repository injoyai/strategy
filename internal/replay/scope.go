package replay

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/injoyai/strategy/internal/domain"
)

// ReadScope is the session-derived bound every historical read is evaluated
// against. A session pins one snapshot, one decision time and one revision, so
// a read can be neither shifted into the future nor served from another
// session's or another revision's cache. Reads need no state gate: the scope
// carries the *committed* decision point, which does not move while a session is
// advancing, so a page cannot see the half state of an uncommitted step.
type ReadScope struct {
	SessionID    domain.ID
	SnapshotID   domain.ID
	SnapshotHash string
	// From is the session's configured history start. It is the lower bound of
	// a read that names no range, not a restriction on reads that name one: a
	// chart may legitimately reach before the session's first decision point.
	From     time.Time
	AsOf     time.Time
	Revision int
}

// ScopeOf derives the read bounds of one session. Every part is required and
// checked: the config must be the session's own frozen config (a session paired
// with someone else's config would silently read the wrong universe), the
// snapshot hash must be known, and the session must carry a decision point.
// Without all three there is nothing a read could be bounded by, so this fails
// closed instead of guessing.
func ScopeOf(s Session, cfg Config, snapshotHash string) (ReadScope, error) {
	if s.ID == "" {
		return ReadScope{}, domain.NewError(codeSessionMismatch, "replay scope: session id is required")
	}
	if s.ConfigHash != cfg.Hash() {
		return ReadScope{}, domain.NewError(codeSessionMismatch,
			"replay scope: session %s does not carry this config", s.ID)
	}
	if cfg.SnapshotID == "" || strings.TrimSpace(snapshotHash) == "" {
		return ReadScope{}, domain.NewError(codeConditionsMissing,
			"replay scope: session %s needs both a snapshot binding and its manifest hash", s.ID)
	}
	if s.CurrentAsOf.IsZero() {
		return ReadScope{}, domain.NewError(codeConditionsMissing,
			"replay scope: session %s has no decision point to read up to", s.ID)
	}
	return ReadScope{
		SessionID:    s.ID,
		SnapshotID:   cfg.SnapshotID,
		SnapshotHash: snapshotHash,
		From:         cfg.From.UTC(),
		AsOf:         s.CurrentAsOf.UTC(),
		Revision:     s.Revision,
	}, nil
}

// DataQuery is one session-scoped read of historical data, mirroring the
// contract's ReplayDataQuery. The client names a session and a window; it never
// names a snapshot, a decision time or an availability rule, because those come
// from the session alone.
type DataQuery struct {
	SessionID     domain.ID
	Range         *domain.Interval
	Dataset       string
	Frequency     string
	InstrumentIDs []domain.ID
	Fields        []string
}

// Resolve validates one query against the scope and returns the effective
// half-open [from, to) interval the caller must read through.
//
// A query with no range reads the session's configured history up to the
// decision point. A query that reaches past the decision point is refused
// rather than clipped: silently shortening it would let a caller believe it
// received a window it did not, which is the "prefetch the whole span and hide
// the future" failure the session boundary exists to prevent. A read that ends
// exactly at the decision point is legal — the bound is exclusive.
func (r ReadScope) Resolve(q DataQuery) (domain.Interval, error) {
	if q.SessionID != r.SessionID {
		return domain.Interval{}, domain.NewError(codeSessionMismatch,
			"replay read: query targets session %q but scope is %q", q.SessionID, r.SessionID)
	}
	if strings.TrimSpace(q.Dataset) == "" || strings.TrimSpace(q.Frequency) == "" {
		return domain.Interval{}, domain.NewError(codeQueryInvalid,
			"replay read: dataset and frequency are required")
	}
	if len(q.InstrumentIDs) == 0 || len(q.Fields) == 0 {
		return domain.Interval{}, domain.NewError(codeQueryInvalid,
			"replay read: at least one instrument and one field are required")
	}
	for _, id := range q.InstrumentIDs {
		if id == "" {
			return domain.Interval{}, domain.NewError(codeQueryInvalid, "replay read: instrument id is empty")
		}
	}
	for _, field := range q.Fields {
		if strings.TrimSpace(field) == "" {
			return domain.Interval{}, domain.NewError(codeQueryInvalid, "replay read: field name is empty")
		}
	}
	if q.Range == nil {
		iv, err := domain.NewInterval(r.From, r.AsOf)
		if err != nil {
			return domain.Interval{}, domain.NewError(codeRangeInvalid,
				"replay read: session has no history before its decision point (%s); name a range explicitly",
				r.AsOf.Format(time.RFC3339))
		}
		return iv, nil
	}
	iv, err := domain.NewInterval(q.Range.From, q.Range.To)
	if err != nil {
		return domain.Interval{}, err
	}
	if iv.To.After(r.AsOf) {
		return domain.Interval{}, domain.NewError(codeFutureRead,
			"replay read: range ends at %s, after the session decision time %s",
			iv.To.Format(time.RFC3339), r.AsOf.Format(time.RFC3339))
	}
	return iv, nil
}

// readCachePayload is the exact cache-key input for one session-scoped read.
// Snapshot hash pins the data, session id and revision separate the session's
// own reads from any other's and from an earlier step of the same session, the
// effective interval pins the window (so two ranges never share a key), and the
// computation version invalidates keys across changes in how a value is
// derived. A key that omitted any of these could return data the caller is not
// allowed to see.
type readCachePayload struct {
	SessionID          string   `json:"session_id"`
	SnapshotHash       string   `json:"snapshot_hash"`
	AsOf               int64    `json:"as_of"`
	Revision           int      `json:"revision"`
	From               int64    `json:"from"`
	To                 int64    `json:"to"`
	Dataset            string   `json:"dataset"`
	Frequency          string   `json:"frequency"`
	InstrumentIDs      []string `json:"instrument_ids"`
	Fields             []string `json:"fields"`
	ComputationVersion string   `json:"computation_version"`
}

// CacheKey returns the deterministic cache key one session-scoped read must be
// cached under. It resolves the query first, so a key can never be minted for a
// query the scope refuses, and the key covers the resolved interval rather than
// the requested one. An empty computation version is rejected: a cache that
// ignores which code derived a value would serve yesterday's result under
// today's version.
func (r ReadScope) CacheKey(q DataQuery, computationVersion string) (string, error) {
	iv, err := r.Resolve(q)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(computationVersion) == "" {
		return "", domain.NewError(codeQueryInvalid, "replay read: computation version is required for a cache key")
	}
	encoded, err := json.Marshal(readCachePayload{
		SessionID:          r.SessionID.String(),
		SnapshotHash:       r.SnapshotHash,
		AsOf:               r.AsOf.UnixNano(),
		Revision:           r.Revision,
		From:               iv.From.UnixNano(),
		To:                 iv.To.UnixNano(),
		Dataset:            q.Dataset,
		Frequency:          q.Frequency,
		InstrumentIDs:      canonicalIDs(q.InstrumentIDs),
		Fields:             canonicalStrings(q.Fields),
		ComputationVersion: computationVersion,
	})
	if err != nil {
		return "", domain.Wrap(err, domain.CodeInternalError, "replay read: encode cache key")
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

// ScreenInputs is the screening input the server derives for one in-session
// screening request. It has no time, snapshot or universe field a client could
// set: the browser names a saved screener revision and the session supplies
// everything else, which is what keeps a screen from being evaluated at a point
// the session has not reached.
type ScreenInputs struct {
	CommandID  domain.ID
	Screener   domain.VersionRef
	SnapshotID domain.ID
	Universe   domain.VersionRef
	AsOf       time.Time
	Timezone   string
	StrictPIT  bool
}

// ScreenRunInputsOf validates one in-session screening command and derives the
// inputs it will be submitted with. It is legal only while the session is
// awaiting an action — screening mid-advance would evaluate a point the session
// has not committed — and every bound comes from the session and its frozen
// config, never from the caller.
func ScreenRunInputsOf(s Session, cfg Config, cmd Command, screener domain.VersionRef) (ScreenInputs, error) {
	if err := s.gate(cmd); err != nil {
		return ScreenInputs{}, err
	}
	if cmd.Kind != CmdScreen {
		return ScreenInputs{}, domain.NewError(codeCommandKind, "unsupported screen command %q", cmd.Kind)
	}
	if s.State != StateAwaitingAction {
		return ScreenInputs{}, domain.NewError(codeStateInvalid, "screening requires awaiting_action, session is %q", s.State)
	}
	if err := screener.Validate(); err != nil {
		return ScreenInputs{}, domain.Wrap(err, codeQueryInvalid, "replay screen: screener reference invalid")
	}
	return ScreenInputs{
		CommandID:  cmd.CommandID,
		Screener:   screener,
		SnapshotID: cfg.SnapshotID,
		Universe:   cfg.UniverseRef,
		AsOf:       s.CurrentAsOf.UTC(),
		Timezone:   cfg.Timezone,
		StrictPIT:  cfg.StrictPIT,
	}, nil
}

// canonicalIDs returns the ids sorted and deduplicated so a cache key depends on
// the set the caller asked for, not on the order it happened to list them in.
func canonicalIDs(values []domain.ID) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, v.String())
	}
	sort.Strings(out)
	return dedupeStrings(out)
}

func canonicalStrings(values []string) []string {
	out := append([]string{}, values...)
	sort.Strings(out)
	return dedupeStrings(out)
}

func dedupeStrings(sorted []string) []string {
	out := sorted[:0]
	for i, v := range sorted {
		if i == 0 || v != sorted[i-1] {
			out = append(out, v)
		}
	}
	return out
}
