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
	EmailStatus  map[string]string `json:"email_status"`  // email -> valid/risky/invalid/unknown
	SEOScore     int               `json:"seo_score"`     // legacy, mirrors GMBScore
	ScoreReasons []string          `json:"score_reasons"` // legacy, mirrors point labels

	// Google Maps profile signals (gosom); zero values when only OSM.
	Categories  []string `json:"categories,omitempty"`
	HasHours    bool     `json:"has_hours"`
	PhotoCount  int      `json:"photo_count"`
	PhotosKnown bool     `json:"photos_known"` // gosom often returns images=[] at low depth; only judge when known
	OwnerKnown  bool     `json:"owner_known"`  // profile shows an owner (claimed)
	Description string   `json:"description,omitempty"`
	MapsLink    string   `json:"maps_link,omitempty"`
	// Extra website checks
	HasViewport bool     `json:"has_viewport"`
	HasLBSchema bool     `json:"has_lb_schema"`
	HasMapEmbed bool     `json:"has_map_embed"`
	LinksToMaps bool     `json:"links_to_maps"`
	NAPIssues   []string `json:"nap_issues,omitempty"` // phone/address/name
	// GMB opportunity
	Reachable  bool              `json:"reachable"`
	BestEmail  string            `json:"best_email,omitempty"`
	SameDomain bool              `json:"same_domain_email"`
	EmailType  string            `json:"email_type,omitempty"` // personal / named / generic (of BestEmail)
	EmailTypes map[string]string `json:"email_types,omitempty"`
	GMBScore   int               `json:"gmb_score"`
	Points     []Point           `json:"points,omitempty"`
	NeedsGMaps bool              `json:"needs_gmaps"`
	// Google Maps local rank for the job keyword (reachable leads only).
	MapsRank    int    `json:"maps_rank"`     // 1..N; -1 = not in top RankDepth; 0 = not checked
	MapsRankN   int    `json:"maps_rank_n"`   // results inspected
	InLocalPack bool   `json:"in_local_pack"` // rank 1..3
	RankKeyword string `json:"rank_keyword,omitempty"`
	EnrichVer   int    `json:"enrich_ver"` // bump to force re-enrichment of cached leads // only OSM data: Maps-profile checks skipped
}

// Point is one explainable improvement opportunity.
type Point struct {
	Key     string `json:"key"`
	LabelZH string `json:"label_zh"`
	PitchEN string `json:"pitch_en"`
	Weight  int    `json:"weight"`
}

// HasSource reports whether the lead came from src.
func (b Business) HasSource(src string) bool {
	for _, s := range b.Sources {
		if s == src {
			return true
		}
	}
	return false
}

// JobParams are user inputs for a job.
type JobParams struct {
	Category    string `json:"category"`
	Location    string `json:"location"` // city / region
	Area        string `json:"area"`     // optional district / street (finer place)
	Country     string `json:"country"`
	Lang        string `json:"lang"`
	RadiusM     int    `json:"radius_m"`    // 0 = whole place bounding box
	Limit       int    `json:"limit"`       // per-cell hint
	MaxResults  int    `json:"max_results"` // pick best N reachable leads after full scan (0 = all)
	UseGMaps    bool   `json:"use_gmaps"`
	UseOSM      bool   `json:"use_osm"`
	Social      bool   `json:"social"`
	SMTPCheck   bool   `json:"smtp_check"`
	GMapsMaxSec int    `json:"gmaps_max_sec"`
	// Incremental coverage
	Incremental bool `json:"incremental"` // skip already-scanned cells (default true)
	CellSizeM   int  `json:"cell_size_m"` // grid cell size meters
	MaxCells    int  `json:"max_cells"`   // safety cap per source per job
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
