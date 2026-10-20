// Package pipeline orchestrates a lead-finding job.
package pipeline

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/timmy2048/leadfinder/internal/coverage"
	"github.com/timmy2048/leadfinder/internal/crawl"
	"github.com/timmy2048/leadfinder/internal/dedupe"
	"github.com/timmy2048/leadfinder/internal/gmaps"
	"github.com/timmy2048/leadfinder/internal/model"
	"github.com/timmy2048/leadfinder/internal/osm"
	"github.com/timmy2048/leadfinder/internal/score"
	"github.com/timmy2048/leadfinder/internal/social"
	"github.com/timmy2048/leadfinder/internal/store"
	"github.com/timmy2048/leadfinder/internal/verify"
)

type Runner struct {
	Store       *store.Store
	OSM         *osm.Client
	GMapsURL    string
	Concurrency int
}

// PlaceQuery builds the Nominatim query from location + optional finer area.
func PlaceQuery(p model.JobParams) string {
	loc := strings.TrimSpace(p.Location)
	area := strings.TrimSpace(p.Area)
	if area == "" {
		return loc
	}
	// Prefer "area, location" so district/street resolves inside the city.
	if strings.Contains(strings.ToLower(area), strings.ToLower(loc)) {
		return area
	}
	return area + ", " + loc
}

// CoverageBaseKey uses city-level location (not area) so expanding radius
// and optional districts share one coverage ledger per city+category.
func CoverageBaseLocation(p model.JobParams) string {
	return strings.TrimSpace(p.Location)
}

// Normalize applies defaults. RadiusM 0 means whole-place bbox (kept as 0).
func Normalize(p *model.JobParams) {
	if p.Lang == "" {
		p.Lang = "en"
	}
	if p.Limit <= 0 {
		p.Limit = 20
	}
	if p.GMapsMaxSec <= 0 {
		p.GMapsMaxSec = 600
	}
	if p.CellSizeM <= 0 {
		p.CellSizeM = 2500
	}
	if p.MaxCells <= 0 {
		p.MaxCells = 40
	}
	if !p.UseGMaps && !p.UseOSM {
		p.UseOSM = true
	}
	// Incremental defaults to true when unset via JSON zero-value from old jobs;
	// callers that want full rescan must set Incremental=false explicitly.
}

// NormalizeWithIncrementalDefault sets Incremental=true unless the caller
// already chose (used by CLI/UI entry points before CreateJob).
func NormalizeWithIncrementalDefault(p *model.JobParams, incrementalFlag bool) {
	p.Incremental = incrementalFlag
	Normalize(p)
}

// PlanCells decides which cells to scan for a source.
func PlanCells(p model.JobParams, place osm.Place, source string, covered []coverage.Cell) (wanted, toScan []coverage.Cell, mode string) {
	size := p.CellSizeM
	if p.RadiusM > 0 {
		wanted = coverage.CellsForCircle(place.Lat, place.Lon, p.RadiusM, size)
		mode = fmt.Sprintf("circle %dm", p.RadiusM)
		if p.Incremental && len(covered) > 0 {
			oldR := coverage.ApproxMaxRadius(place.Lat, place.Lon, covered)
			if oldR > 0 && p.RadiusM > oldR {
				wanted = coverage.RingCells(place.Lat, place.Lon, oldR, p.RadiusM, size)
				mode = fmt.Sprintf("ring %d→%dm", oldR, p.RadiusM)
			}
		}
	} else if place.HasBBox {
		wanted = coverage.CellsForBBox(place.South, place.West, place.North, place.East, size)
		mode = "bbox full city"
	} else {
		// fallback: 15km circle if no bbox
		wanted = coverage.CellsForCircle(place.Lat, place.Lon, 15000, size)
		mode = "fallback 15km"
	}
	if p.Incremental {
		toScan = coverage.Diff(wanted, covered)
	} else {
		toScan = wanted
	}
	toScan = coverage.Cap(toScan, p.MaxCells)
	return wanted, toScan, mode
}

// Run executes the job synchronously, updating progress in the store.
func (r *Runner) Run(ctx context.Context, jobID int64, p model.JobParams) ([]model.Business, error) {
	Normalize(&p)
	prog := func(s string) {
		log.Printf("job %d: %s", jobID, s)
		_ = r.Store.UpdateJob(jobID, "running", s, "")
	}
	fail := func(err error) ([]model.Business, error) {
		_ = r.Store.UpdateJob(jobID, "failed", "", err.Error())
		return nil, err
	}

	query := PlaceQuery(p)
	prog("geocoding " + query)
	place, err := r.OSM.GeocodePlace(ctx, query, p.Country)
	if err != nil {
		return fail(err)
	}

	baseLoc := CoverageBaseLocation(p)
	leadKey := coverage.LeadKey(baseLoc, p.Category)

	var all []model.Business
	var errs []string

	// Reuse previously stored leads for this city+category.
	if p.Incremental {
		prev, err := r.Store.LoadLeads(leadKey)
		if err == nil && len(prev) > 0 {
			prog(fmt.Sprintf("loaded %d existing leads for %s", len(prev), leadKey))
			all = append(all, prev...)
		}
	}

	if p.UseOSM {
		bs, err := r.scanSource(ctx, p, place, "osm", prog)
		if err != nil {
			errs = append(errs, "osm: "+err.Error())
		}
		all = append(all, bs...)
	}
	if p.UseGMaps {
		bs, err := r.scanSource(ctx, p, place, "gmaps", prog)
		if err != nil {
			errs = append(errs, "gmaps: "+err.Error())
		}
		all = append(all, bs...)
	}

	if len(all) == 0 && len(errs) > 0 {
		return fail(fmt.Errorf("%s", strings.Join(errs, "; ")))
	}
	all = dedupe.Merge(all)

	// Enrich leads that are new or were enriched by an older version.
	// With MaxResults, stop once enough reachable leads are found.
	toEnrich := []*model.Business{}
	reachable := 0
	for i := range all {
		if all[i].EmailStatus == nil || all[i].EnrichVer < EnrichVersion {
			toEnrich = append(toEnrich, &all[i])
		} else {
			score.Compute(&all[i])
			if all[i].Reachable {
				reachable++
			}
		}
	}
	// Websites first: they are the only source of emails.
	sort.SliceStable(toEnrich, func(i, j int) bool { return toEnrich[i].Website != "" && toEnrich[j].Website == "" })
	prog(fmt.Sprintf("%d unique businesses; %d already reachable; enriching up to %d", len(all), reachable, len(toEnrich)))

	conc := r.Concurrency
	if conc <= 0 {
		conc = 4
	}
	var reg *social.Registry
	if p.Social {
		reg = social.Default()
	}
	ver := verify.New(p.SMTPCheck)
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	var mu sync.Mutex
	done := 0
	for _, b := range toEnrich {
		mu.Lock()
		enough := p.MaxResults > 0 && reachable >= p.MaxResults
		mu.Unlock()
		if enough {
			prog(fmt.Sprintf("reached max-results %d reachable; skipping remaining enrichment", p.MaxResults))
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(b *model.Business) {
			defer wg.Done()
			defer func() { <-sem }()
			Enrich(ctx, b, reg, ver)
			mu.Lock()
			if b.Reachable {
				reachable++
			}
			done++
			if done%3 == 0 || done == len(toEnrich) {
				prog(fmt.Sprintf("enriched %d/%d", done, len(toEnrich)))
			}
			mu.Unlock()
		}(b)
	}
	wg.Wait()

	// Cache everything (including not-yet-enriched) for future incremental runs.
	if err := r.Store.UpsertLeads(leadKey, all); err != nil {
		return fail(err)
	}
	out := SelectOutput(all, p.MaxResults)
	if err := r.Store.SaveBusinesses(jobID, out); err != nil {
		return fail(err)
	}
	nr := 0
	for _, b := range out {
		if b.Reachable {
			nr++
		}
	}
	msg := fmt.Sprintf("done: %d reachable (with email), %d unreachable, %d scanned total (incremental=%v)", nr, len(out)-nr, len(all), p.Incremental)
	all = out
	_ = r.Store.UpdateJob(jobID, "done", msg, strings.Join(errs, "; "))
	return all, nil
}

// EnrichVersion bumps when enrichment adds new signals (forces re-crawl of cached leads).
const EnrichVersion = 3

// SelectOutput sorts reachable-first by GMB score and caps reachable leads at
// maxResults (0 = no cap). Unreachable leads that were enriched are kept after
// them (also capped at maxResults) for the separate "暂不可触达" view.
func SelectOutput(all []model.Business, maxResults int) []model.Business {
	var reach, unreach []model.Business
	for _, b := range all {
		if b.EmailStatus == nil && !b.Reachable {
			continue // never enriched (skipped by max-results)
		}
		if b.Reachable {
			reach = append(reach, b)
		} else {
			unreach = append(unreach, b)
		}
	}
	less := func(s []model.Business) func(i, j int) bool {
		return func(i, j int) bool { return score.Less(s[i], s[j]) }
	}
	sort.SliceStable(reach, less(reach))
	sort.SliceStable(unreach, less(unreach))
	if maxResults > 0 {
		if len(reach) > maxResults {
			reach = reach[:maxResults]
		}
		if len(unreach) > maxResults {
			unreach = unreach[:maxResults]
		}
	}
	return append(reach, unreach...)
}

// Partition splits into reachable and unreachable, preserving order.
func Partition(bs []model.Business) (reach, unreach []model.Business) {
	for _, b := range bs {
		if b.Reachable {
			reach = append(reach, b)
		} else {
			unreach = append(unreach, b)
		}
	}
	return
}

func (r *Runner) scanSource(ctx context.Context, p model.JobParams, place osm.Place, source string, prog func(string)) ([]model.Business, error) {
	covKey := coverage.NormalizeKey(CoverageBaseLocation(p), p.Category, source)
	covered, _ := r.Store.ListCoverage(covKey)
	wanted, toScan, mode := PlanCells(p, place, source, covered)
	prog(fmt.Sprintf("%s: plan %s — %d cells wanted, %d to scan (skip %d)", source, mode, len(wanted), len(toScan), len(wanted)-len(toScan)))

	if len(toScan) == 0 {
		prog(source + ": nothing new to scan")
		return nil, nil
	}

	var all []model.Business
	var lastErr error
	perCellLimit := p.Limit
	if perCellLimit < 10 {
		perCellLimit = 10
	}
	// distribute limit across cells roughly
	perCellLimit = max(5, p.Limit*2/max(1, len(toScan))+5)

	for i, cell := range toScan {
		if err := ctx.Err(); err != nil {
			return all, err
		}
		// Global cap: enough raw candidates (≈4× target, since many lack email); remaining cells stay unscanned for later.
		if p.MaxResults > 0 && len(all) >= p.MaxResults*4 {
			prog(fmt.Sprintf("%s: %d candidates ≥ 4×max-results, stop scanning (%d cells left for next run)", source, len(all), len(toScan)-i))
			break
		}
		prog(fmt.Sprintf("%s: cell %d/%d (%.4f,%.4f)", source, i+1, len(toScan), cell.Lat, cell.Lon))
		var bs []model.Business
		var err error
		rad := cell.SizeM
		if rad < 800 {
			rad = 800
		}
		// slight overlap
		rad = int(float64(rad) * 0.75)
		if rad < 500 {
			rad = 500
		}
		switch source {
		case "osm":
			bs, err = r.OSM.Search(ctx, p.Category, cell.Lat, cell.Lon, rad, perCellLimit)
			// be polite to public Overpass
			time.Sleep(1100 * time.Millisecond)
		case "gmaps":
			gc := gmaps.New(r.GMapsURL)
			kw := p.Category + " in " + PlaceQuery(p)
			bs, err = gc.Scrape(ctx, gmaps.Request{
				Name: fmt.Sprintf("lf-%s-%s", source, cell.Key), Keywords: []string{kw},
				Lang: p.Lang, Zoom: zoomForRadius(rad), Lat: strconv.FormatFloat(cell.Lat, 'f', 6, 64), Lon: strconv.FormatFloat(cell.Lon, 'f', 6, 64),
				Radius: rad, Depth: 1, Email: false, MaxTime: p.GMapsMaxSec,
			}, prog)
			if err == nil {
				var kept []model.Business
				for _, b := range bs {
					if b.Lat == 0 || dedupe.DistanceM(cell.Lat, cell.Lon, b.Lat, b.Lon) <= float64(rad)*1.5 {
						kept = append(kept, b)
					}
				}
				bs = kept
			}
		}
		if err != nil {
			lastErr = err
			prog(fmt.Sprintf("%s: cell error: %v", source, err))
			continue
		}
		all = append(all, bs...)
		if markErr := r.Store.MarkCellsDone(covKey, []coverage.Cell{cell}); markErr != nil {
			prog("mark cell: " + markErr.Error())
		}
	}
	if len(all) == 0 && lastErr != nil {
		return nil, lastErr
	}
	return all, nil
}

func zoomForRadius(m int) int {
	switch {
	case m >= 10000:
		return 12
	case m >= 5000:
		return 13
	case m >= 2000:
		return 14
	default:
		return 15
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Enrich crawls the website, optional socials, verifies emails and scores.
func Enrich(ctx context.Context, b *model.Business, reg *social.Registry, ver *verify.Verifier) {
	if b.Website != "" {
		cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		res := crawl.Site(cctx, b.Website, dedupe.Domain(b.Website), crawl.DefaultOptions())
		cancel()
		b.SiteReached, b.HTTPS, b.LoadMS, b.HasTitle, b.HasMetaDesc = res.Reached, res.HTTPS, res.LoadMS, res.HasTitle, res.HasMetaDesc
		b.HasViewport, b.HasLBSchema, b.HasMapEmbed, b.LinksToMaps = res.HasViewport, res.HasSchema, res.HasMapEmbed, res.LinksToMaps
		b.NAPIssues = nil
		if res.Reached && b.HasSource("gmaps") {
			b.NAPIssues = crawl.NAPIssues(b.Name, b.Address, b.Phone, res.Text)
		}
		b.Emails = dedupe.Union(b.Emails, res.Emails)
		b.Socials = dedupe.Union(b.Socials, res.Socials)
	}
	if reg != nil && len(b.Socials) > 0 {
		b.Emails = dedupe.Union(b.Emails, reg.Enrich(ctx, b.Socials))
	}
	b.Emails = crawl.Rank(b.Emails, dedupe.Domain(b.Website))
	if len(b.Emails) > 5 {
		b.Emails = b.Emails[:5]
	}
	b.EmailStatus = map[string]string{}
	if ver != nil {
		for _, e := range b.Emails {
			b.EmailStatus[e] = ver.Check(e)
		}
	}
	score.Reachability(b, dedupe.Domain(b.Website))
	score.Compute(b)
	b.EnrichVer = EnrichVersion
}
