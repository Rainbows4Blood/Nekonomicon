package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Rainbows4Blood/Nekonomicon/internal/catalog"
	"github.com/Rainbows4Blood/Nekonomicon/internal/enrichdata"
)

type EnrichmentOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}
type EnrichmentOptions struct {
	Options    []EnrichmentOption `json:"options"`
	Next       string             `json:"next"`
	CatalogKey string             `json:"catalogKey"`
}

// Enumerate current adventure values independently of the active browse filters,
// so changing another filter never silently removes a selected choice. Read one
// bounded page and one representative saved value per folded key for its label.
func (s *Store) EnrichmentOptions(ctx context.Context, kind, after string) (EnrichmentOptions, error) {
	out := EnrichmentOptions{Options: []EnrichmentOption{}}
	column := ""
	switch kind {
	case "setting":
		column = "settings_json"
	case "location":
		column = "locations_json"
	case "world-date", "calendar":
		column = "dates_json"
	default:
		return out, errors.New("Unknown enrichment filter")
	}
	if len(after) > 4096 {
		return out, errors.New("Invalid filter page")
	}
	if s.Version() != SchemaVersion {
		return out, errors.New("Apply the database update in Data maintenance first")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT t.value,min(e.`+column+`) FROM enrichment_terms t INDEXED BY enrichment_terms_value
 JOIN enrichment_results e ON e.source=t.source AND e.product_id=t.product_id
 JOIN products p ON p.source=t.source AND p.product_id=t.product_id
 JOIN dnd_books b ON b.source=t.source AND b.product_id=t.product_id
 JOIN catalog_state c ON c.source=t.source AND c.product_id=t.product_id
 WHERE t.kind=? AND t.value>? AND e.version=? AND e.response_fetched_at=p.fetched_at
 AND b.is_adventure=1 AND c.version=? AND c.response_fetched_at=p.fetched_at AND c.status IN ('complete','warnings')
 GROUP BY t.value ORDER BY t.value LIMIT 101`, kind, after, enrichdata.Version, catalog.Version)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		if len(out.Options) == 100 {
			out.Next = out.Options[99].Value
			break
		}
		var key, raw string
		if err = rows.Scan(&key, &raw); err != nil {
			return out, err
		}
		label, err := enrichmentOptionLabel(kind, key, raw)
		if err != nil {
			return out, err
		}
		out.Options = append(out.Options, EnrichmentOption{Value: key, Label: label})
	}
	return out, rows.Err()
}

func enrichmentOptionLabel(kind, key, raw string) (string, error) {
	var values []string
	switch kind {
	case "setting":
		var r enrichdata.Settings
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			return "", err
		}
		if key == "agnostic" && r.Agnostic {
			return "Agnostic (any campaign setting)", nil
		}
		values = r.Names
	case "location":
		var r enrichdata.Locations
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			return "", err
		}
		for _, v := range r.Items {
			values = append(values, v.Name)
		}
	default:
		var r enrichdata.Dates
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			return "", err
		}
		for _, v := range r.Items {
			if kind == "world-date" {
				values = append(values, v.Date)
			} else if v.Calendar != nil {
				values = append(values, *v.Calendar)
			}
		}
	}
	for _, value := range values {
		if catalog.NameKey(strings.TrimSpace(value)) == key {
			return strings.TrimSpace(value), nil
		}
	}
	return "", errors.New("Enrichment filter index does not match the saved result")
}
