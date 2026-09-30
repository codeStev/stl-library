package sqlite

import (
	"context"
	"unicode/utf8"
)

// pathKeys are the columns that hold a folder or file path of the library: the index itself and all the
// user data keyed by path.
var pathKeys = []struct{ table, col string }{
	{"model", "dir"}, {"variant", "dir"}, {"part", "path"}, {"image", "path"},
	{"model_user", "dir"}, {"tag", "dir"}, {"print", "dir"}, {"print", "variant_dir"},
	{"queue", "dir"}, {"queue", "variant_dir"}, {"variant_label", "dir"},
	{"slice_content", "slice_path"}, {"slice_content", "part_path"},
	{"collection_model", "dir"}, {"job_item", "part_path"}, {"job_plate", "ref"},
	{"file_hash", "path"}, {"health_event", "path"},
}

// MoveKeys follows a folder that was moved from -> to: everything below it is renamed in the index and in
// all user data (tags, prints, queue, collections, labels, plates, hashes), so a moved model keeps its id,
// its first-seen date and everything people attached to it. Data left at the new place by a model that
// was there before is replaced.
func (s *Store) MoveKeys(ctx context.Context, from, to string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	n := utf8.RuneCountInString(from)
	for _, k := range pathKeys {
		q := `UPDATE OR REPLACE ` + k.table + ` SET ` + k.col + ` = ? || substr(` + k.col + `, ?)
			WHERE ` + k.col + ` = ? OR substr(` + k.col + `, 1, ?) = ?`
		if _, err := tx.ExecContext(ctx, q, to, n+1, from, n+1, from+"/"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// IsModelDir reports whether a folder is the folder of a model in the index.
func (s *Store) IsModelDir(ctx context.Context, dir string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM model WHERE dir = ?`, dir).Scan(&n)
	return n > 0, err
}
