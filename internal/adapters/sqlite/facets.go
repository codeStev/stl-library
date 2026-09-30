package sqlite

import (
	"context"

	"github.com/codeStev/stl-library/internal/app"
)

// Facets lists, per variant dimension, the values in use with the number of models having each.
func (s *Store) Facets(ctx context.Context) (map[string][]app.FacetCount, error) {
	out := map[string][]app.FacetCount{}
	for _, col := range []string{"scale", "supports", "format", "fill"} {
		rows, err := s.db.QueryContext(ctx, `SELECT coalesce(l.`+col+`, v.`+col+`) AS val, count(DISTINCT v.model_id)
			FROM variant v LEFT JOIN variant_label l ON l.dir = v.dir
			GROUP BY val HAVING val != '' ORDER BY 2 DESC, val LIMIT 60`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var f app.FacetCount
			if err := rows.Scan(&f.Value, &f.Models); err != nil {
				rows.Close()
				return nil, err
			}
			out[col] = append(out[col], f)
		}
		rows.Close()
	}
	return out, nil
}
