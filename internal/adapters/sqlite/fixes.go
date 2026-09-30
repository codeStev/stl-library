package sqlite

import (
	"context"

	"github.com/codeStev/stl-library/internal/app"
)

func (s *Store) AddFix(ctx context.Context, from, to string, atUnix int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO fix_journal (from_path, to_path, at_unix) VALUES (?, ?, ?)`, from, to, atUnix)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) Fixes(ctx context.Context) ([]app.FixRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, from_path, to_path, at_unix, undone FROM fix_journal ORDER BY id DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.FixRecord
	for rows.Next() {
		var r app.FixRecord
		if err := rows.Scan(&r.ID, &r.From, &r.To, &r.AtUnix, &r.Undone); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) Fix(ctx context.Context, id int64) (*app.FixRecord, error) {
	r := app.FixRecord{ID: id}
	err := s.db.QueryRowContext(ctx, `SELECT from_path, to_path, at_unix, undone FROM fix_journal WHERE id = ?`, id).Scan(&r.From, &r.To, &r.AtUnix, &r.Undone)
	if err != nil {
		return nil, notFound(err)
	}
	return &r, nil
}

func (s *Store) MarkFixUndone(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE fix_journal SET undone = 1 WHERE id = ?`, id)
	return err
}
