// Package sqlite keeps the library index in a SQLite database (pure Go
// driver, no cgo), with full-text search over model names.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/codeStev/stl-library/internal/app"
	"github.com/codeStev/stl-library/internal/core/library"
)

// Store implements app.Store.
type Store struct {
	db *sql.DB
}

var _ app.Store = (*Store)(nil)

// Open opens (and creates or migrates) the index database at path.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One writer; SQLite serializes writes anyway.
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// migrations are applied in order; PRAGMA user_version records how many ran.
var migrations = []string{
	`CREATE TABLE model (
		id INTEGER PRIMARY KEY,
		dir TEXT NOT NULL UNIQUE,
		creator TEXT NOT NULL, release TEXT NOT NULL, category TEXT NOT NULL, name TEXT NOT NULL,
		variants INTEGER NOT NULL, parts INTEGER NOT NULL, bytes INTEGER NOT NULL,
		sig TEXT NOT NULL
	);
	CREATE INDEX model_creator ON model(creator);
	CREATE TABLE variant (
		id INTEGER PRIMARY KEY,
		model_id INTEGER NOT NULL REFERENCES model(id) ON DELETE CASCADE,
		dir TEXT NOT NULL UNIQUE, option TEXT NOT NULL,
		scale TEXT NOT NULL, supports TEXT NOT NULL, density TEXT NOT NULL, format TEXT NOT NULL,
		fill TEXT NOT NULL, split TEXT NOT NULL, tech TEXT NOT NULL, extra TEXT NOT NULL
	);
	CREATE INDEX variant_model ON variant(model_id);
	CREATE TABLE part (
		id INTEGER PRIMARY KEY,
		variant_id INTEGER NOT NULL REFERENCES variant(id) ON DELETE CASCADE,
		path TEXT NOT NULL UNIQUE, size INTEGER NOT NULL, mod_unix INTEGER NOT NULL
	);
	CREATE INDEX part_variant ON part(variant_id);
	CREATE TABLE image (
		id INTEGER PRIMARY KEY,
		model_id INTEGER NOT NULL REFERENCES model(id) ON DELETE CASCADE,
		path TEXT NOT NULL UNIQUE, size INTEGER NOT NULL, mod_unix INTEGER NOT NULL
	);
	CREATE INDEX image_model ON image(model_id);
	CREATE TABLE issue (dir TEXT PRIMARY KEY, reason TEXT NOT NULL);
	CREATE VIRTUAL TABLE model_fts USING fts5(name, creator, release, category, tokenize='unicode61 remove_diacritics 2');`,
}

func migrate(db *sql.DB) error {
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	for i := v; i < len(migrations); i++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Sync brings the index in line with models and issues in one transaction.
// Models are matched by folder; an unchanged signature means no write.
func (s *Store) Sync(ctx context.Context, models []*library.Model, issues []library.Issue) (app.SyncStats, error) {
	var st app.SyncStats
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return st, err
	}
	defer tx.Rollback()

	type known struct {
		id  int64
		sig string
	}
	existing := map[string]known{}
	rows, err := tx.QueryContext(ctx, `SELECT id, dir, sig FROM model`)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var k known
		var dir string
		if err := rows.Scan(&k.id, &dir, &k.sig); err != nil {
			rows.Close()
			return st, err
		}
		existing[dir] = k
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return st, err
	}

	seen := map[string]bool{}
	for _, m := range models {
		seen[m.Dir] = true
		sig := m.Signature()
		k, ok := existing[m.Dir]
		switch {
		case ok && k.sig == sig:
			st.Unchanged++
			continue
		case ok:
			st.Updated++
			if err := s.writeModel(ctx, tx, k.id, m, sig); err != nil {
				return st, err
			}
		default:
			st.Added++
			if err := s.writeModel(ctx, tx, 0, m, sig); err != nil {
				return st, err
			}
		}
	}
	for dir, k := range existing {
		if seen[dir] {
			continue
		}
		st.Removed++
		if _, err := tx.ExecContext(ctx, `DELETE FROM model WHERE id = ?`, k.id); err != nil {
			return st, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM model_fts WHERE rowid = ?`, k.id); err != nil {
			return st, err
		}
	}
	if err := syncIssues(ctx, tx, issues); err != nil {
		return st, err
	}
	st.Issues = len(issues)
	return st, tx.Commit()
}

// writeModel inserts a model (id 0) or rewrites an existing one in place,
// keeping its id.
func (s *Store) writeModel(ctx context.Context, tx *sql.Tx, id int64, m *library.Model, sig string) error {
	parts, bytes := 0, int64(0)
	for _, v := range m.Variants {
		parts += len(v.Parts)
		for _, p := range v.Parts {
			bytes += p.Size
		}
	}
	if id == 0 {
		res, err := tx.ExecContext(ctx, `INSERT INTO model (dir, creator, release, category, name, variants, parts, bytes, sig) VALUES (?,?,?,?,?,?,?,?,?)`,
			m.Dir, m.Creator, m.Release, m.Category, m.Name, len(m.Variants), parts, bytes, sig)
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE model SET creator=?, release=?, category=?, name=?, variants=?, parts=?, bytes=?, sig=? WHERE id=?`,
			m.Creator, m.Release, m.Category, m.Name, len(m.Variants), parts, bytes, sig, id); err != nil {
			return err
		}
		for _, q := range []string{`DELETE FROM variant WHERE model_id = ?`, `DELETE FROM image WHERE model_id = ?`, `DELETE FROM model_fts WHERE rowid = ?`} {
			if _, err := tx.ExecContext(ctx, q, id); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO model_fts (rowid, name, creator, release, category) VALUES (?,?,?,?,?)`,
		id, m.Name, m.Creator, m.Release, m.Category); err != nil {
		return err
	}
	for _, v := range m.Variants {
		d := v.Dims
		res, err := tx.ExecContext(ctx, `INSERT INTO variant (model_id, dir, option, scale, supports, density, format, fill, split, tech, extra) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			id, v.Dir, v.Option, d.Scale, d.Supports, d.Density, d.Format, d.Fill, d.Split, d.Tech, d.Extra)
		if err != nil {
			return err
		}
		vid, err := res.LastInsertId()
		if err != nil {
			return err
		}
		for _, p := range v.Parts {
			if _, err := tx.ExecContext(ctx, `INSERT INTO part (variant_id, path, size, mod_unix) VALUES (?,?,?,?)`, vid, p.Path, p.Size, p.ModUnix); err != nil {
				return err
			}
		}
	}
	for _, i := range m.Images {
		if _, err := tx.ExecContext(ctx, `INSERT INTO image (model_id, path, size, mod_unix) VALUES (?,?,?,?)`, id, i.Path, i.Size, i.ModUnix); err != nil {
			return err
		}
	}
	return nil
}

// syncIssues replaces the issue list, writing only differences.
func syncIssues(ctx context.Context, tx *sql.Tx, issues []library.Issue) error {
	old := map[string]string{}
	rows, err := tx.QueryContext(ctx, `SELECT dir, reason FROM issue`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var d, r string
		if err := rows.Scan(&d, &r); err != nil {
			rows.Close()
			return err
		}
		old[d] = r
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, i := range issues {
		if r, ok := old[i.Dir]; ok && r == i.Reason {
			delete(old, i.Dir)
			continue
		}
		delete(old, i.Dir)
		if _, err := tx.ExecContext(ctx, `INSERT INTO issue (dir, reason) VALUES (?,?) ON CONFLICT(dir) DO UPDATE SET reason = excluded.reason`, i.Dir, i.Reason); err != nil {
			return err
		}
	}
	for d := range old {
		if _, err := tx.ExecContext(ctx, `DELETE FROM issue WHERE dir = ?`, d); err != nil {
			return err
		}
	}
	return nil
}

// Search matches every word as a prefix against name, creator, release
// and category, best matches first.
func (s *Store) Search(ctx context.Context, q app.Query) ([]app.ModelSummary, error) {
	var where []string
	var args []any
	from := `model m`
	order := `m.creator, m.release, m.name`
	if match := ftsQuery(q.Text); match != "" {
		from = `model_fts f JOIN model m ON m.id = f.rowid`
		where = append(where, `model_fts MATCH ?`)
		args = append(args, match)
		order = `bm25(model_fts, 10, 2, 3, 1), m.name`
	}
	if q.Creator != "" {
		where = append(where, `m.creator = ?`)
		args = append(args, q.Creator)
	}
	query := `SELECT ` + summaryCols + ` FROM ` + from
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, ` AND `)
	}
	query += ` ORDER BY ` + order + ` LIMIT ? OFFSET ?`
	args = append(args, q.Limit, q.Offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.ModelSummary
	for rows.Next() {
		var m app.ModelSummary
		if err := scanSummary(rows, &m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ftsQuery turns free text into an FTS5 query: every word must match as a
// prefix. Words are quoted, so FTS syntax in the input is never
// interpreted.
func ftsQuery(text string) string {
	var terms []string
	for _, w := range strings.FieldsFunc(text, func(r rune) bool {
		return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r > 127)
	}) {
		terms = append(terms, `"`+w+`"*`)
	}
	return strings.Join(terms, " ")
}

const summaryCols = `m.id, m.creator, m.release, m.category, m.name, m.dir, m.variants, m.parts, m.bytes,
	coalesce((SELECT i.id FROM image i WHERE i.model_id = m.id AND (lower(i.path) LIKE '%.jpg' OR lower(i.path) LIKE '%.jpeg'
		OR lower(i.path) LIKE '%.png' OR lower(i.path) LIKE '%.webp' OR lower(i.path) LIKE '%.gif') ORDER BY i.path LIMIT 1), 0),
	EXISTS (SELECT 1 FROM variant v JOIN part p ON p.variant_id = v.id WHERE v.model_id = m.id AND lower(p.path) LIKE '%.stl')`

func scanSummary(row interface{ Scan(...any) error }, m *app.ModelSummary) error {
	return row.Scan(&m.ID, &m.Creator, &m.Release, &m.Category, &m.Name, &m.Dir, &m.Variants, &m.Parts, &m.Bytes, &m.Cover, &m.Renderable)
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return app.ErrNotFound
	}
	return err
}

// Model returns a model with its variants (parts included) and images.
func (s *Store) Model(ctx context.Context, id int64) (*app.ModelDetail, error) {
	var m app.ModelDetail
	if err := scanSummary(s.db.QueryRowContext(ctx, `SELECT `+summaryCols+` FROM model m WHERE m.id = ?`, id), &m.ModelSummary); err != nil {
		return nil, notFound(err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM variant WHERE model_id = ? ORDER BY dir`, id)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var vid int64
		if err := rows.Scan(&vid); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, vid)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, vid := range ids {
		v, err := s.Variant(ctx, vid)
		if err != nil {
			return nil, err
		}
		m.Variants = append(m.Variants, *v)
	}
	if m.Images, err = s.files(ctx, `SELECT id, path, size, mod_unix FROM image WHERE model_id = ? ORDER BY path`, id); err != nil {
		return nil, err
	}
	return &m, nil
}

// Variant returns one variant with its parts.
func (s *Store) Variant(ctx context.Context, id int64) (*app.VariantDetail, error) {
	v := app.VariantDetail{ID: id}
	d := &v.Dims
	err := s.db.QueryRowContext(ctx, `SELECT model_id, dir, option, scale, supports, density, format, fill, split, tech, extra FROM variant WHERE id = ?`, id).
		Scan(&v.ModelID, &v.Dir, &v.Option, &d.Scale, &d.Supports, &d.Density, &d.Format, &d.Fill, &d.Split, &d.Tech, &d.Extra)
	if err != nil {
		return nil, notFound(err)
	}
	if v.Parts, err = s.files(ctx, `SELECT id, path, size, mod_unix FROM part WHERE variant_id = ? ORDER BY path`, id); err != nil {
		return nil, err
	}
	return &v, nil
}

func (s *Store) files(ctx context.Context, query string, id int64) ([]app.FileRef, error) {
	rows, err := s.db.QueryContext(ctx, query, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.FileRef
	for rows.Next() {
		var f app.FileRef
		if err := rows.Scan(&f.ID, &f.Path, &f.Size, &f.ModUnix); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Image returns one image of a model.
func (s *Store) Image(ctx context.Context, id int64) (*app.FileRef, error) {
	f := app.FileRef{ID: id}
	if err := s.db.QueryRowContext(ctx, `SELECT path, size, mod_unix FROM image WHERE id = ?`, id).Scan(&f.Path, &f.Size, &f.ModUnix); err != nil {
		return nil, notFound(err)
	}
	return &f, nil
}

// Part returns one part file.
func (s *Store) Part(ctx context.Context, id int64) (*app.FileRef, error) {
	f := app.FileRef{ID: id}
	if err := s.db.QueryRowContext(ctx, `SELECT path, size, mod_unix FROM part WHERE id = ?`, id).Scan(&f.Path, &f.Size, &f.ModUnix); err != nil {
		return nil, notFound(err)
	}
	return &f, nil
}

// Creators lists every creator with its number of models.
func (s *Store) Creators(ctx context.Context) ([]app.CreatorCount, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT creator, count(*) FROM model GROUP BY creator ORDER BY creator`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.CreatorCount
	for rows.Next() {
		var c app.CreatorCount
		if err := rows.Scan(&c.Name, &c.Models); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Issues lists the folders that don't follow the convention.
func (s *Store) Issues(ctx context.Context) ([]library.Issue, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT dir, reason FROM issue ORDER BY dir`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []library.Issue
	for rows.Next() {
		var i library.Issue
		if err := rows.Scan(&i.Dir, &i.Reason); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}
