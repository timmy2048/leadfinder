// Package model holds shared data types.
package model

import "time"

// Business is a normalized lead from any source.
type Business struct {
	ID          int64    `json:"id"`
	JobID       int64    `json:"job_id"`
	Name        string   `json:"name"`
	Category    string   `json:"category"`
	Address     string   `json:"address"`
	Lat         float64  `json:"lat"`
	Lon         float64  `json:"lon"`
	Phone       string   `json:"phone"`
	Website     string   `json:"website"`
	Emails      []string `json:"emails"`
	Socials     []string `json:"socials"`
	Sources     []string `json:"sources"`
	ReviewCount int      `json:"review_count"`
	Rating      float64  `json:"rating"`
	// Website audit
	HTTPS        bool              `json:"https"`
	LoadMS       int64             `json:"load_ms"`
	HasTitle     bool              `json:"has_title"`
	HasMetaDesc  bool              `json:"has_meta_desc"`
	SiteReached  bool              `json:"site_reached"`
	EmailStatus  map[string]string `json:"email_status"` // email -> valid/risky/invalid/unknown
	SEOScore     int               `json:"seo_score"`
	ScoreReasons []string          `json:"score_reasons"`
}

// JobParams are user inputs for a job.
type JobParams struct {
	Category    string `json:"category"`
	Location    string `json:"location"`
	Country     string `json:"country"`
	Lang        string `json:"lang"`
	RadiusM     int    `json:"radius_m"`
	Limit       int    `json:"limit"`
	UseGMaps    bool   `json:"use_gmaps"`
	UseOSM      bool   `json:"use_osm"`
	Social      bool   `json:"social"`
	SMTPCheck   bool   `json:"smtp_check"`
	GMapsMaxSec int    `json:"gmaps_max_sec"`
}

// Job is a pipeline run.
type Job struct {
	ID        int64     `json:"id"`
	Params    JobParams `json:"params"`
	Status    string    `json:"status"` // pending, running, done, failed
	Progress  string    `json:"progress"`
	Error     string    `json:"error"`
	CreatedAt time.Time `json:"created_at"`
	Count     int       `json:"count"`
}
