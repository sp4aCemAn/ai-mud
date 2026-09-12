package storage

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Relational is the PostgreSQL-backed RelationalStore — the schema'd
// side of the storage layer.
type Relational struct {
	pool *pgxpool.Pool
}

// connectPostgres opens the pool (with startup retries) and migrates.
func connectPostgres(ctx context.Context, dsn string, cfg Config) (*Relational, error) {
	var pool *pgxpool.Pool
	err := retry(ctx, "postgres", cfg.MaxRetries, cfg.RetryWait, func() error {
		p, err := pgxpool.New(ctx, dsn)
		if err != nil {
			return err
		}
		if err := p.Ping(ctx); err != nil {
			p.Close()
			return err
		}
		pool = p
		return nil
	})
	if err != nil {
		return nil, wrap("postgres", err)
	}

	r := &Relational{pool: pool}
	if err := r.migrate(ctx); err != nil {
		r.Close()
		return nil, err
	}
	return r, nil
}

// migrate creates the tables the relational side owns. Idempotent.
// A real migration tool (goose/golang-migrate) can take over later —
// this stays honest until the schema grows past a couple of tables.
func (r *Relational) migrate(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS generations (
			id          BIGSERIAL PRIMARY KEY,
			kind        TEXT NOT NULL,
			model       TEXT NOT NULL DEFAULT '',
			prompt      TEXT NOT NULL DEFAULT '',
			output      TEXT NOT NULL DEFAULT '',
			created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		CREATE INDEX IF NOT EXISTS idx_generations_kind_created
			ON generations (kind, created_at DESC);

		CREATE TABLE IF NOT EXISTS worlds (
			id         BIGSERIAL PRIMARY KEY,
			name       TEXT UNIQUE NOT NULL,
			seed       BIGINT NOT NULL,
			ww         INT NOT NULL,
			wh         INT NOT NULL,
			spawn_x    INT NOT NULL DEFAULT 0,
			spawn_y    INT NOT NULL DEFAULT 0,
			is_active  BOOL NOT NULL DEFAULT false,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_worlds_one_active
			ON worlds (is_active) WHERE is_active;

		CREATE TABLE IF NOT EXISTS world_objects (
			id         BIGSERIAL PRIMARY KEY,
			world_id   BIGINT NOT NULL REFERENCES worlds(id) ON DELETE CASCADE,
			kind       TEXT NOT NULL,
			name       TEXT NOT NULL DEFAULT '',
			seed       BIGINT NOT NULL DEFAULT 0,
			home_x     INT NOT NULL DEFAULT 0,
			home_y     INT NOT NULL DEFAULT 0,
			radius     INT NOT NULL DEFAULT 0,
			tiles      JSONB NOT NULL DEFAULT '[]',
			data       JSONB NOT NULL DEFAULT '{}',
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		CREATE INDEX IF NOT EXISTS idx_world_objects_world
			ON world_objects (world_id, kind);

		CREATE TABLE IF NOT EXISTS object_state (
			object_id  BIGINT PRIMARY KEY REFERENCES world_objects(id) ON DELETE CASCADE,
			cur_state  JSONB NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);

		CREATE TABLE IF NOT EXISTS world_snapshots (
			id         BIGSERIAL PRIMARY KEY,
			world_id   BIGINT NOT NULL REFERENCES worlds(id) ON DELETE CASCADE,
			label      TEXT NOT NULL DEFAULT '',
			terrain    TEXT NOT NULL,
			meta       JSONB NOT NULL DEFAULT '{}',
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
	`)
	if err != nil {
		return fmt.Errorf("storage: postgres migrate: %w", err)
	}
	return nil
}

func (r *Relational) Ping(ctx context.Context) error {
	if r == nil || r.pool == nil {
		return ErrNotAvailable
	}
	return r.pool.Ping(ctx)
}

func (r *Relational) SaveGeneration(ctx context.Context, g Generation) (Generation, error) {
	if r == nil || r.pool == nil {
		return Generation{}, ErrNotAvailable
	}
	err := r.pool.QueryRow(ctx,
		`INSERT INTO generations (kind, model, prompt, output) VALUES ($1, $2, $3, $4)
		 RETURNING id, created_at`,
		g.Kind, g.Model, g.Prompt, g.Output,
	).Scan(&g.ID, &g.CreatedAt)
	if err != nil {
		return Generation{}, fmt.Errorf("storage: save generation: %w", err)
	}
	return g, nil
}

func (r *Relational) RecentGenerations(ctx context.Context, limit int) ([]Generation, error) {
	if r == nil || r.pool == nil {
		return nil, ErrNotAvailable
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, kind, model, prompt, output, created_at
		 FROM generations ORDER BY created_at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("storage: recent generations: %w", err)
	}
	defer rows.Close()

	out := make([]Generation, 0, limit)
	for rows.Next() {
		var g Generation
		if err := rows.Scan(&g.ID, &g.Kind, &g.Model, &g.Prompt, &g.Output, &g.CreatedAt); err != nil {
			return nil, fmt.Errorf("storage: scan generation: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (r *Relational) Close() {
	if r != nil && r.pool != nil {
		r.pool.Close()
	}
}

func wrap(backend string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("storage: connect %s: %w", backend, err)
}
