package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"vestt/common/models/config"
)

// GetServerConfig loads the single server_config row. Returns
// sql.ErrNoRows-derived ErrNotFound when unset.
func (s *Store) GetServerConfig(ctx context.Context) (*config.ServerConfig, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT config_json FROM server_config WHERE id = 1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("server_config: %w", sql.ErrNoRows)
	}
	if err != nil {
		return nil, err
	}
	var cfg config.ServerConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, fmt.Errorf("decode server_config: %w", err)
	}
	return &cfg, nil
}

// SaveServerConfig upserts the single server_config row.
func (s *Store) SaveServerConfig(ctx context.Context, cfg *config.ServerConfig) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO server_config (id, config_json, updated_at) VALUES (1, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET config_json = excluded.config_json, updated_at = excluded.updated_at`,
		string(raw), nowUTC())
	return err
}
