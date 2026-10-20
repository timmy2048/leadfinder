// Package web serves a minimal UI and JSON API.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"github.com/timmy2048/leadfinder/internal/dedupe"
	"github.com/timmy2048/leadfinder/internal/score"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/timmy2048/leadfinder/internal/coverage"
	"github.com/timmy2048/leadfinder/internal/model"
	"github.com/timmy2048/leadfinder/internal/osm"
	"github.com/timmy2048/leadfinder/internal/pipeline"
	"github.com/timmy2048/leadfinder/internal/store"
)

//go:embed templates/*.html
var tplFS embed.FS

type Server struct {
	Store  *store.Store
	Runner *pipeline.Runner
	tpl    *template.Template
	queue  chan job
}

type job struct {
	id int64
	p  model.JobParams
}

func New(st *store.Store, r *pipeline.Runner) *Server {
	t := template.Must(template.New("").Funcs(template.FuncMap{"join": strings.Join}).ParseFS(tplFS, "templates/*.html"))
	s := &Server{Store: st, Runner: r, tpl: t, queue: make(chan job, 100)}
	go func() { // one job at a time to stay polite to public services
		for j := range s.queue {
			_, _ = r.Run(context.Background(), j.id, j.p)
		}
	}()
	return s
}

func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /{$}", s.index)
	m.HandleFunc("POST /jobs", s.create)
	m.HandleFunc("GET /jobs/{id}", s.view)
	m.HandleFunc("GET /jobs/{id}/csv", s.csv)
	m.HandleFunc("GET /api/jobs", s.apiJobs)
	m.HandleFunc("GET /api/jobs/{id}", s.apiJob)
	m.HandleFunc("GET /api/coverage", s.apiCoverage)
	return m
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	jobs, _ := s.Store.ListJobs()
	_ = s.tpl.ExecuteTemplate(w, "index.html", map[string]any{"Jobs": jobs})
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	atoi := func(k string) int { n, _ := strconv.Atoi(r.FormValue(k)); return n }
	on := func(k string) bool { return r.FormValue(k) != "" }
	radiusRaw := strings.TrimSpace(r.FormValue("radius"))
	radius := 0
	if radiusRaw != "" {
		radius = atoi("radius")
	}
	p := model.JobParams{
		Category: strings.TrimSpace(r.FormValue("category")), Location: strings.TrimSpace(r.FormValue("location")),
		Area: strings.TrimSpace(r.FormValue("area")), Country: r.FormValue("country"), Lang: r.FormValue("lang"),
		RadiusM: radius, Limit: atoi("limit"), MaxResults: atoi("max_results"),
		UseGMaps: on("gmaps"), UseOSM: on("osm"), Social: on("social"), SMTPCheck: on("smtp"), GMapsMaxSec: atoi("gmaps_max_sec"),
		Incremental: on("incremental"), CellSizeM: atoi("cell_size"), MaxCells: atoi("max_cells"),
	}
	if p.Category == "" || p.Location == "" {
		http.Error(w, "category and location required", 400)
		return
	}
	pipeline.Normalize(&p)
	id, err := s.Store.CreateJob(p)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.queue <- job{id, p}
	http.Redirect(w, r, fmt.Sprintf("/jobs/%d", id), http.StatusSeeOther)
}

func (s *Server) id(r *http.Request) int64 {
	n, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return n
}

func (s *Server) view(w http.ResponseWriter, r *http.Request) {
	j, err := s.Store.GetJob(s.id(r))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	bs, _ := s.Store.Businesses(j.ID)
	score.Rerank(bs, dedupe.Domain)
	reach, unreach := pipeline.Partition(bs)
	view := r.URL.Query().Get("view") // "" = reachable, "unreachable", "all"
	rows := reach
	switch view {
	case "unreachable":
		rows = unreach
	case "all":
		rows = bs
	}
	_ = s.tpl.ExecuteTemplate(w, "job.html", map[string]any{"Job": j, "Rows": rows, "View": view, "NReach": len(reach), "NUnreach": len(unreach), "Running": j.Status == "pending" || j.Status == "running"})
}

func (s *Server) csv(w http.ResponseWriter, r *http.Request) {
	bs, _ := s.Store.Businesses(s.id(r))
	score.Rerank(bs, dedupe.Domain)
	reach, unreach := pipeline.Partition(bs)
	name := "leads"
	switch r.URL.Query().Get("view") {
	case "unreachable":
		bs, name = unreach, "unreachable"
	case "all":
	default:
		bs = reach
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-job-%d.csv"`, name, s.id(r)))
	_ = store.WriteCSV(w, bs)
}

func (s *Server) apiJobs(w http.ResponseWriter, r *http.Request) {
	jobs, _ := s.Store.ListJobs()
	_ = json.NewEncoder(w).Encode(jobs)
}

func (s *Server) apiJob(w http.ResponseWriter, r *http.Request) {
	j, err := s.Store.GetJob(s.id(r))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	bs, _ := s.Store.Businesses(j.ID)
	_ = json.NewEncoder(w).Encode(map[string]any{"job": j, "businesses": bs})
}

// apiCoverage returns prior coverage summary for location+category (+ optional geocode).
func (s *Server) apiCoverage(w http.ResponseWriter, r *http.Request) {
	loc := strings.TrimSpace(r.URL.Query().Get("location"))
	cat := strings.TrimSpace(r.URL.Query().Get("category"))
	country := r.URL.Query().Get("country")
	if loc == "" || cat == "" {
		http.Error(w, "location and category required", 400)
		return
	}
	out := map[string]any{}
	for _, src := range []string{"osm", "gmaps"} {
		key := coverage.NormalizeKey(loc, cat, src)
		cells, _ := s.Store.ListCoverage(key)
		sum := coverage.Summary{Key: key, CellCount: len(cells)}
		for _, c := range cells {
			if c.ScannedAt.After(sum.LastScan) {
				sum.LastScan = c.ScannedAt
			}
		}
		out[src] = sum
	}
	leads, _ := s.Store.LoadLeads(coverage.LeadKey(loc, cat))
	out["leads"] = len(leads)
	if r.URL.Query().Get("geocode") == "1" {
		place, err := osm.New().GeocodePlace(r.Context(), loc, country)
		if err == nil {
			out["place"] = place
			for _, src := range []string{"osm", "gmaps"} {
				key := coverage.NormalizeKey(loc, cat, src)
				sum, _ := s.Store.CoverageSummary(key, place.Lat, place.Lon)
				out[src] = sum
			}
		}
	}
	_ = json.NewEncoder(w).Encode(out)
}
