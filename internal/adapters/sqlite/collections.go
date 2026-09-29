package sqlite

import (
	"context"

	"github.com/codeStev/stl-library/internal/app"
)

const collectionCols = `c.id, c.name, c.note, (SELECT count(*) FROM collection_model cm JOIN model m ON m.dir = cm.dir WHERE cm.collection_id = c.id)`

func scanCollections(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]app.Collection, error) {
	var out []app.Collection
	for rows.Next() {
		var c app.Collection
		if err := rows.Scan(&c.ID, &c.Name, &c.Note, &c.Models); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Collections lists the collections by name, with the number of models
// each has in the library now.
func (s *Store) Collections(ctx context.Context) ([]app.Collection, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+collectionCols+` FROM collection c ORDER BY c.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCollections(rows)
}

// CreateCollection adds a collection; the name is unique regardless of case.
func (s *Store) CreateCollection(ctx context.Context, name, note string, atUnix int64) (app.Collection, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO collection (name, note, created_unix) VALUES (?, ?, ?)`, name, note, atUnix)
	if uniqueViolation(err) {
		return app.Collection{}, app.ErrExists
	}
	if err != nil {
		return app.Collection{}, err
	}
	id, err := res.LastInsertId()
	return app.Collection{ID: id, Name: name, Note: note}, err
}

func (s *Store) UpdateCollection(ctx context.Context, id int64, name, note string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE collection SET name = ?, note = ? WHERE id = ?`, name, note, id)
	if uniqueViolation(err) {
		return app.ErrExists
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return app.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteCollection(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM collection WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return app.ErrNotFound
	}
	return nil
}

// EditCollection adds and removes models in one transaction; an unknown
// collection or model id changes nothing.
func (s *Store) EditCollection(ctx context.Context, id int64, add, remove []int64, atUnix int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var one int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM collection WHERE id = ?`, id).Scan(&one); err != nil {
		return notFound(err)
	}
	for _, m := range remove {
		dir, err := s.modelDir(ctx, tx, m)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM collection_model WHERE collection_id = ? AND dir = ?`, id, dir); err != nil {
			return err
		}
	}
	for _, m := range add {
		dir, err := s.modelDir(ctx, tx, m)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO collection_model (collection_id, dir, added_unix) VALUES (?, ?, ?)`, id, dir, atUnix); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ModelCollections returns the collections a model is in.
func (s *Store) ModelCollections(ctx context.Context, modelID int64) ([]app.Collection, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+collectionCols+` FROM collection c
		WHERE EXISTS (SELECT 1 FROM collection_model cm JOIN model m ON m.dir = cm.dir WHERE cm.collection_id = c.id AND m.id = ?)
		ORDER BY c.name COLLATE NOCASE`, modelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCollections(rows)
}
