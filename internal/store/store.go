// Package store persists jobs, businesses, coverage cells and lead library in SQLite.
package store

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/timmy2048/leadfinder/internal/coverage"
	"github.com/timmy2048/leadfinder/internal/dedupe"
	"github.com/timmy2048/leadfinder/internal/model"
	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS jobs(id INTEGER PRIMARY KEY AUTOINCREMENT, params TEXT, status TEXT, progress TEXT, error TEXT, created_at TIMESTAMP, count INTEGER DEFAULT 0);
CREATE TABLE IF NOT EXISTS businesses(id INTEGER PRIMARY KEY AUTOINCREMENT, job_id INTEGER, seo_score INTEGER, data TEXT);
CREATE INDEX IF NOT EXISTS idx_b_job ON businesses(job_id);
CREATE TABLE IF NOT EXISTS coverage_cells(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  cov_key TEXT NOT NULL,
  cell_key TEXT NOT NULL,
  lat REAL, lon REAL,
  size_m INTEGER,
  status TEXT,
  scanned_at TIMESTAMP,
  UNIQUE(cov_key, cell_key)
);
CREATE INDEX IF NOT EXISTS idx_cov_key ON coverage_cells(cov_key);
CREATE TABLE IF NOT EXISTS leads(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  lead_key TEXT NOT NULL,
  fingerprint TEXT NOT NULL,
  seo_score INTEGER,
  data TEXT,
  updated_at TIMESTAMP,
  UNIQUE(lead_key, fingerprint)
);
CREATE INDEX IF NOT EXISTS idx_lead_key ON leads(lead_key);`)
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) CreateJob(p model.JobParams) (int64, error) {
	pj, _ := json.Marshal(p)
	r, err := s.db.Exec(`INSERT INTO jobs(params,status,progress,error,created_at) VALUES(?,?,?,?,?)`, string(pj), "pending", "", "", time.Now())
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

func (s *Store) UpdateJob(id int64, status, progress, errMsg string) error {
	_, err := s.db.Exec(`UPDATE jobs SET status=?, progress=?, error=?, count=(SELECT COUNT(*) FROM businesses WHERE job_id=?) WHERE id=?`, status, progress, errMsg, id, id)
	return err
}

func scanJob(sc interface{ Scan(...any) error }) (model.Job, error) {
	var j model.Job
	var p string
	err := sc.Scan(&j.ID, &p, &j.Status, &j.Progress, &j.Error, &j.CreatedAt, &j.Count)
	_ = json.Unmarshal([]byte(p), &j.Params)
	return j, err
}

func (s *Store) GetJob(id int64) (model.Job, error) {
	return scanJob(s.db.QueryRow(`SELECT id,params,status,progress,error,created_at,count FROM jobs WHERE id=?`, id))
}

func (s *Store) ListJobs() ([]model.Job, error) {
	rows, err := s.db.Query(`SELECT id,params,status,progress,error,created_at,count FROM jobs ORDER BY id DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// MarkInterrupted fails jobs left running by a previous process.
func (s *Store) MarkInterrupted() {
	_, _ = s.db.Exec(`UPDATE jobs SET status='failed', error='interrupted (server restarted)' WHERE status IN ('pending','running')`)
}

func (s *Store) SaveBusinesses(jobID int64, bs []model.Business) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM businesses WHERE job_id=?`, jobID); err != nil {
		tx.Rollback()
		return err
	}
	for _, b := range bs {
		b.JobID = jobID
		d, _ := json.Marshal(b)
		if _, err := tx.Exec(`INSERT INTO businesses(job_id,seo_score,data) VALUES(?,?,?)`, jobID, b.SEOScore, string(d)); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Businesses(jobID int64) ([]model.Business, error) {
	rows, err := s.db.Query(`SELECT id,data FROM businesses WHERE job_id=? ORDER BY id`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Business
	for rows.Next() {
		var id int64
		var d string
		if err := rows.Scan(&id, &d); err != nil {
			return nil, err
		}
		var b model.Business
		_ = json.Unmarshal([]byte(d), &b)
		b.ID = id
		out = append(out, b)
	}
	return out, rows.Err()
}

// --- coverage cells ---

func (s *Store) ListCoverage(covKey string) ([]coverage.Cell, error) {
	rows, err := s.db.Query(`SELECT cell_key,lat,lon,size_m,status,scanned_at FROM coverage_cells WHERE cov_key=?`, covKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []coverage.Cell
	for rows.Next() {
		var c coverage.Cell
		var scanned sql.NullTime
		if err := rows.Scan(&c.Key, &c.Lat, &c.Lon, &c.SizeM, &c.Status, &scanned); err != nil {
			return nil, err
		}
		if scanned.Valid {
			c.ScannedAt = scanned.Time
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) MarkCellsDone(covKey string, cells []coverage.Cell) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	now := time.Now()
	for _, c := range cells {
		_, err := tx.Exec(`INSERT INTO coverage_cells(cov_key,cell_key,lat,lon,size_m,status,scanned_at) VALUES(?,?,?,?,?,?,?)
ON CONFLICT(cov_key,cell_key) DO UPDATE SET status='done', scanned_at=excluded.scanned_at, lat=excluded.lat, lon=excluded.lon, size_m=excluded.size_m`,
			covKey, c.Key, c.Lat, c.Lon, c.SizeM, "done", now)
		if err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) CoverageSummary(covKey string, originLat, originLon float64) (coverage.Summary, error) {
	cells, err := s.ListCoverage(covKey)
	if err != nil {
		return coverage.Summary{}, err
	}
	sum := coverage.Summary{Key: covKey, CellCount: len(cells)}
	for _, c := range cells {
		if c.ScannedAt.After(sum.LastScan) {
			sum.LastScan = c.ScannedAt
		}
	}
	sum.MaxRadiusM = coverage.ApproxMaxRadius(originLat, originLon, cells)
	return sum, nil
}

// --- persistent leads library ---

func fingerprint(b model.Business) string {
	if d := dedupe.Domain(b.Website); d != "" {
		return "d:" + d
	}
	if p := dedupe.Phone(b.Phone); p != "" {
		return "p:" + p
	}
	name := strings.ToLower(strings.TrimSpace(b.Name))
	return fmt.Sprintf("n:%.4f:%.4f:%s", b.Lat, b.Lon, name)
}

func (s *Store) LoadLeads(leadKey string) ([]model.Business, error) {
	rows, err := s.db.Query(`SELECT id,data FROM leads WHERE lead_key=? ORDER BY seo_score DESC, id`, leadKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Business
	for rows.Next() {
		var id int64
		var d string
		if err := rows.Scan(&id, &d); err != nil {
			return nil, err
		}
		var b model.Business
		_ = json.Unmarshal([]byte(d), &b)
		b.ID = id
		out = append(out, b)
	}
	return out, rows.Err()
}

// UpsertLeads merges businesses into the lead library for leadKey.
func (s *Store) UpsertLeads(leadKey string, bs []model.Business) error {
	existing, err := s.LoadLeads(leadKey)
	if err != nil {
		return err
	}
	merged := dedupe.Merge(append(append([]model.Business{}, existing...), bs...))
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM leads WHERE lead_key=?`, leadKey); err != nil {
		tx.Rollback()
		return err
	}
	now := time.Now()
	for _, b := range merged {
		d, _ := json.Marshal(b)
		fp := fingerprint(b)
		if _, err := tx.Exec(`INSERT INTO leads(lead_key,fingerprint,seo_score,data,updated_at) VALUES(?,?,?,?,?)`, leadKey, fp, b.SEOScore, string(d), now); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// WriteCSV exports businesses (UTF-8 BOM for Excel).
func WriteCSV(w io.Writer, bs []model.Business) error {
	_, _ = w.Write([]byte("\ufeff"))
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"name", "category", "address", "phone", "website", "emails", "email_status", "socials", "sources", "review_count", "rating", "https", "load_ms", "has_title", "has_meta_desc", "reachable", "best_email", "email_type", "email_types", "same_domain_email", "gmb_score", "points_zh", "points_en", "point_keys", "needs_gmaps", "maps_rating_photos_hours", "lat", "lon"})
	for _, b := range bs {
		var st []string
		for _, e := range b.Emails {
			st = append(st, e+":"+orDash(b.EmailStatus[e]))
		}
		_ = cw.Write([]string{b.Name, b.Category, b.Address, b.Phone, b.Website, strings.Join(b.Emails, ";"), strings.Join(st, ";"), strings.Join(b.Socials, ";"), strings.Join(b.Sources, ";"),
			fmt.Sprint(b.ReviewCount), fmt.Sprint(b.Rating), fmt.Sprint(b.HTTPS), fmt.Sprint(b.LoadMS), fmt.Sprint(b.HasTitle), fmt.Sprint(b.HasMetaDesc), fmt.Sprint(b.Reachable), b.BestEmail, b.EmailType, joinTypes(b), fmt.Sprint(b.SameDomain), fmt.Sprint(b.GMBScore), joinPts(b.Points, "zh"), joinPts(b.Points, "en"), joinPts(b.Points, "key"), needs(b),
			fmt.Sprintf("photos=%d hours=%v cats=%s", b.PhotoCount, b.HasHours, strings.Join(b.Categories, "|")),
			fmt.Sprintf("%.6f", b.Lat), fmt.Sprintf("%.6f", b.Lon)})
	}
	cw.Flush()
	return cw.Error()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func needs(b model.Business) string {
	if b.NeedsGMaps {
		return "需谷歌地图数据"
	}
	return ""
}

func joinPts(ps []model.Point, f string) string {
	var out []string
	for _, p := range ps {
		switch f {
		case "zh":
			out = append(out, fmt.Sprintf("%s(+%d)", p.LabelZH, p.Weight))
		case "en":
			out = append(out, p.PitchEN)
		default:
			out = append(out, p.Key)
		}
	}
	sep := "; "
	if f == "en" {
		sep = " | "
	}
	return strings.Join(out, sep)
}

func joinTypes(b model.Business) string {
	var out []string
	for _, e := range b.Emails {
		out = append(out, e+":"+orDash(b.EmailTypes[e]))
	}
	return strings.Join(out, ";")
}
