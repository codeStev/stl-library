package sqlite

import (
	"context"

	"github.com/codeStev/stl-library/internal/app"
)

// A model is up for the preview review when its preview is a render (no cover image of its own to show - the
// library shows a bundled image before it renders anything), it has an STL to render, it is shown in the
// library, and it has not been reviewed yet.
const reviewable = `NOT EXISTS (SELECT 1 FROM preview_review r WHERE r.dir = m.dir)
	AND NOT EXISTS (SELECT 1 FROM model_user u WHERE u.dir = m.dir AND u.hidden)
	AND NOT EXISTS (SELECT 1 FROM image i WHERE i.model_id = m.id AND (lower(i.path) LIKE '%.jpg' OR lower(i.path) LIKE '%.jpeg'
		OR lower(i.path) LIKE '%.png' OR lower(i.path) LIKE '%.webp' OR lower(i.path) LIKE '%.gif'))
	AND EXISTS (SELECT 1 FROM variant v JOIN part p ON p.variant_id = v.id WHERE v.model_id = m.id AND lower(p.path) LIKE '%.stl')
	AND (? = '' OR m.creator = ?)`

// ReviewCandidates lists the next models to review (folder order), at most limit, skipping the first skip.
func (s *Store) ReviewCandidates(ctx context.Context, creator string, skip, limit int) ([]app.ReviewCandidate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.id, m.dir FROM model m WHERE `+reviewable+`
		ORDER BY m.creator, m.release, m.category, m.name, m.id LIMIT ? OFFSET ?`, creator, creator, limit, skip)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.ReviewCandidate
	for rows.Next() {
		var c app.ReviewCandidate
		if err := rows.Scan(&c.ID, &c.Dir); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ReviewCounts says how many models wait for review and how many are done.
func (s *Store) ReviewCounts(ctx context.Context, creator string) (remaining, done int, err error) {
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM model m WHERE `+reviewable, creator, creator).Scan(&remaining); err != nil {
		return
	}
	err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM preview_review r JOIN model m ON m.dir = r.dir WHERE (? = '' OR m.creator = ?)`, creator, creator).Scan(&done)
	return
}

func (s *Store) MarkReview(ctx context.Context, dir, action string, atUnix int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO preview_review (dir, action, at_unix) VALUES (?, ?, ?)
		ON CONFLICT(dir) DO UPDATE SET action = excluded.action, at_unix = excluded.at_unix`, dir, action, atUnix)
	return err
}

func (s *Store) UnmarkReview(ctx context.Context, dir string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM preview_review WHERE dir = ?`, dir)
	return err
}

// ResetReviews makes the skipped models (of a creator, or all) up for review again; chosen pictures stay.
func (s *Store) ResetReviews(ctx context.Context, creator string) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM preview_review WHERE action = 'skip'
		AND (? = '' OR dir IN (SELECT dir FROM model WHERE creator = ?))`, creator, creator)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}
