package models

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrRosterImageNotFound is returned when a roster image lookup matches nothing
// (or the image does not belong to the expected team).
var ErrRosterImageNotFound = errors.New("roster image not found")

// RosterImage is a fight-positioning reference image attached to a roster. The
// raw bytes (Data) are populated only by the read-data paths; list/metadata
// queries omit them to stay cheap and are what the JSON API returns (Data is
// never serialized).
type RosterImage struct {
	ID       int64 `json:"id"`
	RosterID int64 `json:"roster_id"`
	// TeamID is resolved via the image's roster (for team-ownership checks).
	TeamID      int64     `json:"team_id"`
	Position    int       `json:"position"`
	Caption     string    `json:"caption"`
	ContentType string    `json:"content_type"`
	ByteSize    int       `json:"byte_size"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	// Data holds the raw image bytes; only loaded by the data paths and never
	// included in JSON responses.
	Data []byte `json:"-"`
}

// RosterImageStore provides data access for a roster's positioning images.
type RosterImageStore struct {
	pool *pgxpool.Pool
}

// NewRosterImageStore constructs a RosterImageStore backed by the given pool.
func NewRosterImageStore(pool *pgxpool.Pool) *RosterImageStore {
	return &RosterImageStore{pool: pool}
}

// ListForRoster returns a roster's images (metadata only, no bytes), ordered by
// position.
func (s *RosterImageStore) ListForRoster(ctx context.Context, rosterID int64) ([]RosterImage, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT i.id, i.roster_id, r.team_id, i.position, i.caption, i.content_type, i.byte_size, i.created_at, i.updated_at
		 FROM roster_images i JOIN rosters r ON r.id = i.roster_id
		 WHERE i.roster_id = $1 ORDER BY i.position, i.id`, rosterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	images := []RosterImage{}
	for rows.Next() {
		var img RosterImage
		if err := rows.Scan(&img.ID, &img.RosterID, &img.TeamID, &img.Position, &img.Caption,
			&img.ContentType, &img.ByteSize, &img.CreatedAt, &img.UpdatedAt); err != nil {
			return nil, err
		}
		images = append(images, img)
	}
	return images, rows.Err()
}

// ListDataForActiveRoster returns the bytes + captions of every image on the
// team's active roster, ordered by position. Used by the Discord bot to post
// positioning images into a run's thread. Returns no rows when the team has no
// active roster set.
func (s *RosterImageStore) ListDataForActiveRoster(ctx context.Context, teamID int64) ([]RosterImage, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT i.id, i.roster_id, i.position, i.caption, i.content_type, i.byte_size, i.data
		 FROM roster_images i
		 JOIN teams t ON t.active_roster_id = i.roster_id
		 WHERE t.id = $1 ORDER BY i.position, i.id`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	images := []RosterImage{}
	for rows.Next() {
		var img RosterImage
		if err := rows.Scan(&img.ID, &img.RosterID, &img.Position, &img.Caption,
			&img.ContentType, &img.ByteSize, &img.Data); err != nil {
			return nil, err
		}
		images = append(images, img)
	}
	return images, rows.Err()
}

// Get returns one image's metadata (no bytes), with its resolved team id.
func (s *RosterImageStore) Get(ctx context.Context, imageID int64) (*RosterImage, error) {
	img := &RosterImage{}
	err := s.pool.QueryRow(ctx,
		`SELECT i.id, i.roster_id, r.team_id, i.position, i.caption, i.content_type, i.byte_size, i.created_at, i.updated_at
		 FROM roster_images i JOIN rosters r ON r.id = i.roster_id WHERE i.id = $1`, imageID).Scan(
		&img.ID, &img.RosterID, &img.TeamID, &img.Position, &img.Caption,
		&img.ContentType, &img.ByteSize, &img.CreatedAt, &img.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRosterImageNotFound
	}
	if err != nil {
		return nil, err
	}
	return img, nil
}

// GetData returns one image's raw bytes and content type, for HTTP serving.
func (s *RosterImageStore) GetData(ctx context.Context, imageID int64) ([]byte, string, error) {
	var data []byte
	var contentType string
	err := s.pool.QueryRow(ctx,
		`SELECT data, content_type FROM roster_images WHERE id = $1`, imageID).Scan(&data, &contentType)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrRosterImageNotFound
	}
	if err != nil {
		return nil, "", err
	}
	return data, contentType, nil
}

// CountForRoster returns how many images a roster has.
func (s *RosterImageStore) CountForRoster(ctx context.Context, rosterID int64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM roster_images WHERE roster_id = $1`, rosterID).Scan(&n)
	return n, err
}

// Create inserts a new image (appended after existing ones) and returns its
// metadata.
func (s *RosterImageStore) Create(ctx context.Context, rosterID int64, caption, contentType string, data []byte) (*RosterImage, error) {
	var position int
	if err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(position), -1) + 1 FROM roster_images WHERE roster_id = $1`, rosterID,
	).Scan(&position); err != nil {
		return nil, err
	}
	var id int64
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO roster_images (roster_id, position, caption, content_type, byte_size, data)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		rosterID, position, caption, contentType, len(data), data,
	).Scan(&id); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// UpdateCaption changes an image's caption.
func (s *RosterImageStore) UpdateCaption(ctx context.Context, imageID int64, caption string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE roster_images SET caption = $1, updated_at = now() WHERE id = $2`, caption, imageID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrRosterImageNotFound
	}
	return nil
}

// Delete removes an image.
func (s *RosterImageStore) Delete(ctx context.Context, imageID int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM roster_images WHERE id = $1`, imageID)
	return err
}

// copyRosterImagesTx copies every image from srcRosterID to dstRosterID within an
// existing transaction. Used when copying a roster (or a whole team).
func copyRosterImagesTx(ctx context.Context, tx pgx.Tx, srcRosterID, dstRosterID int64) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO roster_images (roster_id, position, caption, content_type, byte_size, data)
		 SELECT $1, position, caption, content_type, byte_size, data
		 FROM roster_images WHERE roster_id = $2`,
		dstRosterID, srcRosterID)
	return err
}
