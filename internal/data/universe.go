package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/ports"
)

// M1-06 universe persistence and resolution: immutable member-selection
// versions bound to one snapshot, resolved strictly through a DataView
// pinned to that snapshot — never through current tables, which is what
// keeps delisted instruments inside their historical samples.

const (
	defaultUniverseLimit = 50
	maxUniverseLimit     = 200

	// UniverseResolverVersion identifies the member-resolution algorithm.
	// It rides in every cache key so a future change in rule semantics can
	// never be served from caches computed by an older resolver.
	UniverseResolverVersion = "universe-resolver-v1"
)

const universeColumns = `id, name, snapshot_id, kind, definition_json, definition_hash, created_at`

func (s *Store) scanUniverse(row scanner) (domain.UniverseVersion, error) {
	var (
		uv        domain.UniverseVersion
		kind      string
		defJSON   []byte
		createdAt int64
	)
	if err := row.Scan(&uv.ID, &uv.Name, &uv.SnapshotID, &kind, &defJSON, &uv.DefinitionHash, &createdAt); err != nil {
		return domain.UniverseVersion{}, err
	}
	if err := json.Unmarshal(defJSON, &uv.Definition); err != nil {
		return domain.UniverseVersion{}, domain.Wrap(err, domain.CodeInternalError, "data: decode universe definition")
	}
	// The kind column and the JSON blob must agree: the column exists for
	// listing and debugging without decoding, so a mismatch means the row
	// was written by something other than CreateUniverseVersion.
	if uv.Definition.Kind == "" || uv.Definition.Kind != domain.UniverseKind(kind) {
		return domain.UniverseVersion{}, domain.NewError(domain.CodeInternalError, "data: universe %s definition kind mismatch", uv.ID)
	}
	uv.CreatedAt = time.Unix(0, createdAt).UTC()
	return uv, nil
}

// CreateUniverseVersion saves one immutable universe version. Creation is
// not idempotent: every call records a new version even for a byte-identical
// request, mirroring screening's save semantics — re-using a definition is a
// lookup by name, not a republish. The snapshot binding is verified up front
// so a version can never reference an unknown snapshot. The stored
// definition is the canonical form (static members sorted and deduplicated)
// and the hash is taken over that JSON alone, excluding name and snapshot,
// so it identifies the member-selection contract itself.
func (s *Store) CreateUniverseVersion(ctx context.Context, req domain.UniverseVersionRequest) (domain.UniverseVersion, error) {
	if err := req.Validate(); err != nil {
		return domain.UniverseVersion{}, err
	}
	if _, err := s.GetSnapshot(ctx, req.SnapshotID); err != nil {
		return domain.UniverseVersion{}, err
	}
	canonical := req.Definition.Canonical()
	encoded, err := marshalJSON(canonical)
	if err != nil {
		return domain.UniverseVersion{}, err
	}
	definitionHash := s.sum.Checksum(encoded)
	createdAt := s.clock.Now().UTC()
	id := s.newID("univ")
	// A single-row INSERT needs no transaction: the row is complete and
	// immutable from the moment it lands.
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO universe_versions (
			id, workspace, name, snapshot_id, kind, definition_json, definition_hash, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, workspaceDefault, req.Name, req.SnapshotID, string(canonical.Kind), encoded, definitionHash, createdAt.UnixNano()); err != nil {
		return domain.UniverseVersion{}, fmt.Errorf("data: insert universe version: %w", err)
	}
	return domain.UniverseVersion{
		ID:             id,
		Name:           req.Name,
		SnapshotID:     req.SnapshotID,
		Definition:     canonical,
		DefinitionHash: definitionHash,
		CreatedAt:      createdAt,
	}, nil
}

// GetUniverseVersion returns one universe version by id. Unknown ids are
// wire not-found errors; SQL text never leaks into HTTP.
func (s *Store) GetUniverseVersion(ctx context.Context, id domain.ID) (domain.UniverseVersion, error) {
	uv, err := s.scanUniverse(s.db.QueryRowContext(ctx, `
		SELECT `+universeColumns+`
		FROM universe_versions
		WHERE id = ? AND workspace = ?`, id, workspaceDefault))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.UniverseVersion{}, domain.NewError(domain.CodeResourceNotFound, "data: universe version %s not found", id)
	}
	if err != nil {
		return domain.UniverseVersion{}, fmt.Errorf("data: get universe version: %w", err)
	}
	return uv, nil
}

// ListUniverseVersions lists universe versions with the same id-only keyset
// semantics as ListSnapshots; Q is a substring filter on the name.
func (s *Store) ListUniverseVersions(ctx context.Context, f ports.UniverseFilter) (domain.PageResult[domain.UniverseVersion], error) {
	asc := f.Sort == sortID
	limit := clampLimit(f.Limit, defaultUniverseLimit, maxUniverseLimit)

	query := new(strings.Builder)
	query.WriteString(`
		SELECT ` + universeColumns + `
		FROM universe_versions
		WHERE workspace = ?`)
	args := []any{workspaceDefault}
	if f.Q != "" {
		query.WriteString(` AND name LIKE ? ESCAPE '\'`)
		args = append(args, "%"+escapeLike(f.Q)+"%")
	}
	if f.AfterID != "" {
		if asc {
			query.WriteString(` AND id > ?`)
		} else {
			query.WriteString(` AND id < ?`)
		}
		args = append(args, f.AfterID)
	}
	if asc {
		query.WriteString(` ORDER BY id ASC`)
	} else {
		query.WriteString(` ORDER BY id DESC`)
	}
	query.WriteString(` LIMIT ?`)
	args = append(args, limit+1)

	rs, err := s.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return domain.PageResult[domain.UniverseVersion]{}, fmt.Errorf("data: list universe versions: %w", err)
	}
	defer rs.Close()

	items := make([]domain.UniverseVersion, 0)
	for rs.Next() {
		uv, err := s.scanUniverse(rs)
		if err != nil {
			return domain.PageResult[domain.UniverseVersion]{}, fmt.Errorf("data: scan universe version: %w", err)
		}
		items = append(items, uv)
	}
	if err := rs.Err(); err != nil {
		return domain.PageResult[domain.UniverseVersion]{}, fmt.Errorf("data: iterate universe versions: %w", err)
	}

	next := ""
	if len(items) > limit {
		items = items[:limit]
		next = items[len(items)-1].ID.String()
	}
	return domain.PageResult[domain.UniverseVersion]{Items: items, NextCursor: next}, nil
}

// ResolveUniverse produces the member list of one universe version at the
// view's decision time. The view carries both the snapshot and the as_of,
// so the resolver structurally cannot mix membership from one snapshot with
// decision data from another, and it never touches current database tables.
// Static versions return the canonical member list — resolution is
// as_of-independent by construction, which is exactly the "静态研究口径"
// contract. historical_rule versions resolve the rule's dataset through the
// view's PIT filters, which keeps delisted and later-removed instruments
// inside their historical samples; an empty result is a valid membership,
// not an error.
func ResolveUniverse(ctx context.Context, uv domain.UniverseVersion, view ports.DataView) ([]domain.ID, error) {
	if view == nil {
		return nil, domain.NewError(domain.CodeValidationInvalid, "universe: resolver requires a data view")
	}
	if err := uv.Definition.Validate(); err != nil {
		return nil, err
	}
	if uv.SnapshotID != view.SnapshotID() {
		return nil, domain.NewError(domain.CodeValidationInvalid, "universe: version %s is bound to snapshot %s, not %s", uv.ID, uv.SnapshotID, view.SnapshotID())
	}
	if uv.Definition.Kind == domain.UniverseStatic {
		return uv.Definition.CanonicalMembers(), nil
	}
	return view.DatasetInstruments(ctx, uv.Definition.Rule.Dataset, uv.Definition.Rule.Frequency)
}

// universeCachePayload is the exact cache-key input for one resolved
// membership. Snapshot hash plus definition hash pin the content, the
// universe id separates same-definition versions, as_of pins the decision
// time, and the resolver version invalidates caches across rule-semantics
// changes. Membership has no range dimension — it is a state at as_of — so
// factor caches (M1-07) build their range-scoped keys on top of this one.
type universeCachePayload struct {
	ResolverVersion string `json:"resolver_version"`
	SnapshotHash    string `json:"snapshot_hash"`
	UniverseID      string `json:"universe_id"`
	DefinitionHash  string `json:"definition_hash"`
	AsOf            int64  `json:"as_of"`
}

// UniverseCacheKey derives the deterministic cache key for one resolved
// universe membership at one decision time. The snapshot hash comes from
// the snapshot the version is bound to, so views on different snapshots
// never share a key even for byte-identical definitions. A zero as_of is a
// programming error and rejected rather than hashed.
func (s *Store) UniverseCacheKey(ctx context.Context, uv domain.UniverseVersion, asOf time.Time) (string, error) {
	if asOf.IsZero() {
		return "", domain.NewError(domain.CodeValidationInvalid, "universe: cache key as_of is required")
	}
	snap, err := s.GetSnapshot(ctx, uv.SnapshotID)
	if err != nil {
		return "", err
	}
	encoded, err := marshalJSON(universeCachePayload{
		ResolverVersion: UniverseResolverVersion,
		SnapshotHash:    snap.ManifestHash,
		UniverseID:      uv.ID.String(),
		DefinitionHash:  uv.DefinitionHash,
		AsOf:            asOf.UTC().UnixNano(),
	})
	if err != nil {
		return "", err
	}
	return s.sum.Checksum(encoded), nil
}
