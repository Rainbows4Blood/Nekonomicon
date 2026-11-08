package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/Rainbows4Blood/Nekonomicon/internal/catalog"
	"github.com/Rainbows4Blood/Nekonomicon/internal/enrichdata"
	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
)

func enrichmentSchema() string {
	return `CREATE INDEX IF NOT EXISTS dnd_adventures ON dnd_books(is_adventure,source,product_id);
CREATE TABLE IF NOT EXISTS enrichment_results(source TEXT NOT NULL,product_id INTEGER NOT NULL,version INTEGER NOT NULL,response_fetched_at TEXT NOT NULL,players_json TEXT NOT NULL,levels_json TEXT NOT NULL,min_player INTEGER,max_player INTEGER,min_level INTEGER,max_level INTEGER,players_complete INTEGER NOT NULL,levels_complete INTEGER NOT NULL,status TEXT NOT NULL,message TEXT NOT NULL,model TEXT NOT NULL,cover_hash TEXT NOT NULL,updated_at TEXT NOT NULL,PRIMARY KEY(source,product_id),FOREIGN KEY(source,product_id) REFERENCES products(source,product_id));`
}

const enrichmentFrom = ` FROM dnd_books b JOIN products p USING(source,product_id) JOIN catalog_state c USING(source,product_id) LEFT JOIN enrichment_results e ON e.source=p.source AND e.product_id=p.product_id AND e.response_fetched_at=p.fetched_at AND e.version=1 WHERE b.is_adventure=1 AND c.version=? AND c.response_fetched_at=p.fetched_at AND c.status IN ('complete','warnings')`
const allEnriched = `(coalesce(e.players_done,0)=1 AND coalesce(e.levels_done,0)=1 AND coalesce(e.settings_done,0)=1 AND coalesce(e.locations_done,0)=1 AND coalesce(e.dates_done,0)=1)`
const missingEnrichment = ` AND NOT ` + allEnriched

type EnrichmentCounts struct {
	Adventures    int64 `json:"adventures"`
	Players       int64 `json:"players"`
	Levels        int64 `json:"levels"`
	Settings      int64 `json:"settings"`
	Locations     int64 `json:"locations"`
	Dates         int64 `json:"dates"`
	Pending       int64 `json:"pending"`
	Unknown       int64 `json:"unknown"`
	Failed        int64 `json:"failed"`
	MissingCovers int64 `json:"missingCovers"`
}
type EnrichmentBook struct {
	Source        storefront.Source `json:"source"`
	ID            int64             `json:"id"`
	Name          string            `json:"name"`
	Fetched       string            `json:"-"`
	Raw           []byte            `json:"-"`
	Existing      enrichdata.Result `json:"result"`
	Status        string            `json:"status"`
	Message       string            `json:"message"`
	PlayersDone   bool              `json:"playersDone"`
	LevelsDone    bool              `json:"levelsDone"`
	SettingsDone  bool              `json:"settingsDone"`
	LocationsDone bool              `json:"locationsDone"`
	DatesDone     bool              `json:"datesDone"`
}

func (b EnrichmentBook) Done() bool {
	return b.PlayersDone && b.LevelsDone && b.SettingsDone && b.LocationsDone && b.DatesDone
}

func (s *Store) EnrichmentCounts(ctx context.Context) (EnrichmentCounts, error) {
	var v EnrichmentCounts
	err := s.db.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(e.players_done),0),coalesce(sum(e.levels_done),0),coalesce(sum(e.settings_done),0),coalesce(sum(e.locations_done),0),coalesce(sum(e.dates_done),0),coalesce(sum(NOT `+allEnriched+`),0),coalesce(sum(`+allEnriched+` AND (e.players_complete=0 OR e.levels_complete=0 OR e.settings_complete=0 OR e.locations_complete=0 OR e.dates_complete=0)),0),coalesce(sum(e.status='failed'),0),coalesce(sum(NOT EXISTS(SELECT 1 FROM covers cv WHERE cv.source=b.source AND cv.product_id=b.product_id)),0)`+enrichmentFrom, catalog.Version).Scan(&v.Adventures, &v.Players, &v.Levels, &v.Settings, &v.Locations, &v.Dates, &v.Pending, &v.Unknown, &v.Failed, &v.MissingCovers)
	return v, err
}
func (s *Store) NextEnrichment(ctx context.Context, source storefront.Source, id int64) (*EnrichmentBook, error) {
	var b EnrichmentBook
	var players, levels, settings, locations, dates string
	err := s.db.QueryRowContext(ctx, `SELECT b.source,b.product_id,b.name,p.fetched_at,p.response_json,coalesce(e.players_json,'{}'),coalesce(e.levels_json,'{}'),coalesce(e.settings_json,'{}'),coalesce(e.locations_json,'{}'),coalesce(e.dates_json,'{}'),coalesce(e.players_done,0),coalesce(e.levels_done,0),coalesce(e.settings_done,0),coalesce(e.locations_done,0),coalesce(e.dates_done,0)`+enrichmentFrom+missingEnrichment+` AND (b.source,b.product_id)>(?,?) ORDER BY b.source,b.product_id LIMIT 1`, catalog.Version, source, id).Scan(&b.Source, &b.ID, &b.Name, &b.Fetched, &b.Raw, &players, &levels, &settings, &locations, &dates, &b.PlayersDone, &b.LevelsDone, &b.SettingsDone, &b.LocationsDone, &b.DatesDone)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if decodeEnrichment(&b.Existing, players, levels, settings, locations, dates) != nil {
		return nil, errors.New("Invalid saved enrichment")
	}
	return &b, nil
}
func (s *Store) SaveEnrichment(ctx context.Context, b EnrichmentBook, result enrichdata.Result, hash, message string) error {
	if err := result.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	playersDone := b.PlayersDone || result.Players.Complete()
	levelsDone := b.LevelsDone || result.Levels.Complete()
	settingsDone := b.SettingsDone || result.Settings.Known()
	locationsDone := b.LocationsDone || result.Locations.Known()
	datesDone := b.DatesDone || result.Dates.Known()
	status := "complete"
	if !playersDone || !levelsDone || !settingsDone || !locationsDone || !datesDone {
		status = "partial"
	}
	if message != "" {
		status = "failed"
	}
	players, _ := json.Marshal(result.Players)
	levels, _ := json.Marshal(result.Levels)
	settings, _ := json.Marshal(result.Settings)
	locations, _ := json.Marshal(result.Locations)
	dates, _ := json.Marshal(result.Dates)
	r, err := s.db.ExecContext(ctx, `INSERT INTO enrichment_results(source,product_id,version,response_fetched_at,players_json,levels_json,min_player,max_player,min_level,max_level,players_complete,levels_complete,status,message,model,cover_hash,updated_at,players_done,levels_done,settings_json,locations_json,dates_json,settings_done,locations_done,dates_done,settings_complete,locations_complete,dates_complete) SELECT ?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM products WHERE source=? AND product_id=? AND fetched_at=?) ON CONFLICT(source,product_id) DO UPDATE SET version=excluded.version,response_fetched_at=excluded.response_fetched_at,players_json=excluded.players_json,levels_json=excluded.levels_json,min_player=excluded.min_player,max_player=excluded.max_player,min_level=excluded.min_level,max_level=excluded.max_level,players_complete=excluded.players_complete,levels_complete=excluded.levels_complete,status=excluded.status,message=excluded.message,model=excluded.model,cover_hash=excluded.cover_hash,updated_at=excluded.updated_at,players_done=excluded.players_done,levels_done=excluded.levels_done,settings_json=excluded.settings_json,locations_json=excluded.locations_json,dates_json=excluded.dates_json,settings_done=excluded.settings_done,locations_done=excluded.locations_done,dates_done=excluded.dates_done,settings_complete=excluded.settings_complete,locations_complete=excluded.locations_complete,dates_complete=excluded.dates_complete`, b.Source, b.ID, enrichdata.Version, b.Fetched, string(players), string(levels), result.Players.Min, result.Players.Max, result.Levels.Min, result.Levels.Max, result.Players.Complete(), result.Levels.Complete(), status, message, enrichdata.Model, hash, now(), playersDone, levelsDone, string(settings), string(locations), string(dates), settingsDone, locationsDone, datesDone, result.Settings.Known(), result.Locations.Known(), result.Dates.Known(), b.Source, b.ID, b.Fetched)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return errors.New("Source changed before enrichment could be saved")
	}
	return nil
}
func (s *Store) EnrichmentHistory(ctx context.Context, offset int) ([]EnrichmentBook, error) {
	if offset < 0 {
		offset = 0
	}
	if offset > 1000000 {
		offset = 1000000
	}
	rows, err := s.db.QueryContext(ctx, `SELECT b.source,b.product_id,b.name,e.players_json,e.levels_json,e.settings_json,e.locations_json,e.dates_json,e.status,e.message,e.players_done,e.levels_done,e.settings_done,e.locations_done,e.dates_done`+enrichmentFrom+` AND e.source IS NOT NULL ORDER BY e.updated_at DESC,b.source,b.product_id LIMIT 30 OFFSET ?`, catalog.Version, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EnrichmentBook{}
	for rows.Next() {
		var b EnrichmentBook
		var p, l, settings, locations, dates string
		if err = rows.Scan(&b.Source, &b.ID, &b.Name, &p, &l, &settings, &locations, &dates, &b.Status, &b.Message, &b.PlayersDone, &b.LevelsDone, &b.SettingsDone, &b.LocationsDone, &b.DatesDone); err != nil {
			return nil, err
		}
		if decodeEnrichment(&b.Existing, p, l, settings, locations, dates) != nil {
			return nil, errors.New("Invalid enrichment")
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
func (s *Store) BookEnrichment(ctx context.Context, source storefront.Source, id int64) (enrichdata.Result, error) {
	b, err := s.bookEnrichment(ctx, source, id)
	return b.Existing, err
}
func (s *Store) bookEnrichment(ctx context.Context, source storefront.Source, id int64) (EnrichmentBook, error) {
	var b EnrichmentBook
	var p, l, settings, locations, dates string
	err := s.db.QueryRowContext(ctx, `SELECT e.players_json,e.levels_json,e.settings_json,e.locations_json,e.dates_json,e.players_done,e.levels_done,e.settings_done,e.locations_done,e.dates_done FROM enrichment_results e JOIN products p USING(source,product_id) WHERE e.source=? AND e.product_id=? AND e.response_fetched_at=p.fetched_at AND e.version=?`, source, id, enrichdata.Version).Scan(&p, &l, &settings, &locations, &dates, &b.PlayersDone, &b.LevelsDone, &b.SettingsDone, &b.LocationsDone, &b.DatesDone)
	if errors.Is(err, sql.ErrNoRows) {
		return b, nil
	}
	if err != nil {
		return b, err
	}
	err = decodeEnrichment(&b.Existing, p, l, settings, locations, dates)
	return b, err
}
func decodeEnrichment(r *enrichdata.Result, p, l, s, loc, d string) error {
	for _, v := range []struct {
		raw    string
		target any
	}{{p, &r.Players}, {l, &r.Levels}, {s, &r.Settings}, {loc, &r.Locations}, {d, &r.Dates}} {
		if err := json.Unmarshal([]byte(v.raw), v.target); err != nil {
			return err
		}
	}
	return r.Validate()
}
