package sqlite

import (
	"context"

	"github.com/codeStev/stl-library/internal/app"
)

// modelName is what the library shows for a model.
const modelName = `coalesce(nullif(mu.display_name, ''), m.name)`

func (s *Store) partPath(ctx context.Context, partID int64) (string, error) {
	var p string
	err := s.db.QueryRowContext(ctx, `SELECT path FROM part WHERE id = ?`, partID).Scan(&p)
	return p, notFound(err)
}

// SliceContents returns what the part contains (when it is a sliced file)
// and the sliced files that contain it.
func (s *Store) SliceContents(ctx context.Context, partID int64) (app.SliceInfo, error) {
	var info app.SliceInfo
	path, err := s.partPath(ctx, partID)
	if err != nil {
		return info, err
	}
	load := func(query string) ([]app.PartRef, error) {
		rows, err := s.db.QueryContext(ctx, query, path)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []app.PartRef
		for rows.Next() {
			var r app.PartRef
			var id, modelID *int64
			var name *string
			if err := rows.Scan(&r.Path, &r.Count, &id, &modelID, &name); err != nil {
				return nil, err
			}
			if id == nil {
				r.Missing = true
			} else {
				r.PartID, r.ModelID, r.ModelName = *id, *modelID, *name
			}
			out = append(out, r)
		}
		return out, rows.Err()
	}
	join := ` LEFT JOIN variant v ON v.id = p.variant_id LEFT JOIN model m ON m.id = v.model_id
		LEFT JOIN model_user mu ON mu.dir = m.dir `
	if info.Contents, err = load(`SELECT sc.part_path, sc.count, p.id, m.id, ` + modelName + `
		FROM slice_content sc LEFT JOIN part p ON p.path = sc.part_path` + join + `
		WHERE sc.slice_path = ? ORDER BY sc.part_path`); err != nil {
		return info, err
	}
	info.UsedIn, err = load(`SELECT sc.slice_path, sc.count, p.id, m.id, ` + modelName + `
		FROM slice_content sc LEFT JOIN part p ON p.path = sc.slice_path` + join + `
		WHERE sc.part_path = ? AND sc.slice_path NOT LIKE 'upload:%' ORDER BY sc.slice_path`)
	if err != nil {
		return info, err
	}
	// Uploaded plates that hold the part.
	urows, err := s.db.QueryContext(ctx, `SELECT u.id, u.name, sc.count FROM slice_content sc
		JOIN upload u ON 'upload:' || u.id = sc.slice_path WHERE sc.part_path = ? ORDER BY u.name`, path)
	if err != nil {
		return info, err
	}
	defer urows.Close()
	for urows.Next() {
		var r app.PartRef
		if err := urows.Scan(&r.UploadID, &r.Path, &r.Count); err != nil {
			return info, err
		}
		info.UsedIn = append(info.UsedIn, r)
	}
	return info, urows.Err()
}

// UploadContents returns what an uploaded plate contains.
func (s *Store) UploadContents(ctx context.Context, uploadID string) ([]app.PartRef, error) {
	var one int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM upload WHERE id = ?`, uploadID).Scan(&one); err != nil {
		return nil, notFound(err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT sc.part_path, sc.count, p.id, m.id, `+modelName+`
		FROM slice_content sc LEFT JOIN part p ON p.path = sc.part_path
		LEFT JOIN variant v ON v.id = p.variant_id LEFT JOIN model m ON m.id = v.model_id
		LEFT JOIN model_user mu ON mu.dir = m.dir
		WHERE sc.slice_path = ? ORDER BY sc.part_path`, "upload:"+uploadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.PartRef
	for rows.Next() {
		var r app.PartRef
		var id, modelID *int64
		var name *string
		if err := rows.Scan(&r.Path, &r.Count, &id, &modelID, &name); err != nil {
			return nil, err
		}
		if id == nil {
			r.Missing = true
		} else {
			r.PartID, r.ModelID, r.ModelName = *id, *modelID, *name
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetUploadContents replaces what an uploaded plate contains.
func (s *Store) SetUploadContents(ctx context.Context, uploadID string, items []app.SliceItem) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var one int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM upload WHERE id = ?`, uploadID).Scan(&one); err != nil {
		return notFound(err)
	}
	slice := "upload:" + uploadID
	if _, err := tx.ExecContext(ctx, `DELETE FROM slice_content WHERE slice_path = ?`, slice); err != nil {
		return err
	}
	for _, it := range items {
		var p string
		if err := tx.QueryRowContext(ctx, `SELECT path FROM part WHERE id = ?`, it.PartID).Scan(&p); err != nil {
			return notFound(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO slice_content (slice_path, part_path, count) VALUES (?, ?, ?)`, slice, p, it.Count); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetSliceContents replaces what a sliced file contains. Every part id
// must exist.
func (s *Store) SetSliceContents(ctx context.Context, partID int64, items []app.SliceItem) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var slice string
	if err := tx.QueryRowContext(ctx, `SELECT path FROM part WHERE id = ?`, partID).Scan(&slice); err != nil {
		return notFound(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM slice_content WHERE slice_path = ?`, slice); err != nil {
		return err
	}
	for _, it := range items {
		var p string
		if err := tx.QueryRowContext(ctx, `SELECT path FROM part WHERE id = ?`, it.PartID).Scan(&p); err != nil {
			return notFound(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO slice_content (slice_path, part_path, count) VALUES (?, ?, ?)`, slice, p, it.Count); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// VariantSlices lists for each part of a variant the sliced files it is in.
func (s *Store) VariantSlices(ctx context.Context, variantID int64) (map[int64][]app.PartRef, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.id, sc.slice_path, sc.count, sp.id, m.id, `+modelName+`
		FROM part p JOIN slice_content sc ON sc.part_path = p.path
		LEFT JOIN part sp ON sp.path = sc.slice_path
		LEFT JOIN variant v ON v.id = sp.variant_id LEFT JOIN model m ON m.id = v.model_id
		LEFT JOIN model_user mu ON mu.dir = m.dir
		WHERE p.variant_id = ? ORDER BY sc.slice_path`, variantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]app.PartRef{}
	for rows.Next() {
		var partID int64
		var r app.PartRef
		var id, modelID *int64
		var name *string
		if err := rows.Scan(&partID, &r.Path, &r.Count, &id, &modelID, &name); err != nil {
			return nil, err
		}
		if id == nil {
			r.Missing = true
		} else {
			r.PartID, r.ModelID, r.ModelName = *id, *modelID, *name
		}
		out[partID] = append(out[partID], r)
	}
	return out, rows.Err()
}
