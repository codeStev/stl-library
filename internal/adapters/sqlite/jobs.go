package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/codeStev/stl-library/internal/app"
)

// Jobs lists the prints, newest first.
func (s *Store) Jobs(ctx context.Context) ([]app.JobSummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT j.id, j.name, j.state, j.created_unix, j.printed_unix,
			(SELECT count(*) FROM job_item i WHERE i.job_id = j.id),
			(SELECT coalesce(sum(count), 0) FROM job_item i WHERE i.job_id = j.id),
			(SELECT count(*) FROM job_plate p WHERE p.job_id = j.id)
		FROM print_job j ORDER BY j.created_unix DESC, j.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.JobSummary
	for rows.Next() {
		var j app.JobSummary
		if err := rows.Scan(&j.ID, &j.Name, &j.State, &j.CreatedUnix, &j.PrintedUnix, &j.Items, &j.Copies, &j.Plates); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// Job returns a print with its parts and plates.
func (s *Store) Job(ctx context.Context, id int64) (*app.Job, error) {
	j := &app.Job{ID: id}
	err := s.db.QueryRowContext(ctx, `SELECT name, note, state, created_unix, printed_unix FROM print_job WHERE id = ?`, id).
		Scan(&j.Name, &j.Note, &j.State, &j.CreatedUnix, &j.PrintedUnix)
	if err != nil {
		return nil, notFound(err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT i.part_path, i.count, p.id, m.id, `+modelName+`
		FROM job_item i LEFT JOIN part p ON p.path = i.part_path
		LEFT JOIN variant v ON v.id = p.variant_id LEFT JOIN model m ON m.id = v.model_id
		LEFT JOIN model_user mu ON mu.dir = m.dir
		WHERE i.job_id = ? ORDER BY i.part_path`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var it app.JobItem
		var pid, mid *int64
		var mname *string
		if err := rows.Scan(&it.Path, &it.Count, &pid, &mid, &mname); err != nil {
			rows.Close()
			return nil, err
		}
		if pid == nil {
			it.Missing = true
		} else {
			it.PartID, it.ModelID, it.ModelName = *pid, *mid, *mname
		}
		j.Items = append(j.Items, it)
	}
	rows.Close()
	prow, err := s.db.QueryContext(ctx, `SELECT pl.ref, p.id, u.id, u.name, u.size
		FROM job_plate pl LEFT JOIN part p ON p.path = pl.ref
		LEFT JOIN upload u ON 'upload:' || u.id = pl.ref
		WHERE pl.job_id = ? ORDER BY pl.ref`, id)
	if err != nil {
		return nil, err
	}
	defer prow.Close()
	for prow.Next() {
		var pl app.JobPlate
		var ref string
		var pid *int64
		var uid, uname *string
		var usize *int64
		if err := prow.Scan(&ref, &pid, &uid, &uname, &usize); err != nil {
			return nil, err
		}
		switch {
		case uid != nil:
			pl.UploadID, pl.Name, pl.Size, pl.Path = *uid, *uname, *usize, ref
		case pid != nil:
			pl.PartID, pl.Path = *pid, ref
		default:
			pl.Path, pl.Missing = ref, true
		}
		if pl.Name == "" {
			pl.Name = baseName(ref)
		}
		j.Plates = append(j.Plates, pl)
	}
	return j, prow.Err()
}

func baseName(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}

func (s *Store) CreateJob(ctx context.Context, name, note string, atUnix int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO print_job (name, note, created_unix) VALUES (?, ?, ?)`, name, note, atUnix)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateJob(ctx context.Context, id int64, name, note, state string, printedUnix int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE print_job SET name = ?, note = ?, state = ?, printed_unix = ? WHERE id = ?`, name, note, state, printedUnix, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return app.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteJob(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM print_job WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return app.ErrNotFound
	}
	return nil
}

// SetJobItems replaces the parts of a job; every part id must exist.
func (s *Store) SetJobItems(ctx context.Context, id int64, items []app.SliceItem) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var one int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM print_job WHERE id = ?`, id).Scan(&one); err != nil {
		return notFound(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM job_item WHERE job_id = ?`, id); err != nil {
		return err
	}
	for _, it := range items {
		var p string
		if err := tx.QueryRowContext(ctx, `SELECT path FROM part WHERE id = ?`, it.PartID).Scan(&p); err != nil {
			return notFound(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO job_item (job_id, part_path, count) VALUES (?, ?, ?)`, id, p, it.Count); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetJobPlates replaces the plates of a job; every library part and upload must exist.
func (s *Store) SetJobPlates(ctx context.Context, id int64, plates []app.PlateRef) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var one int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM print_job WHERE id = ?`, id).Scan(&one); err != nil {
		return notFound(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM job_plate WHERE job_id = ?`, id); err != nil {
		return err
	}
	for _, p := range plates {
		ref := ""
		if p.UploadID != "" {
			if err := tx.QueryRowContext(ctx, `SELECT 1 FROM upload WHERE id = ?`, p.UploadID).Scan(&one); err != nil {
				return notFound(err)
			}
			ref = "upload:" + p.UploadID
		} else if err := tx.QueryRowContext(ctx, `SELECT path FROM part WHERE id = ?`, p.PartID).Scan(&ref); err != nil {
			return notFound(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO job_plate (job_id, ref) VALUES (?, ?)`, id, ref); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AddUpload records an uploaded plate (the same content again just refreshes the name).
func (s *Store) AddUpload(ctx context.Context, u app.Upload) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO upload (id, name, size, created_unix) VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name = excluded.name`, u.ID, u.Name, u.Size, u.CreatedUnix)
	return err
}

func (s *Store) Upload(ctx context.Context, id string) (*app.Upload, error) {
	u := app.Upload{ID: id}
	err := s.db.QueryRowContext(ctx, `SELECT name, size, created_unix FROM upload WHERE id = ?`, id).Scan(&u.Name, &u.Size, &u.CreatedUnix)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, app.ErrNotFound
	}
	return &u, err
}

// PrintedParts counts, per part of a variant, the copies in printed jobs.
func (s *Store) PrintedParts(ctx context.Context, variantID int64) (map[int64]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.id, sum(i.count)
		FROM part p JOIN job_item i ON i.part_path = p.path
		JOIN print_job j ON j.id = i.job_id AND j.state = 'printed'
		WHERE p.variant_id = ? GROUP BY p.id`, variantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}
