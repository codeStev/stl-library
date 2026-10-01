package sqlite

import (
	"context"
	"strings"

	"github.com/codeStev/stl-library/internal/app"
)

// FilesToHash lists library files that have no hash yet, or whose size or date changed since they were hashed.
func (s *Store) FilesToHash(ctx context.Context, limit int) ([]app.HashJob, error) {
	return s.hashJobs(ctx, `SELECT p.path, p.size, p.mod_unix FROM part p LEFT JOIN file_hash h ON h.path = p.path
		WHERE h.path IS NULL OR h.size != p.size OR h.mod_unix != p.mod_unix ORDER BY p.id LIMIT ?`, limit)
}

// FilesToVerify lists unchanged files (same size and date as when hashed) last checked before olderThan.
func (s *Store) FilesToVerify(ctx context.Context, olderThan int64, limit int) ([]app.HashJob, error) {
	return s.hashJobs(ctx, `SELECT p.path, p.size, p.mod_unix FROM part p JOIN file_hash h ON h.path = p.path
		WHERE h.size = p.size AND h.mod_unix = p.mod_unix AND h.verified_unix < ? ORDER BY h.verified_unix LIMIT ?`, olderThan, limit)
}

func (s *Store) hashJobs(ctx context.Context, query string, args ...any) ([]app.HashJob, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.HashJob
	for rows.Next() {
		var j app.HashJob
		if err := rows.Scan(&j.Path, &j.Size, &j.ModUnix); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// SaveHash records the hash of a new or changed file (a changed file is a new file: no event).
func (s *Store) SaveHash(ctx context.Context, j app.HashJob, sum string, atUnix int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO file_hash (path, size, mod_unix, sha256, hashed_unix, verified_unix) VALUES (?,?,?,?,?,?)
		ON CONFLICT(path) DO UPDATE SET size = excluded.size, mod_unix = excluded.mod_unix, sha256 = excluded.sha256,
			hashed_unix = excluded.hashed_unix, verified_unix = excluded.verified_unix`, j.Path, j.Size, j.ModUnix, sum, atUnix, atUnix)
	return err
}

// Verified handles the re-check of a file that looks unchanged. The same content just refreshes the
// date. Other content - with the same size and date - is what a failing disk does: an event is
// raised and the old hash is kept, so the event stays true.
func (s *Store) Verified(ctx context.Context, j app.HashJob, sum string, atUnix int64) (bool, error) {
	var old string
	if err := s.db.QueryRowContext(ctx, `SELECT sha256 FROM file_hash WHERE path = ?`, j.Path).Scan(&old); err != nil {
		return false, notFound(err)
	}
	if old == sum {
		_, err := s.db.ExecContext(ctx, `UPDATE file_hash SET verified_unix = ? WHERE path = ?`, atUnix, j.Path)
		return true, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM health_event WHERE kind = 'corrupt' AND path = ? AND dismissed = 0`, j.Path).Scan(&n); err != nil {
		return false, err
	}
	if n == 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO health_event (kind, path, detail, at_unix) VALUES ('corrupt', ?, ?, ?)`,
			j.Path, "the content is not what it was (was "+old[:12]+"…, now "+sum[:12]+"…) though size and date are unchanged", atUnix); err != nil {
			return false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE file_hash SET verified_unix = ? WHERE path = ?`, atUnix, j.Path); err != nil {
		return false, err
	}
	return false, tx.Commit()
}

// mergedJoin attaches the merge record of a file while it is still valid (size and date unchanged).
const mergedJoin = `LEFT JOIN dup_merge dm ON dm.path = h.path AND dm.size = h.size AND dm.mod_unix = h.mod_unix`

// DuplicateGroups lists sets of files in the library with the same content, the biggest waste first,
// and the total number of sets. Files merged into hard links count as one copy.
func (s *Store) DuplicateGroups(ctx context.Context, minSize int64, limit, offset int) ([]app.DupGroup, int, error) {
	const from = `FROM file_hash h JOIN part p ON p.path = h.path ` + mergedJoin + `
		WHERE h.size >= ? GROUP BY h.sha256, h.size HAVING sum(dm.path IS NULL) > 1`
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT 1 `+from+`)`, minSize).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT h.sha256, h.size `+from+` ORDER BY (sum(dm.path IS NULL) - 1) * h.size DESC, h.sha256 LIMIT ? OFFSET ?`, minSize, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	var groups []app.DupGroup
	for rows.Next() {
		var g app.DupGroup
		if err := rows.Scan(&g.SHA256, &g.Size); err != nil {
			rows.Close()
			return nil, 0, err
		}
		groups = append(groups, g)
	}
	rows.Close()
	for i := range groups {
		groups[i].Files, err = s.dupFiles(ctx, groups[i].SHA256, groups[i].Size)
		if err != nil {
			return nil, 0, err
		}
	}
	return groups, total, nil
}

// DuplicateGroup returns the files with exactly this content.
func (s *Store) DuplicateGroup(ctx context.Context, sha string, size int64) (app.DupGroup, error) {
	files, err := s.dupFiles(ctx, sha, size)
	if err != nil {
		return app.DupGroup{}, err
	}
	return app.DupGroup{SHA256: sha, Size: size, Files: files}, nil
}

func (s *Store) dupFiles(ctx context.Context, sha string, size int64) ([]app.DupFile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.id, h.path, m.id, `+modelName+`, dm.path IS NOT NULL
		FROM file_hash h JOIN part p ON p.path = h.path JOIN variant v ON v.id = p.variant_id
		JOIN model m ON m.id = v.model_id LEFT JOIN model_user mu ON mu.dir = m.dir `+mergedJoin+`
		WHERE h.sha256 = ? AND h.size = ? ORDER BY h.path`, sha, size)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.DupFile
	for rows.Next() {
		var f app.DupFile
		if err := rows.Scan(&f.PartID, &f.Path, &f.ModelID, &f.ModelName, &f.Linked); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// SaveMerge records that a file was replaced by a hard link to an identical one.
func (s *Store) SaveMerge(ctx context.Context, path string, size, modUnix, atUnix int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO dup_merge (path, size, mod_unix, at_unix) VALUES (?,?,?,?)
		ON CONFLICT(path) DO UPDATE SET size = excluded.size, mod_unix = excluded.mod_unix, at_unix = excluded.at_unix`, path, size, modUnix, atUnix)
	return err
}

// HealthEvents lists the events of a kind that nobody dismissed, newest first.
func (s *Store) HealthEvents(ctx context.Context, kind string) ([]app.HealthEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, path, detail, at_unix FROM health_event
		WHERE kind = ? AND dismissed = 0 ORDER BY at_unix DESC, id DESC LIMIT 500`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.HealthEvent
	for rows.Next() {
		var e app.HealthEvent
		if err := rows.Scan(&e.ID, &e.Kind, &e.Path, &e.Detail, &e.AtUnix); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) DismissHealthEvent(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE health_event SET dismissed = 1 WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return app.ErrNotFound
	}
	return nil
}

// MissingContent lists files once hashed that are gone from the library while no file with the same
// content is left - so a moved file (same content, new path) is not reported, a lost one is.
func (s *Store) MissingContent(ctx context.Context) ([]app.HealthEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT h.path, h.size, h.hashed_unix FROM file_hash h
		WHERE NOT EXISTS (SELECT 1 FROM part p WHERE p.path = h.path)
		AND NOT EXISTS (SELECT 1 FROM file_hash h2 JOIN part p2 ON p2.path = h2.path WHERE h2.sha256 = h.sha256)
		ORDER BY h.path LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.HealthEvent
	for rows.Next() {
		var e app.HealthEvent
		var size int64
		if err := rows.Scan(&e.Path, &size, &e.AtUnix); err != nil {
			return nil, err
		}
		e.Kind, e.Detail = "missing", "the file is gone and no file with its content is left"
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) HashCounts(ctx context.Context) (app.HealthCounts, error) {
	var c app.HealthCounts
	err := s.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM part),
		(SELECT count(*) FROM part p JOIN file_hash h ON h.path = p.path AND h.size = p.size AND h.mod_unix = p.mod_unix)`).Scan(&c.Files, &c.Hashed)
	return c, err
}

// PruneHashes forgets the hashes of files that are gone but whose content is still in the library under
// another path (moved files), so only really missing content stays on record.
func (s *Store) PruneHashes(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM file_hash WHERE path NOT IN (SELECT path FROM part)
		AND sha256 IN (SELECT h2.sha256 FROM file_hash h2 JOIN part p ON p.path = h2.path)`)
	return err
}

// KnownHashes returns which of the sums belong to a hashed library file.
func (s *Store) KnownHashes(ctx context.Context, sums []string) (map[string]bool, error) {
	out := map[string]bool{}
	const chunk = 500
	for i := 0; i < len(sums); i += chunk {
		part := sums[i:min(i+chunk, len(sums))]
		args := make([]any, len(part))
		for j, v := range part {
			args[j] = v
		}
		rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT sha256 FROM file_hash WHERE sha256 IN (?`+strings.Repeat(",?", len(part)-1)+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				rows.Close()
				return nil, err
			}
			out[v] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}
