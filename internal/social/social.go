// Package social provides pluggable, OFF-by-default enrichment of public
// social pages (Facebook/Instagram/TikTok). Anonymous GETs only (no login,
// no cookies). Platforms often return a login wall; then nothing is found.
package social

import (
	"context"
	"net/url"
	"strings"

	"github.com/timmy2048/leadfinder/internal/crawl"
	"github.com/timmy2048/leadfinder/internal/fetch"
)

// Enricher extracts emails (email -> source label) from a profile URL.
type Enricher interface {
	Name() string
	Match(url string) bool
	Emails(ctx context.Context, f *fetch.Client, url string) map[string]string
}

// Registry of enrichers.
type Registry struct {
	list []Enricher
	F    *fetch.Client
}

func (r *Registry) Register(e Enricher) { r.list = append(r.list, e) }

// Default returns Facebook (m./mbasic about variants), Instagram and TikTok public-page enrichers.
func Default(f *fetch.Client) *Registry {
	r := &Registry{F: f}
	r.Register(facebook{})
	r.Register(&PublicPage{N: "instagram", Host: "instagram.com"})
	r.Register(&PublicPage{N: "tiktok", Host: "tiktok.com"})
	return r
}

// Enrich runs matching enrichers; one profile per platform.
func (r *Registry) Enrich(ctx context.Context, links []string) map[string]string {
	out := map[string]string{}
	done := map[string]bool{}
	for _, l := range links {
		for _, e := range r.list {
			if done[e.Name()] || !e.Match(l) {
				continue
			}
			done[e.Name()] = true
			for em, src := range e.Emails(ctx, r.F, l) {
				if _, ok := out[em]; !ok {
					out[em] = src
				}
			}
		}
	}
	return out
}

// PublicPage fetches a public profile page anonymously (bio/meta description).
type PublicPage struct{ N, Host string }

func (p *PublicPage) Name() string        { return p.N }
func (p *PublicPage) Match(u string) bool { return strings.Contains(strings.ToLower(u), p.Host) }
func (p *PublicPage) Emails(ctx context.Context, f *fetch.Client, u string) map[string]string {
	out := map[string]string{}
	r, err := f.Get(ctx, u, nil)
	if err != nil || r.Status >= 400 {
		return out
	}
	for _, e := range crawl.ExtractEmails(string(r.Body)) {
		out[e] = p.N
	}
	return out
}

type facebook struct{}

func (facebook) Name() string { return "facebook" }
func (facebook) Match(u string) bool {
	l := strings.ToLower(u)
	return strings.Contains(l, "facebook.com/") || strings.Contains(l, "fb.com/")
}

// FBVariants returns about/contact URLs on www, m. and mbasic hosts.
func FBVariants(raw string) []string {
	u, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	path := strings.TrimRight(u.Path, "/")
	if path == "" || strings.Contains(path, "/sharer") {
		return nil
	}
	q := ""
	if strings.HasPrefix(path, "/profile.php") {
		q = "?" + u.RawQuery
		return []string{"https://m.facebook.com" + path + q + "&sk=about", "https://mbasic.facebook.com" + path + q + "&v=info"}
	}
	return []string{"https://m.facebook.com" + path + "/about", "https://mbasic.facebook.com" + path + "/about", "https://www.facebook.com" + path + "/about_contact_and_basic_info"}
}

func (facebook) Emails(ctx context.Context, f *fetch.Client, u string) map[string]string {
	out := map[string]string{}
	for _, v := range FBVariants(u) {
		r, err := f.Get(ctx, v, nil)
		if err != nil || r.Status >= 400 {
			continue
		}
		for _, e := range crawl.ExtractEmails(string(r.Body)) {
			if !strings.HasSuffix(e, "facebook.com") && !strings.HasSuffix(e, "fb.com") {
				out[e] = "facebook"
			}
		}
		if len(out) > 0 {
			break
		}
	}
	return out
}
