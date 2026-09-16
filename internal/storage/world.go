package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// World persistence: worlds + world_objects + object_state +
// world_snapshots. The world is *seed + authored content* — terrain
// stays deterministic, content is replayed on load (see
// docs/world-persistence proposal). Layer lives in the relational
// store; every mutation is one statement.

// WorldObjectKind enumerates the authored-placement families.
const (
	ObjectVillage    = "village"
	ObjectFaction    = "faction"
	ObjectNPCGroup   = "npc_group"
	ObjectEnemyGroup = "enemy_group"
	ObjectMerchant   = "merchant"
	ObjectEdit       = "edit"
)

// KnownObjectKinds guards inserts against typos early.
func KnownObjectKind(kind string) bool {
	switch kind {
	case ObjectVillage, ObjectFaction, ObjectNPCGroup,
		ObjectEnemyGroup, ObjectMerchant, ObjectEdit:
		return true
	}
	return false
}

// World is one persistent, loadable game world.
type World struct {
	ID                   int64
	Name                 string
	Seed                 int64
	WW, WH               int
	SpawnX, SpawnY       int
	IsActive             bool
	CreatedAt, UpdatedAt time.Time
}

// WorldObject is one authored placement (village, faction, group,
// merchant, terrain edit). Payload is free-form JSON per kind.
type WorldObject struct {
	ID                   int64
	WorldID              int64
	Kind                 string
	Name                 string
	Seed                 int64
	HomeX, HomeY         int
	Radius               int
	Tiles                json.RawMessage `json:"tiles"`
	Payload              json.RawMessage `json:"data"`
	CreatedAt, UpdatedAt time.Time
}

// ObjectState is the runtime half of a placement — whatever mutates
// while the world is served (HP, respawn timers, position drift).
type ObjectState struct {
	ObjectID  int64
	State     json.RawMessage
	UpdatedAt time.Time
}

// WorldSnapshot is a checkpoint: terrain + meta at a moment.
type WorldSnapshot struct {
	ID        int64
	WorldID   int64
	Label     string
	Terrain   string
	Meta      json.RawMessage
	CreatedAt time.Time
}

// --- worlds -----------------------------------------------------------------

// CreateWorld inserts a world; ErrConflict when the name is taken.
func (r *Relational) CreateWorld(ctx context.Context, w *World) error {
	if err := r.avail(); err != nil {
		return err
	}
	err := r.pool.QueryRow(ctx,
		`INSERT INTO worlds (name, seed, ww, wh, spawn_x, spawn_y)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, created_at, updated_at`,
		w.Name, w.Seed, w.WW, w.WH, w.SpawnX, w.SpawnY,
	).Scan(&w.ID, &w.CreatedAt, &w.UpdatedAt)
	if isUniqueViolation(err) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("storage: create world: %w", err)
	}
	return nil
}

// WorldCard is the listing view of a saved world.
type WorldCard struct {
	ID        int64
	Name      string
	WW, WH    int
	IsActive  bool
	UpdatedAt time.Time
}

// ListWorlds returns every saved world (newest first).
func (r *Relational) ListWorlds(ctx context.Context) ([]WorldCard, error) {
	if err := r.avail(); err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, name, ww, wh, is_active, updated_at
		 FROM worlds ORDER BY updated_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("storage: list worlds: %w", err)
	}
	defer rows.Close()
	out := []WorldCard{}
	for rows.Next() {
		var c WorldCard
		if err := rows.Scan(&c.ID, &c.Name, &c.WW, &c.WH, &c.IsActive, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("storage: scan world card: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// World fetches one saved world (ErrNotFound when absent).
func (r *Relational) World(ctx context.Context, id int64) (World, error) {
	if err := r.avail(); err != nil {
		return World{}, err
	}
	var w World
	err := r.pool.QueryRow(ctx,
		`SELECT id, name, seed, ww, wh, spawn_x, spawn_y, is_active, created_at, updated_at
		 FROM worlds WHERE id = $1`, id,
	).Scan(&w.ID, &w.Name, &w.Seed, &w.WW, &w.WH, &w.SpawnX, &w.SpawnY,
		&w.IsActive, &w.CreatedAt, &w.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return World{}, ErrNotFound
	}
	if err != nil {
		return World{}, fmt.Errorf("storage: fetch world: %w", err)
	}
	return w, nil
}

// ActiveWorld fetches the one world currently being served.
func (r *Relational) ActiveWorld(ctx context.Context) (World, error) {
	if err := r.avail(); err != nil {
		return World{}, err
	}
	var w World
	err := r.pool.QueryRow(ctx,
		`SELECT id, name, seed, ww, wh, spawn_x, spawn_y, is_active, created_at, updated_at
		 FROM worlds WHERE is_active = true`,
	).Scan(&w.ID, &w.Name, &w.Seed, &w.WW, &w.WH, &w.SpawnX, &w.SpawnY,
		&w.IsActive, &w.CreatedAt, &w.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return World{}, ErrNotFound
	}
	if err != nil {
		return World{}, fmt.Errorf("storage: fetch active world: %w", err)
	}
	return w, nil
}

// ActivateWorld makes world id the one being served (singular row
// flipped inside a transaction).
func (r *Relational) ActivateWorld(ctx context.Context, id int64) error {
	if err := r.avail(); err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("storage: activate world: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE worlds SET is_active = false WHERE is_active`); err != nil {
		return fmt.Errorf("storage: activate world: %w", err)
	}
	tag, err := tx.Exec(ctx,
		`UPDATE worlds SET is_active = true, updated_at = now() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("storage: activate world: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// DeleteWorld removes the world and cascades its content.
func (r *Relational) DeleteWorld(ctx context.Context, id int64) error {
	if err := r.avail(); err != nil {
		return err
	}
	tag, err := r.pool.Exec(ctx, `DELETE FROM worlds WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("storage: delete world: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- world_objects ----------------------------------------------------------

// UpsertObject inserts or edits one placement. With o.ID == 0 it's an
// insert (id assigned); with an existing o.ID the row is updated.
func (r *Relational) UpsertObject(ctx context.Context, o *WorldObject) error {
	if err := r.avail(); err != nil {
		return err
	}
	if !KnownObjectKind(o.Kind) {
		return fmt.Errorf("storage: upsert object: unknown kind %q", o.Kind)
	}
	if len(o.Tiles) == 0 {
		o.Tiles = json.RawMessage(`[]`)
	}
	if len(o.Payload) == 0 {
		o.Payload = json.RawMessage(`{}`)
	}
	var err error
	if o.ID == 0 {
		err = r.pool.QueryRow(ctx,
			`INSERT INTO world_objects (world_id, kind, name, seed, home_x, home_y, radius, tiles, data)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9::jsonb)
			 RETURNING id, created_at, updated_at`,
			o.WorldID, o.Kind, o.Name, o.Seed, o.HomeX, o.HomeY, o.Radius, o.Tiles, o.Payload,
		).Scan(&o.ID, &o.CreatedAt, &o.UpdatedAt)
	} else {
		err = r.pool.QueryRow(ctx,
			`UPDATE world_objects SET kind = $1, name = $2, seed = $3, home_x = $4, home_y = $5,
			    radius = $6, tiles = $7::jsonb, data = $8::jsonb, updated_at = now()
			 WHERE id = $9
			 RETURNING id, created_at, updated_at`,
			o.Kind, o.Name, o.Seed, o.HomeX, o.HomeY, o.Radius, o.Tiles, o.Payload, o.ID,
		).Scan(&o.ID, &o.CreatedAt, &o.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound // editing a placement that is not there
		}
	}
	if err != nil {
		return fmt.Errorf("storage: upsert object: %w", err)
	}
	return nil
}

// DeleteObject removes one placement (its object_state cascades).
func (r *Relational) DeleteObject(ctx context.Context, objectID int64) error {
	if err := r.avail(); err != nil {
		return err
	}
	tag, err := r.pool.Exec(ctx, `DELETE FROM world_objects WHERE id = $1`, objectID)
	if err != nil {
		return fmt.Errorf("storage: delete object: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListObjects returns the world's placements, optionally filtered by
// kinds, ordered creation-first so replay order is stable.
func (r *Relational) ListObjects(ctx context.Context, worldID int64, kinds ...string) ([]WorldObject, error) {
	if err := r.avail(); err != nil {
		return nil, err
	}
	q := `SELECT id, world_id, kind, name, seed, home_x, home_y, radius, tiles, data, created_at, updated_at
	      FROM world_objects WHERE world_id = $1`
	args := []any{worldID}
	if len(kinds) > 0 {
		q += ` AND kind = ANY($2)`
		args = append(args, kinds)
	}

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: list objects: %w", err)
	}
	defer rows.Close()
	out := []WorldObject{}
	for rows.Next() {
		var o WorldObject
		if err := rows.Scan(&o.ID, &o.WorldID, &o.Kind, &o.Name, &o.Seed,
			&o.HomeX, &o.HomeY, &o.Radius, &o.Tiles, &o.Payload, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, fmt.Errorf("storage: scan object: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// GetObjectState reads one object's runtime half.
func (r *Relational) GetObjectState(ctx context.Context, objectID int64) (ObjectState, error) {
	if err := r.avail(); err != nil {
		return ObjectState{}, err
	}
	var st ObjectState
	err := r.pool.QueryRow(ctx,
		`SELECT object_id, cur_state, updated_at FROM object_state WHERE object_id = $1`, objectID,
	).Scan(&st.ObjectID, &st.State, &st.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ObjectState{}, ErrNotFound
	}
	if err != nil {
		return ObjectState{}, fmt.Errorf("storage: get object state: %w", err)
	}
	return st, nil
}

// PutObjectState upserts one object's runtime half.
func (r *Relational) PutObjectState(ctx context.Context, st *ObjectState) error {
	if err := r.avail(); err != nil {
		return err
	}
	err := r.pool.QueryRow(ctx,
		`INSERT INTO object_state (object_id, cur_state) VALUES ($1, $2::jsonb)
		 ON CONFLICT (object_id) DO UPDATE SET cur_state = EXCLUDED.cur_state, updated_at = now()
		 RETURNING updated_at`,
		st.ObjectID, st.State,
	).Scan(&st.UpdatedAt)
	if err != nil {
		return fmt.Errorf("storage: put object state: %w", err)
	}
	return nil
}

// --- snapshots ---------------------------------------------------------------

// SaveSnapshot stores a full terrain checkpoint for the world.
func (r *Relational) SaveSnapshot(ctx context.Context, s *WorldSnapshot) error {
	if err := r.avail(); err != nil {
		return err
	}
	if len(s.Meta) == 0 {
		s.Meta = json.RawMessage(`{}`)
	}
	err := r.pool.QueryRow(ctx,
		`INSERT INTO world_snapshots (world_id, label, terrain, meta)
		 VALUES ($1, $2, $3, $4::jsonb) RETURNING id, created_at`,
		s.WorldID, s.Label, s.Terrain, s.Meta,
	).Scan(&s.ID, &s.CreatedAt)
	if err != nil {
		return fmt.Errorf("storage: save snapshot: %w", err)
	}
	return nil
}

// Snapshot is an alias kept stable for callers naming states.
func (r *Relational) LatestSnapshot(ctx context.Context, worldID int64) (WorldSnapshot, error) {
	if err := r.avail(); err != nil {
		return WorldSnapshot{}, err
	}
	var s WorldSnapshot
	err := r.pool.QueryRow(ctx,
		`SELECT id, world_id, label, terrain, meta, created_at
		 FROM world_snapshots WHERE world_id = $1
		 ORDER BY created_at DESC, id DESC LIMIT 1`, worldID,
	).Scan(&s.ID, &s.WorldID, &s.Label, &s.Terrain, &s.Meta, &s.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return WorldSnapshot{}, ErrNotFound
	}
	if err != nil {
		return WorldSnapshot{}, fmt.Errorf("storage: latest snapshot: %w", err)
	}
	return s, nil
}

// DeleteSnapshot removes one checkpoint.
func (r *Relational) DeleteSnapshot(ctx context.Context, id int64) error {
	if err := r.avail(); err != nil {
		return err
	}
	tag, err := r.pool.Exec(ctx, `DELETE FROM world_snapshots WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("storage: delete snapshot: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- helpers -----------------------------------------------------------------

func (r *Relational) avail() error {
	if r == nil || r.pool == nil {
		return ErrNotAvailable
	}
	return nil
}

// isUniqueViolation maps pg's 23505 to the package-level ErrConflict.
func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) && pgErr.SQLState() == "23505" {
		return true
	}
	// pgconn.PgError carries SQLState(); catching it through the direct
	// error type without importing errors everywhere:
	return err != nil && strings.Contains(err.Error(), "duplicate key value")
}

// --- narration commits (slice 5: lore edits build on each other) ------------
//
// Narrative edits are COMMITS, not hot overwrites: each edit inserts
// revision `tip+1` over the current tip of its key. Reads return the
// TIP. Boot never trusts the hot layer — the village row's authored
// convo seeds revision 1 (rev base NULL), and every later edit extends
// the chain. Restart replays the chain in-order; nothing "goes hot".

// NarrCommits is the NarrStore adapter over the relational narration
// commit chain (*storage.Document satisfies nothing here — lore lives
// in the schema'd side so revisions replay from the world record).
type NarrCommits struct {
	r       *Relational
	worldID int64
}

// NewNarrCommits binds the commit adapter to one world's rows.
func NewNarrCommits(r *Relational, worldID int64) *NarrCommits {
	return &NarrCommits{r: r, worldID: worldID}
}

func (n *NarrCommits) avail() error {
	if n == nil || n.r == nil {
		return ErrNotAvailable
	}
	return n.r.avail()
}

// tipRev reads the highest revision for a key (0 = nothing committed).
func (n *NarrCommits) tipRev(ctx context.Context, key string) (int, error) {
	var rev int
	err := n.r.pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(rev), 0) FROM narr_revisions WHERE world_id = $1 AND key = $2`,
		n.worldID, key).Scan(&rev)
	if err != nil {
		return 0, fmt.Errorf("storage: narr tip: %w", err)
	}
	return rev, nil
}

// PutNarrDoc commits ONE revision: rev = tip+1, base_rev = the tip
// the author saw (the commit chain, append-only). Never overwrites.
func (n *NarrCommits) PutNarrDoc(ctx context.Context, key string, v any) error {
	if err := n.avail(); err != nil {
		return err
	}
	tip, err := n.tipRev(ctx, key)
	if err != nil {
		return err
	}
	raw, err := marshalDoc(v)
	if err != nil {
		return err
	}
	if _, err := n.r.pool.Exec(ctx,
		`INSERT INTO narr_revisions (world_id, key, rev, base_rev, payload, author)
		 VALUES ($1, $2, $3, $4, $5, 'harness')`,
		n.worldID, key, tip+1, tip, raw,
	); err != nil {
		return fmt.Errorf("storage: narr commit: %w", err)
	}
	return nil
}

// NarrDocByKey reads the tip revision's payload for a key.
// ErrNotFound when the key has no commits yet.
func (n *NarrCommits) NarrDocByKey(ctx context.Context, key string, v any) error {
	if err := n.avail(); err != nil {
		return err
	}
	var raw string
	err := n.r.pool.QueryRow(ctx,
		`SELECT payload FROM narr_revisions
		 WHERE world_id = $1 AND key = $2
		 ORDER BY rev DESC LIMIT 1`,
		n.worldID, key).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("storage: narr tip read: %w", err)
	}
	return decodeDoc(raw, v)
}
