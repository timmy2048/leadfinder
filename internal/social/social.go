// Package social provides pluggable, OFF-by-default enrichment of social
// profile links (Instagram/Facebook/TikTok). It only performs anonymous GETs
// of public profile pages (no login, no cookies, no API abuse) and extracts
// emails visible in the returned HTML (bio / meta description). Platforms
// often return a login wall; in that case nothing is extracted. Respect each
// platform's Terms of Service.
package social

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/timmy2048/leadfinder/internal/crawl"
)

// Enricher extracts emails from a profile URL.
type Enricher interface {
	Name() string
	Match(url string) bool
	Emails(ctx context.Context, url string) ([]string, error)
}

// Registry of enrichers.
type Registry struct{ list []Enricher }

func (r *Registry) Register(e Enricher) { r.list = append(r.list, e) }

// Default returns a registry with public-page enrichers for the 3 platforms.
func Default() *Registry {
	r := &Registry{}
	hc := &http.Client{Timeout: 15 * time.Second}
	for _, p := range []struct{ name, host string }{{"instagram", "instagram.com"}, {"facebook", "facebook.com"}, {"tiktok", "tiktok.com"}} {
		r.Register(&PublicPage{N: p.name, Host: p.host, HTTP: hc})
	}
	return r
}

// Enrich runs matching enrichers over the given links; errors are ignored.
func (r *Registry) Enrich(ctx context.Context, links []string) []string {
	var out []string
	for _, l := range links {
		for _, e := range r.list {
			if e.Match(l) {
				if em, err := e.Emails(ctx, l); err == nil {
					out = append(out, em...)
				}
				time.Sleep(2 * time.Second) // be gentle
			}
		}
	}
	return out
}

// PublicPage fetches a public profile page anonymously.
type PublicPage struct {
	N, Host string
	HTTP    *http.Client
}

func (p *PublicPage) Name() string { return p.N }
func (p *PublicPage) Match(u string) bool {
	l := strings.ToLower(u)
	return strings.Contains(l, p.Host) || (p.Host == "facebook.com" && strings.Contains(l, "fb.com"))
}
func (p *PublicPage) Emails(ctx context.Context, u string) ([]string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; leadfinder/0.1)")
	resp, err := p.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 3<<20))
	return crawl.ExtractEmails(string(b)), nil
}
