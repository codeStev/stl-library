package sqlite

import (
	"context"
	"sort"

	"github.com/codeStev/stl-library/internal/app"
)

// Storage reports where the library's space goes: per creator and release, the biggest models, the kind of
// file, and what identical files waste.
func (s *Store) Storage(ctx context.Context) (app.StorageReport, error) {
	var r app.StorageReport
	rows, err := s.db.QueryContext(ctx, `SELECT creator, release, count(*), sum(bytes) FROM model GROUP BY creator, release`)
	if err != nil {
		return r, err
	}
	byCreator := map[string]*app.StorageCreator{}
	var order []string
	for rows.Next() {
		var creator string
		var rel app.StorageRelease
		if err := rows.Scan(&creator, &rel.Name, &rel.Models, &rel.Bytes); err != nil {
			rows.Close()
			return r, err
		}
		c := byCreator[creator]
		if c == nil {
			c = &app.StorageCreator{Name: creator}
			byCreator[creator] = c
			order = append(order, creator)
		}
		c.Models += rel.Models
		c.Bytes += rel.Bytes
		c.Releases = append(c.Releases, rel)
		r.Models += rel.Models
		r.Bytes += rel.Bytes
	}
	rows.Close()
	for _, name := range order {
		c := byCreator[name]
		sortReleases(c.Releases)
		r.Creators = append(r.Creators, *c)
	}
	sortCreators(r.Creators)

	lrows, err := s.db.QueryContext(ctx, `SELECT m.id, `+modelName+`, m.creator, m.bytes
		FROM model m LEFT JOIN model_user mu ON mu.dir = m.dir ORDER BY m.bytes DESC, m.id LIMIT 25`)
	if err != nil {
		return r, err
	}
	for lrows.Next() {
		var m app.StorageModel
		if err := lrows.Scan(&m.ID, &m.Name, &m.Creator, &m.Bytes); err != nil {
			lrows.Close()
			return r, err
		}
		r.Largest = append(r.Largest, m)
	}
	lrows.Close()

	const ext = `lower(replace(path, rtrim(path, replace(path, '.', '')), ''))`
	krows, err := s.db.QueryContext(ctx, `SELECT e, count(*), sum(size) FROM (
			SELECT `+ext+` AS e, size FROM part UNION ALL SELECT `+ext+`, size FROM image)
		GROUP BY e ORDER BY 3 DESC LIMIT 12`)
	if err != nil {
		return r, err
	}
	for krows.Next() {
		var k app.StorageKind
		if err := krows.Scan(&k.Ext, &k.Files, &k.Bytes); err != nil {
			krows.Close()
			return r, err
		}
		r.Kinds = append(r.Kinds, k)
	}
	krows.Close()

	if err := s.db.QueryRowContext(ctx, `SELECT count(*), coalesce(sum((c - 1) * size), 0) FROM (
			SELECT count(*) AS c, h.size AS size FROM file_hash h JOIN part p ON p.path = h.path GROUP BY h.sha256, h.size HAVING count(*) > 1)`).
		Scan(&r.DuplicateGroups, &r.DuplicateBytes); err != nil {
		return r, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM file_hash h JOIN part p ON p.path = h.path), (SELECT count(*) FROM part)`).
		Scan(&r.Hashed, &r.Total); err != nil {
		return r, err
	}
	r.Files = r.Total
	return r, nil
}

func sortReleases(rs []app.StorageRelease) {
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].Bytes != rs[j].Bytes {
			return rs[i].Bytes > rs[j].Bytes
		}
		return rs[i].Name < rs[j].Name
	})
}

func sortCreators(cs []app.StorageCreator) {
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].Bytes != cs[j].Bytes {
			return cs[i].Bytes > cs[j].Bytes
		}
		return cs[i].Name < cs[j].Name
	})
}
