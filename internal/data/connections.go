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
)

const (
	defaultConnectionLimit = 50
	maxConnectionLimit     = 200
)

const connectionColumns = `id, parent_id, version, settings, secret_ref, created_at`

type connectionRecord struct {
	Name     string            `json:"name"`
	Provider domain.VersionRef `json:"provider"`
	Settings json.RawMessage   `json:"settings"`
}

func (s *Store) CreateConnection(ctx context.Context, cfg domain.ConnectionConfig) (domain.Connection, error) {
	if err := cfg.Validate(); err != nil {
		return domain.Connection{}, err
	}
	settings, err := marshalJSON(connectionRecord{Name: cfg.Name, Provider: cfg.Provider, Settings: cfg.Settings})
	if err != nil {
		return domain.Connection{}, err
	}
	id := s.newID("conn")
	createdAt := s.clock.Now().UTC()

	if cfg.ParentID == "" {
		_, err = s.db.ExecContext(ctx, `
			INSERT INTO provider_connection_versions (id, workspace, parent_id, version, settings, secret_ref, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, workspaceDefault, id, 1, settings, cfg.SecretRef, createdAt.UnixNano())
		if err != nil {
			return domain.Connection{}, connectionWriteError(err)
		}
		return s.GetConnection(ctx, id)
	}

	root, err := s.connectionRoot(ctx, cfg.ParentID)
	if err != nil {
		return domain.Connection{}, err
	}
	version := 0
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		var head int
		if err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(MAX(version), 0)
			FROM provider_connection_versions
			WHERE workspace = ? AND parent_id = ?`, workspaceDefault, root).Scan(&head); err != nil {
			return fmt.Errorf("data: read connection head: %w", err)
		}
		version = head + 1
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO provider_connection_versions (id, workspace, parent_id, version, settings, secret_ref, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, workspaceDefault, root, version, settings, cfg.SecretRef, createdAt.UnixNano()); err != nil {
			return fmt.Errorf("data: insert connection version: %w", err)
		}
		return nil
	})
	if err != nil {
		return domain.Connection{}, connectionWriteError(err)
	}
	return domain.Connection{
		ID:        id,
		Version:   strconv.Itoa(version),
		ParentID:  domain.ID(root),
		Name:      cfg.Name,
		Provider:  cfg.Provider,
		Settings:  cfg.Settings,
		SecretRef: cfg.SecretRef,
		CreatedAt: createdAt,
	}, nil
}

func connectionWriteError(err error) error {
	if strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return domain.NewError(domain.CodeResourceConflict,
			"connection: concurrent version write, retry against the current head")
	}
	return fmt.Errorf("data: write connection: %w", err)
}

func (s *Store) connectionRoot(ctx context.Context, id string) (string, error) {
	var root string
	err := s.db.QueryRowContext(ctx, `
		SELECT parent_id
		FROM provider_connection_versions
		WHERE workspace = ? AND (id = ? OR parent_id = ?)
		ORDER BY version DESC
		LIMIT 1`, workspaceDefault, id, id).Scan(&root)
	if errors.Is(err, sql.ErrNoRows) {
		return "", domain.NewError(domain.CodeResourceNotFound, "data: connection %s not found", id)
	}
	if err != nil {
		return "", fmt.Errorf("data: resolve connection root: %w", err)
	}
	return root, nil
}

func scanConnection(row scanner) (domain.Connection, error) {
	var (
		c         domain.Connection
		rec       connectionRecord
		raw       []byte
		version   int
		createdAt int64
	)
	if err := row.Scan(&c.ID, &c.ParentID, &version, &raw, &c.SecretRef, &createdAt); err != nil {
		return domain.Connection{}, err
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		return domain.Connection{}, domain.Wrap(err, domain.CodeInternalError, "data: decode connection record")
	}
	c.Version = strconv.Itoa(version)
	c.Name = rec.Name
	c.Provider = rec.Provider
	c.Settings = rec.Settings
	c.CreatedAt = time.Unix(0, createdAt).UTC()
	if c.ParentID == c.ID {
		c.ParentID = ""
	}
	return c, nil
}

func (s *Store) GetConnection(ctx context.Context, id domain.ID) (domain.Connection, error) {
	root, err := s.connectionRoot(ctx, id.String())
	if err != nil {
		return domain.Connection{}, err
	}
	c, err := scanConnection(s.db.QueryRowContext(ctx, `
		SELECT `+connectionColumns+`
		FROM provider_connection_versions
		WHERE workspace = ? AND parent_id = ?
		ORDER BY version DESC
		LIMIT 1`, workspaceDefault, root))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Connection{}, domain.NewError(domain.CodeResourceNotFound, "data: connection %s not found", id)
	}
	if err != nil {
		return domain.Connection{}, fmt.Errorf("data: get connection: %w", err)
	}
	return c, nil
}

func (s *Store) ListConnections(ctx context.Context, q, sort, afterID string, limit int) (domain.PageResult[domain.Connection], error) {
	asc := sort == sortID
	limit = clampLimit(limit, defaultConnectionLimit, maxConnectionLimit)

	query := new(strings.Builder)
	query.WriteString(`
		SELECT ` + connectionColumns + `
		FROM provider_connection_versions
		WHERE workspace = ?
		AND version = (
			SELECT MAX(pc.version)
			FROM provider_connection_versions pc
			WHERE pc.workspace = provider_connection_versions.workspace
			AND pc.parent_id = provider_connection_versions.parent_id
		)`)
	args := []any{workspaceDefault}
	if q != "" {
		pattern := "%" + escapeLike(q) + "%"
		query.WriteString(` AND (id LIKE ? ESCAPE '\' OR CAST(settings AS TEXT) LIKE ? ESCAPE '\')`)
		args = append(args, pattern, pattern)
	}
	if afterID != "" {
		if asc {
			query.WriteString(` AND id > ?`)
		} else {
			query.WriteString(` AND id < ?`)
		}
		args = append(args, afterID)
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
		return domain.PageResult[domain.Connection]{}, fmt.Errorf("data: list connections: %w", err)
	}
	defer rs.Close()

	items := make([]domain.Connection, 0)
	for rs.Next() {
		c, err := scanConnection(rs)
		if err != nil {
			return domain.PageResult[domain.Connection]{}, fmt.Errorf("data: scan connection: %w", err)
		}
		items = append(items, c)
	}
	if err := rs.Err(); err != nil {
		return domain.PageResult[domain.Connection]{}, fmt.Errorf("data: iterate connections: %w", err)
	}

	next := ""
	if len(items) > limit {
		items = items[:limit]
		next = items[len(items)-1].ID.String()
	}
	return domain.PageResult[domain.Connection]{Items: items, NextCursor: next}, nil
}
