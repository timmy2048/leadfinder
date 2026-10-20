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

// EmailSourceZH renders an email source label, e.g. "官网 /contact", "Facebook", "猜测".
func EmailSourceZH(s string) string {
	if s == "" {
		return ""
	}
	k, path, _ := strings.Cut(s, ":")
	m := map[string]string{"site": "官网", "sitemap": "官网(sitemap)", "js": "官网(JS渲染)", "facebook": "Facebook", "instagram": "Instagram", "tiktok": "TikTok",
		"import": "导入CSV", "gmaps": "谷歌地图", "osm": "OSM"}
	if z, ok := m[k]; ok {
		k = z
	}
	if path != "" {
		return k + " " + path
	}
	return k
}

// InterruptedJobs returns jobs left pending/running by a previous process.
func (s *Store) InterruptedJobs() ([]model.Job, error) {
	all, err := s.ListJobs()
	var out []model.Job
	for _, j := range all {
		if j.Status == "pending" || j.Status == "running" {
			out = append(out, j)
		}
	}
	return out, err
}

// MarkInterruptedImports fails interrupted CSV-import jobs (rows are not kept).
func (s *Store) MarkInterruptedImports() {
	_, _ = s.db.Exec(`UPDATE jobs SET status='failed', error='interrupted (server restarted); re-upload CSV' WHERE status IN ('pending','running') AND params LIKE '%"import_file":"%'`)
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
	merged := dedupe.Merge(append(append([]model.Business{}, bs...), existing...)) // newest first so fresh enrichment wins
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

// WriteCSV exports the outreach-relevant columns (UTF-8 BOM for Excel).
// Internal fields (sources, coords, keys, verification, socials) stay in the DB/JSON.
func WriteCSV(w io.Writer, bs []model.Business) error {
	_, _ = w.Write([]byte("\ufeff"))
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"name", "primary_email", "email_type", "email_source", "other_emails", "phone", "website", "address", "rating", "review_count", "maps_rank", "gmb_score", "points_zh", "points_en"})
	for _, b := range bs {
		_ = cw.Write([]string{b.Name, b.BestEmail, EmailTypeZH(b.EmailType), EmailSourceZH(b.EmailSources[b.BestEmail]), OtherEmails(b), b.Phone, b.Website, b.Address,
			ratingStr(b.Rating), fmt.Sprint(b.ReviewCount), RankLabel(b), fmt.Sprint(b.GMBScore), joinPts(b.Points, "zh"), joinPts(b.Points, "en")})
	}
	cw.Flush()
	return cw.Error()
}

func ratingStr(r float64) string {
	if r == 0 {
		return ""
	}
	return fmt.Sprint(r)
}

// RankLabel renders the Maps rank: "3 (前三)", ">20", or "" when unchecked.
func RankLabel(b model.Business) string {
	switch {
	case b.MapsRank > 0 && b.InLocalPack:
		return fmt.Sprintf("%d (前三)", b.MapsRank)
	case b.MapsRank > 0:
		return fmt.Sprint(b.MapsRank)
	case b.MapsRank < 0:
		return fmt.Sprintf(">%d", max1(b.MapsRankN))
	}
	return ""
}

func max1(n int) int {
	if n <= 0 {
		return 20
	}
	return n
}

// EmailTypeZH translates the email tier.
func EmailTypeZH(t string) string {
	switch t {
	case "personal":
		return "个人"
	case "named":
		return "具名"
	case "generic":
		return "通用"
	}
	return t
}

// OtherEmails lists non-primary emails with their type.
func OtherEmails(b model.Business) string {
	var out []string
	for _, e := range b.Emails {
		if e != b.BestEmail {
			t := EmailTypeZH(b.EmailTypes[e])
			if src := b.EmailSources[e]; src != "" {
				t += ", " + EmailSourceZH(src)
			}
			out = append(out, e+"("+t+")")
		}
	}
	return strings.Join(out, "; ")
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
