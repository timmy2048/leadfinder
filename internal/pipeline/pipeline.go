// Package pipeline orchestrates a lead-finding job.
package pipeline

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

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

// Normalize applies defaults.
func Normalize(p *model.JobParams) {
	if p.Lang == "" {
		p.Lang = "en"
	}
	if p.RadiusM <= 0 {
		p.RadiusM = 5000
	}
	if p.Limit <= 0 {
		p.Limit = 20
	}
	if p.GMapsMaxSec <= 0 {
		p.GMapsMaxSec = 600
	}
	if !p.UseGMaps && !p.UseOSM {
		p.UseOSM = true
	}
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
	prog("geocoding " + p.Location)
	loc := p.Location
	lat, lon, err := r.OSM.Geocode(ctx, loc, p.Country)
	if err != nil {
		return fail(err)
	}
	var all []model.Business
	var errs []string
	if p.UseOSM {
		prog("querying OpenStreetMap (Overpass)")
		bs, err := r.OSM.Search(ctx, p.Category, lat, lon, p.RadiusM, p.Limit)
		if err != nil {
			errs = append(errs, "osm: "+err.Error())
		}
		all = append(all, bs...)
	}
	if p.UseGMaps {
		prog("querying Google Maps via gosom scraper (takes minutes)")
		gc := gmaps.New(r.GMapsURL)
		bs, err := gc.Scrape(ctx, gmaps.Request{
			Name: fmt.Sprintf("leadfinder-%d", jobID), Keywords: []string{p.Category + " in " + p.Location},
			Lang: p.Lang, Zoom: 14, Lat: strconv.FormatFloat(lat, 'f', 6, 64), Lon: strconv.FormatFloat(lon, 'f', 6, 64),
			Radius: p.RadiusM, Depth: 1, Email: false, MaxTime: p.GMapsMaxSec,
		}, prog)
		if err != nil {
			errs = append(errs, "gmaps: "+err.Error())
		}
		var kept []model.Business
		for _, b := range bs {
			if b.Lat == 0 || dedupe.DistanceM(lat, lon, b.Lat, b.Lon) <= float64(p.RadiusM)*1.5 {
				kept = append(kept, b)
			}
		}
		if len(kept) > p.Limit {
			kept = kept[:p.Limit]
		}
		all = append(all, kept...)
	}
	if len(all) == 0 && len(errs) > 0 {
		return fail(fmt.Errorf("%s", strings.Join(errs, "; ")))
	}
	all = dedupe.Merge(all)
	prog(fmt.Sprintf("found %d unique businesses; crawling websites", len(all)))

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
	for i := range all {
		wg.Add(1)
		sem <- struct{}{}
		go func(b *model.Business) {
			defer wg.Done()
			defer func() { <-sem }()
			Enrich(ctx, b, reg, ver)
			mu.Lock()
			done++
			if done%3 == 0 || done == len(all) {
				prog(fmt.Sprintf("enriched %d/%d", done, len(all)))
			}
			mu.Unlock()
		}(&all[i])
	}
	wg.Wait()
	if err := r.Store.SaveBusinesses(jobID, all); err != nil {
		return fail(err)
	}
	msg := fmt.Sprintf("done: %d businesses", len(all))
	_ = r.Store.UpdateJob(jobID, "done", msg, strings.Join(errs, "; "))
	return all, nil
}

// Enrich crawls the website, optional socials, verifies emails and scores.
func Enrich(ctx context.Context, b *model.Business, reg *social.Registry, ver *verify.Verifier) {
	if b.Website != "" {
		cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		res := crawl.Site(cctx, b.Website, dedupe.Domain(b.Website), crawl.DefaultOptions())
		cancel()
		b.SiteReached, b.HTTPS, b.LoadMS, b.HasTitle, b.HasMetaDesc = res.Reached, res.HTTPS, res.LoadMS, res.HasTitle, res.HasMetaDesc
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
	score.Compute(b)
}
