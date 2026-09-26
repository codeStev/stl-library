// Package sqlite keeps the library index in a SQLite database (pure Go
// driver, no cgo), with full-text search over model names.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/codeStev/stl-library/convention"
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

	// User data, keyed by folder (not by id) and never touched by Sync, so
	// it survives rescans - even a model that is gone for a while.
	`CREATE TABLE model_user (dir TEXT PRIMARY KEY, display_name TEXT NOT NULL DEFAULT '');
	CREATE TABLE tag (dir TEXT NOT NULL, tag TEXT NOT NULL, PRIMARY KEY (dir, tag));
	CREATE INDEX tag_tag ON tag(tag);
	CREATE TABLE print (
		id INTEGER PRIMARY KEY, dir TEXT NOT NULL, variant_dir TEXT NOT NULL,
		printed_unix INTEGER NOT NULL, note TEXT NOT NULL
	);
	CREATE INDEX print_dir ON print(dir);
	CREATE INDEX print_variant ON print(variant_dir);
	CREATE TABLE queue (
		id INTEGER PRIMARY KEY, dir TEXT NOT NULL, variant_dir TEXT NOT NULL UNIQUE,
		added_unix INTEGER NOT NULL, note TEXT NOT NULL
	);
	DROP TABLE model_fts;
	CREATE VIRTUAL TABLE model_fts USING fts5(name, creator, release, category, display, tags, tokenize='unicode61 remove_diacritics 2');
	INSERT INTO model_fts (rowid, name, creator, release, category, display, tags) SELECT id, name, creator, release, category, '', '' FROM model;`,

	// What the importer did with each download folder.
	`CREATE TABLE import_unit (
		source TEXT PRIMARY KEY, signature TEXT NOT NULL, state TEXT NOT NULL, target TEXT NOT NULL,
		files INTEGER NOT NULL, message TEXT NOT NULL, updated_unix INTEGER NOT NULL
	);
	CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);`,

	// Corrections, by folder like all user data: hidden models, and
	// variant labels overriding what the folder names say.
	`ALTER TABLE model_user ADD COLUMN hidden INTEGER NOT NULL DEFAULT 0;
	CREATE TABLE variant_label (
		dir TEXT PRIMARY KEY, option TEXT NOT NULL,
		scale TEXT NOT NULL, supports TEXT NOT NULL, density TEXT NOT NULL, format TEXT NOT NULL,
		fill TEXT NOT NULL, split TEXT NOT NULL, tech TEXT NOT NULL, extra TEXT NOT NULL
	);`,

	// Accounts and everything around signing in (see accounts.go).
	`CREATE TABLE account (
		id TEXT PRIMARY KEY, email TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL,
		auth_provider TEXT NOT NULL, external_subject TEXT NOT NULL, role TEXT NOT NULL,
		enabled INTEGER NOT NULL, token_version INTEGER NOT NULL, failed_attempts INTEGER NOT NULL,
		locked_until INTEGER NOT NULL, mfa TEXT NOT NULL, totp_secret TEXT NOT NULL,
		totp_pending TEXT NOT NULL, created_unix INTEGER NOT NULL
	);
	CREATE UNIQUE INDEX account_external ON account (auth_provider, external_subject) WHERE auth_provider != '';
	CREATE TABLE recovery_code (
		id INTEGER PRIMARY KEY, account_id TEXT NOT NULL REFERENCES account(id) ON DELETE CASCADE,
		hash TEXT NOT NULL, used_unix INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX recovery_code_account ON recovery_code (account_id);
	CREATE TABLE account_session (
		id TEXT PRIMARY KEY, account_id TEXT NOT NULL REFERENCES account(id) ON DELETE CASCADE,
		created_unix INTEGER NOT NULL, expires_unix INTEGER NOT NULL, revoked_unix INTEGER NOT NULL DEFAULT 0,
		user_agent TEXT NOT NULL, ip TEXT NOT NULL
	);
	CREATE INDEX account_session_account ON account_session (account_id);
	CREATE TABLE webauthn_credential (
		id BLOB PRIMARY KEY, account_id TEXT NOT NULL REFERENCES account(id) ON DELETE CASCADE,
		data BLOB NOT NULL, name TEXT NOT NULL, created_unix INTEGER NOT NULL
	);
	CREATE INDEX webauthn_credential_account ON webauthn_credential (account_id);
	CREATE TABLE ephemeral (key TEXT PRIMARY KEY, value BLOB NOT NULL, expires_unix INTEGER NOT NULL);`,

	// Google accounts an admin added have no subject until their first
	// sign-in; several may wait at once.
	`DROP INDEX account_external;
	CREATE UNIQUE INDEX account_external ON account (auth_provider, external_subject) WHERE external_subject != '';`,
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
		for _, q := range []string{`DELETE FROM variant WHERE model_id = ?`, `DELETE FROM image WHERE model_id = ?`} {
			if _, err := tx.ExecContext(ctx, q, id); err != nil {
				return err
			}
		}
	}
	if err := writeFTS(ctx, tx, id); err != nil {
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

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// writeFTS (re)writes the search row of a model, with its display name
// and tags.
func writeFTS(ctx context.Context, tx execer, id int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM model_fts WHERE rowid = ?`, id); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO model_fts (rowid, name, creator, release, category, display, tags)
		SELECT m.id, m.name, m.creator, m.release, m.category,
			coalesce((SELECT display_name FROM model_user u WHERE u.dir = m.dir), ''),
			coalesce((SELECT group_concat(tag, ' ') FROM tag t WHERE t.dir = m.dir), '')
		FROM model m WHERE m.id = ?`, id)
	return err
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
	order := `m.creator, m.release, coalesce(nullif((SELECT display_name FROM model_user u WHERE u.dir = m.dir), ''), m.name) COLLATE NOCASE`
	if match := ftsQuery(q.Text); match != "" {
		from = `model_fts f JOIN model m ON m.id = f.rowid`
		where = append(where, `model_fts MATCH ?`)
		args = append(args, match)
		order = `bm25(model_fts, 10, 2, 3, 1, 10, 5), m.name`
	}
	if q.Creator != "" {
		where = append(where, `m.creator = ?`)
		args = append(args, q.Creator)
	}
	if q.Tag != "" {
		where = append(where, `EXISTS (SELECT 1 FROM tag t WHERE t.dir = m.dir AND t.tag = ? COLLATE NOCASE)`)
		args = append(args, q.Tag)
	}
	if !q.Hidden {
		where = append(where, `NOT EXISTS (SELECT 1 FROM model_user u WHERE u.dir = m.dir AND u.hidden)`)
	}
	if q.Printed != nil {
		not := "NOT "
		if *q.Printed {
			not = ""
		}
		where = append(where, not+`EXISTS (SELECT 1 FROM print pr WHERE pr.dir = m.dir)`)
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
	EXISTS (SELECT 1 FROM variant v JOIN part p ON p.variant_id = v.id WHERE v.model_id = m.id AND lower(p.path) LIKE '%.stl'),
	coalesce((SELECT display_name FROM model_user u WHERE u.dir = m.dir), ''),
	coalesce((SELECT group_concat(tag, char(31)) FROM (SELECT tag FROM tag t WHERE t.dir = m.dir ORDER BY tag COLLATE NOCASE)), ''),
	(SELECT count(*) FROM print pr WHERE pr.dir = m.dir),
	coalesce((SELECT hidden FROM model_user u WHERE u.dir = m.dir), 0)`

func scanSummary(row interface{ Scan(...any) error }, m *app.ModelSummary) error {
	var tags string
	if err := row.Scan(&m.ID, &m.Creator, &m.Release, &m.Category, &m.Name, &m.Dir, &m.Variants, &m.Parts, &m.Bytes,
		&m.Cover, &m.Renderable, &m.DisplayName, &tags, &m.Prints, &m.Hidden); err != nil {
		return err
	}
	if tags != "" {
		m.Tags = strings.Split(tags, "\x1f")
	}
	return nil
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
	err := s.db.QueryRowContext(ctx, `SELECT v.model_id, v.dir, coalesce(l.option, v.option),
			coalesce(l.scale, v.scale), coalesce(l.supports, v.supports), coalesce(l.density, v.density), coalesce(l.format, v.format),
			coalesce(l.fill, v.fill), coalesce(l.split, v.split), coalesce(l.tech, v.tech), coalesce(l.extra, v.extra), l.dir IS NOT NULL
		FROM variant v LEFT JOIN variant_label l ON l.dir = v.dir WHERE v.id = ?`, id).
		Scan(&v.ModelID, &v.Dir, &v.Option, &d.Scale, &d.Supports, &d.Density, &d.Format, &d.Fill, &d.Split, &d.Tech, &d.Extra, &v.Relabeled)
	if err != nil {
		return nil, notFound(err)
	}
	if v.Parts, err = s.files(ctx, `SELECT id, path, size, mod_unix FROM part WHERE variant_id = ? ORDER BY path`, id); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, printed_unix, note, variant_dir FROM print WHERE variant_dir = ? ORDER BY printed_unix DESC, id DESC`, v.Dir)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p app.Print
		if err := rows.Scan(&p.ID, &p.AtUnix, &p.Note, &p.Variant); err != nil {
			return nil, err
		}
		v.Prints = append(v.Prints, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var queued int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM queue WHERE variant_dir = ?`, v.Dir).Scan(&queued); err != nil {
		return nil, err
	}
	v.Queued = queued > 0
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

// PartVariant returns the variant a part belongs to.
func (s *Store) PartVariant(ctx context.Context, partID int64) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT variant_id FROM part WHERE id = ?`, partID).Scan(&id)
	return id, notFound(err)
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

// modelDir resolves a model id to its folder, the key of user data.
func (s *Store) modelDir(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id int64) (string, error) {
	var dir string
	if err := q.QueryRowContext(ctx, `SELECT dir FROM model WHERE id = ?`, id).Scan(&dir); err != nil {
		return "", notFound(err)
	}
	return dir, nil
}

// SetTags replaces a model's tags.
func (s *Store) SetTags(ctx context.Context, modelID int64, tags []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	dir, err := s.modelDir(ctx, tx, modelID)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM tag WHERE dir = ?`, dir); err != nil {
		return err
	}
	for _, t := range tags {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO tag (dir, tag) VALUES (?, ?)`, dir, t); err != nil {
			return err
		}
	}
	if err := writeFTS(ctx, tx, modelID); err != nil {
		return err
	}
	return tx.Commit()
}

// SetDisplayName sets (or with "" clears) a model's display name.
func (s *Store) SetDisplayName(ctx context.Context, modelID int64, name string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	dir, err := s.modelDir(ctx, tx, modelID)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO model_user (dir, display_name) VALUES (?, ?)
		ON CONFLICT(dir) DO UPDATE SET display_name = excluded.display_name`, dir, name); err != nil {
		return err
	}
	if err := writeFTS(ctx, tx, modelID); err != nil {
		return err
	}
	return tx.Commit()
}

// Tags lists every tag with the number of models (present in the
// library) carrying it.
func (s *Store) Tags(ctx context.Context) ([]app.TagCount, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT t.tag, count(*) FROM tag t JOIN model m ON m.dir = t.dir
		GROUP BY t.tag COLLATE NOCASE ORDER BY t.tag COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.TagCount
	for rows.Next() {
		var t app.TagCount
		if err := rows.Scan(&t.Tag, &t.Models); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// variantDirs resolves a variant id to its folder and its model's folder.
func (s *Store) variantDirs(ctx context.Context, variantID int64) (modelDir, variantDir string, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT m.dir, v.dir FROM variant v JOIN model m ON m.id = v.model_id WHERE v.id = ?`, variantID).
		Scan(&modelDir, &variantDir)
	return modelDir, variantDir, notFound(err)
}

// AddPrint records a print of a variant.
func (s *Store) AddPrint(ctx context.Context, variantID, atUnix int64, note string) (app.Print, error) {
	mdir, vdir, err := s.variantDirs(ctx, variantID)
	if err != nil {
		return app.Print{}, err
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO print (dir, variant_dir, printed_unix, note) VALUES (?,?,?,?)`, mdir, vdir, atUnix, note)
	if err != nil {
		return app.Print{}, err
	}
	id, err := res.LastInsertId()
	return app.Print{ID: id, AtUnix: atUnix, Note: note, Variant: vdir}, err
}

// DeletePrint removes a print record.
func (s *Store) DeletePrint(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM print WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return app.ErrNotFound
	}
	return nil
}

// Enqueue puts a variant on the print queue; one already queued keeps its
// place (the note is updated).
func (s *Store) Enqueue(ctx context.Context, variantID, atUnix int64, note string) error {
	mdir, vdir, err := s.variantDirs(ctx, variantID)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO queue (dir, variant_dir, added_unix, note) VALUES (?,?,?,?)
		ON CONFLICT(variant_dir) DO UPDATE SET note = excluded.note`, mdir, vdir, atUnix, note)
	return err
}

// Dequeue takes a variant off the queue.
func (s *Store) Dequeue(ctx context.Context, variantID int64) error {
	_, vdir, err := s.variantDirs(ctx, variantID)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM queue WHERE variant_dir = ?`, vdir)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return app.ErrNotFound
	}
	return nil
}

// Queue lists queued variants that are in the library, oldest first.
func (s *Store) Queue(ctx context.Context) ([]app.QueueItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT v.id, m.id, coalesce(nullif(u.display_name, ''), m.name), v.dir, m.dir, coalesce(l.option, v.option),
			coalesce(l.scale, v.scale), coalesce(l.supports, v.supports), coalesce(l.density, v.density), coalesce(l.format, v.format),
			coalesce(l.fill, v.fill), coalesce(l.split, v.split), coalesce(l.tech, v.tech), coalesce(l.extra, v.extra), q.added_unix, q.note
		FROM queue q JOIN variant v ON v.dir = q.variant_dir JOIN model m ON m.id = v.model_id
		LEFT JOIN model_user u ON u.dir = m.dir LEFT JOIN variant_label l ON l.dir = v.dir
		ORDER BY q.added_unix, q.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.QueueItem
	for rows.Next() {
		var it app.QueueItem
		var vdir, mdir, option string
		var d convention.Dims
		if err := rows.Scan(&it.VariantID, &it.ModelID, &it.Model, &vdir, &mdir, &option,
			&d.Scale, &d.Supports, &d.Density, &d.Format, &d.Fill, &d.Split, &d.Tech, &d.Extra, &it.AddedUnix, &it.Note); err != nil {
			return nil, err
		}
		it.Label = strings.TrimSpace(strings.Join(convention.CanonicalSegments(d), " ") + " " + option)
		out = append(out, it)
	}
	return out, rows.Err()
}

var _ app.ImportLog = (*Store)(nil)

// ImportRecords lists every import record, most recently changed first.
func (s *Store) ImportRecords(ctx context.Context) ([]app.ImportRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT source, signature, state, target, files, message, updated_unix FROM import_unit ORDER BY updated_unix DESC, source`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.ImportRecord
	for rows.Next() {
		var r app.ImportRecord
		if err := rows.Scan(&r.Source, &r.Signature, &r.State, &r.Target, &r.Files, &r.Message, &r.UpdatedUnix); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveImport inserts or replaces a record.
func (s *Store) SaveImport(ctx context.Context, r app.ImportRecord) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO import_unit (source, signature, state, target, files, message, updated_unix) VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(source) DO UPDATE SET signature=excluded.signature, state=excluded.state, target=excluded.target,
			files=excluded.files, message=excluded.message, updated_unix=excluded.updated_unix`,
		r.Source, r.Signature, r.State, r.Target, r.Files, r.Message, r.UpdatedUnix)
	return err
}

func (s *Store) ImportBaselined(ctx context.Context) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM meta WHERE key = 'import_baselined'`).Scan(&n)
	return n > 0, err
}

func (s *Store) SetImportBaselined(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO meta (key, value) VALUES ('import_baselined', '1')`)
	return err
}

// SetHidden hides a model from the library (or shows it again).
func (s *Store) SetHidden(ctx context.Context, modelID int64, hidden bool) error {
	dir, err := s.modelDir(ctx, s.db, modelID)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO model_user (dir, hidden) VALUES (?, ?)
		ON CONFLICT(dir) DO UPDATE SET hidden = excluded.hidden`, dir, hidden)
	return err
}

// SetVariantLabel overrides a variant's dimensions and option; nil removes
// the override.
func (s *Store) SetVariantLabel(ctx context.Context, variantID int64, label *app.VariantLabel) error {
	_, vdir, err := s.variantDirs(ctx, variantID)
	if err != nil {
		return err
	}
	if label == nil {
		_, err = s.db.ExecContext(ctx, `DELETE FROM variant_label WHERE dir = ?`, vdir)
		return err
	}
	d := label.Dims
	_, err = s.db.ExecContext(ctx, `INSERT INTO variant_label (dir, option, scale, supports, density, format, fill, split, tech, extra)
		VALUES (?,?,?,?,?,?,?,?,?,?) ON CONFLICT(dir) DO UPDATE SET option=excluded.option, scale=excluded.scale,
		supports=excluded.supports, density=excluded.density, format=excluded.format, fill=excluded.fill,
		split=excluded.split, tech=excluded.tech, extra=excluded.extra`,
		vdir, label.Option, d.Scale, d.Supports, d.Density, d.Format, d.Fill, d.Split, d.Tech, d.Extra)
	return err
}

var _ app.Settings = (*Store)(nil)

// PrinterSettings returns the saved printer settings.
func (s *Store) PrinterSettings(ctx context.Context) (app.PrinterSettings, bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = 'printer'`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return app.PrinterSettings{}, false, nil
	}
	if err != nil {
		return app.PrinterSettings{}, false, err
	}
	var ps app.PrinterSettings
	if err := json.Unmarshal([]byte(v), &ps); err != nil {
		return app.PrinterSettings{}, false, fmt.Errorf("saved printer settings: %w", err)
	}
	return ps, true, nil
}

// SavePrinterSettings saves the printer settings.
func (s *Store) SavePrinterSettings(ctx context.Context, ps app.PrinterSettings) error {
	b, err := json.Marshal(ps)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES ('printer', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, string(b))
	return err
}

var _ app.NotificationStore = (*Store)(nil)

// NotificationSettings returns the saved notification settings (secrets
// still sealed); all off when none were saved.
func (s *Store) NotificationSettings(ctx context.Context) (app.NotificationSettings, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = 'notifications'`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return app.NotificationSettings{Events: map[string]bool{}}, nil
	}
	if err != nil {
		return app.NotificationSettings{}, err
	}
	var ns app.NotificationSettings
	if err := json.Unmarshal([]byte(v), &ns); err != nil {
		return ns, fmt.Errorf("saved notification settings: %w", err)
	}
	if ns.Events == nil {
		ns.Events = map[string]bool{}
	}
	return ns, nil
}

// SaveNotificationSettings saves the notification settings.
func (s *Store) SaveNotificationSettings(ctx context.Context, ns app.NotificationSettings) error {
	b, err := json.Marshal(ns)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES ('notifications', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, string(b))
	return err
}
