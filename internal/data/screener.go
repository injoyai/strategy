package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/ports"
	"github.com/injoyai/strategy/internal/screening"
)

// M1S S2 screener persistence: immutable screening rule versions. Saving is
// not idempotent — every call records a new revision, mirroring universe
// versions — and the stored payload is the canonical wire definition, so what
// the API returns is byte-for-byte what was hashed at save time.

const (
	defaultScreenerLimit = 50
	maxScreenerLimit     = 200
)

const screenerColumns = `id, version, name, description, parent_id, rule_schema_version, definition_json, definition_hash, created_at`

// screenerKey is the list cursor key for one (id, version) row. "@" cannot
// appear in an identifier (domain.ParseID), so the composite is unambiguous.
func screenerKey(id, version string) string { return id + "@" + version }

func splitScreenerKey(key string) (string, string) {
	id, version, found := strings.Cut(key, "@")
	if !found {
		return key, ""
	}
	return id, version
}

func (s *Store) scanScreener(row scanner) (screening.Version, error) {
	var (
		v         screening.Version
		payload   []byte
		createdAt int64
	)
	if err := row.Scan(&v.ID, &v.Version, &v.Name, &v.Description, &v.ParentID,
		&v.RuleSchemaVersion, &payload, &v.DefinitionHash, &createdAt); err != nil {
		return screening.Version{}, err
	}
	if err := json.Unmarshal(payload, &v.Definition); err != nil {
		return screening.Version{}, domain.Wrap(err, domain.CodeInternalError, "data: decode screener definition")
	}
	v.CreatedAt = time.Unix(0, createdAt).UTC()
	return v, nil
}

// CreateScreenerVersion saves one immutable screener version. A request
// without parent_id starts a new screener identity at revision v1; a request
// with parent_id records a new revision of that identity. The revision
// number is derived inside the write transaction from the versions of that
// identity, so two concurrent saves cannot mint the same revision.
func (s *Store) CreateScreenerVersion(ctx context.Context, req screening.VersionRequest) (screening.Version, error) {
	if err := req.Validate(); err != nil {
		return screening.Version{}, err
	}
	payload := screening.WireDefinitionOf(req.Definition)
	encoded, err := marshalJSON(payload)
	if err != nil {
		return screening.Version{}, err
	}
	definitionHash := s.sum.Checksum(encoded)
	createdAt := s.clock.Now().UTC()

	var out screening.Version
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		id := req.ParentID
		version := domain.ID("v1")
		if id == "" {
			id = s.newID("scr")
		} else {
			var exists int
			if err := tx.QueryRowContext(ctx, `
				SELECT COUNT(*) FROM screener_versions
				WHERE workspace = ? AND id = ?`, workspaceDefault, id).Scan(&exists); err != nil {
				return fmt.Errorf("data: count screener versions: %w", err)
			}
			if exists == 0 {
				return domain.NewError(domain.CodeResourceNotFound, "data: screener %s not found", id)
			}
			var maxVersion int64
			// Versions are only ever written as "v<N>" by this method, so the
			// numeric suffix is the revision order.
			if err := tx.QueryRowContext(ctx, `
				SELECT COALESCE(MAX(CAST(SUBSTR(version, 2) AS INTEGER)), 0)
				FROM screener_versions
				WHERE workspace = ? AND id = ?`, workspaceDefault, id).Scan(&maxVersion); err != nil {
				return fmt.Errorf("data: read screener revision: %w", err)
			}
			version = domain.ID("v" + strconv.FormatInt(maxVersion+1, 10))
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO screener_versions (
				id, workspace, version, name, description, parent_id,
				rule_schema_version, definition_json, definition_hash, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, workspaceDefault, version, req.Name, req.Description, req.ParentID,
			screening.RuleSchemaVersion, encoded, definitionHash, createdAt.UnixNano()); err != nil {
			return fmt.Errorf("data: insert screener version: %w", err)
		}
		out = screening.Version{
			ID:                id,
			Version:           version,
			Name:              req.Name,
			Description:       req.Description,
			ParentID:          req.ParentID,
			RuleSchemaVersion: screening.RuleSchemaVersion,
			Definition:        payload,
			DefinitionHash:    definitionHash,
			CreatedAt:         createdAt,
		}
		return nil
	})
	if err != nil {
		return screening.Version{}, err
	}
	return out, nil
}

// GetScreenerVersion returns one immutable revision. Both parts of the
// address are required: a screener identity alone does not name a frozen rule
// set, and guessing the latest revision would make a run unreproducible.
func (s *Store) GetScreenerVersion(ctx context.Context, id, version domain.ID) (screening.Version, error) {
	if id == "" {
		return screening.Version{}, domain.NewError(domain.CodeValidationInvalid, "data: screener id is required")
	}
	if version == "" {
		return screening.Version{}, domain.NewError(domain.CodeValidationInvalid, "data: screener version is required")
	}
	v, err := s.scanScreener(s.db.QueryRowContext(ctx, `
		SELECT `+screenerColumns+`
		FROM screener_versions
		WHERE workspace = ? AND id = ? AND version = ?`, workspaceDefault, id, version))
	if errors.Is(err, sql.ErrNoRows) {
		return screening.Version{}, domain.NewError(domain.CodeResourceNotFound, "data: screener %s@%s not found", id, version)
	}
	if err != nil {
		return screening.Version{}, fmt.Errorf("data: get screener version: %w", err)
	}
	return v, nil
}

// ListScreenerVersions lists immutable revisions ordered by (id, version),
// the same keyset semantics the other list APIs use; Q is a substring filter
// on the name. Every revision of an identity is its own row, so a screener
// appears once per saved revision.
func (s *Store) ListScreenerVersions(ctx context.Context, f ports.ScreenerFilter) (domain.PageResult[screening.Version], error) {
	asc := f.Sort == sortID
	limit := clampLimit(f.Limit, defaultScreenerLimit, maxScreenerLimit)

	query := new(strings.Builder)
	query.WriteString(`
		SELECT ` + screenerColumns + `
		FROM screener_versions
		WHERE workspace = ?`)
	args := []any{workspaceDefault}
	if f.Q != "" {
		query.WriteString(` AND name LIKE ? ESCAPE '\'`)
		args = append(args, "%"+escapeLike(f.Q)+"%")
	}
	if f.AfterID != "" {
		id, version := splitScreenerKey(f.AfterID)
		if asc {
			query.WriteString(` AND (id > ? OR (id = ? AND version > ?))`)
		} else {
			query.WriteString(` AND (id < ? OR (id = ? AND version < ?))`)
		}
		args = append(args, id, id, version)
	}
	if asc {
		query.WriteString(` ORDER BY id ASC, version ASC`)
	} else {
		query.WriteString(` ORDER BY id DESC, version DESC`)
	}
	query.WriteString(` LIMIT ?`)
	args = append(args, limit+1)

	rs, err := s.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return domain.PageResult[screening.Version]{}, fmt.Errorf("data: list screener versions: %w", err)
	}
	defer rs.Close()

	items := make([]screening.Version, 0)
	for rs.Next() {
		v, err := s.scanScreener(rs)
		if err != nil {
			return domain.PageResult[screening.Version]{}, fmt.Errorf("data: scan screener version: %w", err)
		}
		items = append(items, v)
	}
	if err := rs.Err(); err != nil {
		return domain.PageResult[screening.Version]{}, fmt.Errorf("data: iterate screener versions: %w", err)
	}

	next := ""
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		next = screenerKey(last.ID.String(), last.Version.String())
	}
	return domain.PageResult[screening.Version]{Items: items, NextCursor: next}, nil
}
