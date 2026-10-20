// Package directory adds pluggable, best-effort FREE business directory
// sources (Yelp, Yellow Pages, TripAdvisor, Booking.com, OpenTable, Chamber
// of Commerce member directories on ChamberMaster/GrowthZone/Weblink).
// Goal: discover businesses + website/phone; emails come from crawling the
// website afterwards. Public pages only, no login. Most of these sites run
// bot protection (403/captcha) for datacenter IPs; a failure just yields 0
// results. Their ToS generally forbid scraping — use at your own risk.
package directory

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/timmy2048/leadfinder/internal/fetch"
	"github.com/timmy2048/leadfinder/internal/model"
)

// Query for a directory search.
type Query struct {
	Category   string
	Location   string // "Downtown Tampa, FL"
	City       string // "Tampa"
	Limit      int    // max businesses (detail fetches) per source
	ChamberURL string // optional known chamber directory base URL
}

// Source is a pluggable directory.
type Source interface {
	Name() string
	Search(ctx context.Context, f *fetch.Client, q Query) ([]model.Business, error)
}

// All lists available sources by name.
var All = map[string]Source{
	"yelp":        detailSource{name: "yelp", search: "https://www.yelp.com/search?find_desc={q}&find_loc={loc}", detail: regexp.MustCompile(`/biz/[a-z0-9\-]+`), host: "https://www.yelp.com"},
	"yellowpages": ypSource{},
	"tripadvisor": detailSource{name: "tripadvisor", search: "https://www.tripadvisor.com/Search?q={q}+{loc}", detail: regexp.MustCompile(`/(?:Restaurant_Review|Attraction_Review|Hotel_Review)-g\d+-d\d+-Reviews-[A-Za-z0-9_\-]+\.html`), host: "https://www.tripadvisor.com"},
	"booking":     detailSource{name: "booking", search: "https://www.booking.com/searchresults.html?ss={loc}", detail: regexp.MustCompile(`/hotel/[a-z]{2}/[a-z0-9\-]+\.html`), host: "https://www.booking.com"},
	"opentable":   detailSource{name: "opentable", search: "https://www.opentable.com/s?term={q}+{loc}", detail: regexp.MustCompile(`/r/[a-z0-9\-]+`), host: "https://www.opentable.com"},
	"chamber":     chamberSource{},
}

// Names in stable order.
var Names = []string{"yelp", "yellowpages", "tripadvisor", "booking", "opentable", "chamber"}

// ErrBlocked signals bot protection.
var ErrBlocked = errors.New("blocked (403/captcha)")

func fill(t string, q Query) string {
	return strings.NewReplacer("{q}", url.QueryEscape(q.Category), "{loc}", url.QueryEscape(q.Location)).Replace(t)
}

func get(ctx context.Context, f *fetch.Client, u string) (*goquery.Document, string, error) {
	r, err := f.Get(ctx, u, nil)
	if err != nil {
		return nil, "", err
	}
	body := string(r.Body)
	if r.Status == 403 || r.Status == 202 || r.Status == 429 || strings.Contains(body, "captcha") && len(body) < 30000 {
		return nil, body, fmt.Errorf("%w: http %d", ErrBlocked, r.Status)
	}
	if r.Status >= 400 {
		return nil, body, fmt.Errorf("http %d", r.Status)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	return doc, body, err
}

var telRe = regexp.MustCompile(`\(?\d{3}\)?[\s.\-]\d{3}[\s.\-]\d{4}`)

// detailSource: search page -> detail links -> parse each detail page.
type detailSource struct {
	name, search, host string
	detail             *regexp.Regexp
}

func (d detailSource) Name() string { return d.name }
func (d detailSource) Search(ctx context.Context, f *fetch.Client, q Query) ([]model.Business, error) {
	_, body, err := get(ctx, f, fill(d.search, q))
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []model.Business
	for _, p := range d.detail.FindAllString(body, -1) {
		if seen[p] || len(out) >= max(q.Limit, 1) {
			continue
		}
		seen[p] = true
		doc, raw, err := get(ctx, f, d.host+p)
		if err != nil {
			if errors.Is(err, ErrBlocked) {
				return out, err
			}
			continue
		}
		b := ParseDetail(doc, raw, d.host)
		if b.Name == "" {
			continue
		}
		b.Sources = []string{d.name}
		b.Category = q.Category
		out = append(out, b)
	}
	return out, nil
}

var redirParam = regexp.MustCompile(`(?:biz_redir\?url=|[?&](?:url|u|website|to)=)(https?%3A%2F%2F[^&"']+)`)

// ParseDetail extracts name/phone/address/website from a business detail page.
func ParseDetail(doc *goquery.Document, raw, ownHost string) model.Business {
	var b model.Business
	b.Name = strings.TrimSpace(doc.Find("h1").First().Text())
	if b.Name == "" {
		b.Name, _ = doc.Find(`meta[property="og:title"]`).Attr("content")
	}
	if t, ok := doc.Find(`a[href^="tel:"]`).First().Attr("href"); ok {
		b.Phone = strings.TrimPrefix(t, "tel:")
	} else if m := telRe.FindString(doc.Text()); m != "" {
		b.Phone = m
	}
	b.Address = strings.TrimSpace(doc.Find("address").First().Text())
	if m := redirParam.FindStringSubmatch(raw); m != nil {
		if u, err := url.QueryUnescape(m[1]); err == nil {
			b.Website = u
		}
	}
	if b.Website == "" {
		b.Website = ExternalWebsite(doc, ownHost)
	}
	return b
}

var skipHosts = []string{"facebook.", "instagram.", "twitter.", "x.com", "tiktok.", "youtube.", "google.", "apple.com", "yelp.", "tripadvisor.", "booking.", "opentable.", "yellowpages.", "linkedin.", "pinterest.", "doordash.", "ubereats.", "grubhub.", "chambermaster", "growthzone", "weblink", "tacdn", "bing.", "maps."}

// ExternalWebsite picks the first plausible outbound business website link.
func ExternalWebsite(doc *goquery.Document, ownHost string) string {
	own, _ := url.Parse(ownHost)
	best := ""
	doc.Find("a[href]").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		h, _ := s.Attr("href")
		u, err := url.Parse(h)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return true
		}
		host := strings.ToLower(u.Host)
		if own != nil && strings.HasSuffix(host, strings.TrimPrefix(own.Host, "www.")) {
			return true
		}
		for _, sk := range skipHosts {
			if strings.Contains(host, sk) {
				return true
			}
		}
		txt := strings.ToLower(s.Text())
		if strings.Contains(txt, "website") || strings.Contains(txt, "visit") || best == "" {
			best = u.Scheme + "://" + u.Host
			if strings.Contains(txt, "website") {
				return false
			}
		}
		return true
	})
	return best
}

// ypSource parses Yellow Pages result cards directly (website link on card).
type ypSource struct{}

func (ypSource) Name() string { return "yellowpages" }
func (ypSource) Search(ctx context.Context, f *fetch.Client, q Query) ([]model.Business, error) {
	var out []model.Business
	for page := 1; page <= 2; page++ {
		u := fill("https://www.yellowpages.com/search?search_terms={q}&geo_location_terms={loc}", q) + fmt.Sprintf("&page=%d", page)
		doc, _, err := get(ctx, f, u)
		if err != nil {
			return out, err
		}
		out = append(out, ParseYP(doc, q.Category)...)
	}
	return out, nil
}

// ParseYP parses yellowpages.com result cards.
func ParseYP(doc *goquery.Document, cat string) []model.Business {
	var out []model.Business
	doc.Find(".result .info, .srp-listing .info").Each(func(_ int, s *goquery.Selection) {
		b := model.Business{Category: cat, Sources: []string{"yellowpages"}}
		b.Name = strings.TrimSpace(s.Find("a.business-name").Text())
		b.Phone = strings.TrimSpace(s.Find(".phones").First().Text())
		b.Website, _ = s.Find("a.track-visit-website").Attr("href")
		b.Address = strings.TrimSpace(s.Find(".street-address").Text() + ", " + s.Find(".locality").Text())
		if b.Name != "" {
			out = append(out, b)
		}
	})
	return out
}

// chamberSource discovers a Chamber of Commerce member directory (via
// ChamberURL or a DuckDuckGo/Bing HTML search) and parses common platforms.
type chamberSource struct{}

func (chamberSource) Name() string { return "chamber" }

var chamberHost = regexp.MustCompile(`https?://[a-z0-9.\-]*(?:chamber[a-z0-9.\-]*|growthzoneapp\.com|chambermaster\.com|weblinkconnect\.com)[^"'&<> ]*`)

func (c chamberSource) Search(ctx context.Context, f *fetch.Client, q Query) ([]model.Business, error) {
	bases := []string{}
	if q.ChamberURL != "" {
		bases = append(bases, q.ChamberURL)
	} else {
		sq := url.QueryEscape(q.City + " chamber of commerce member directory")
		for _, s := range []string{"https://html.duckduckgo.com/html/?q=" + sq, "https://www.bing.com/search?q=" + sq} {
			_, body, err := get(ctx, f, s)
			if err != nil {
				continue
			}
			body, _ = url.QueryUnescape(body)
			for _, m := range chamberHost.FindAllString(body, 20) {
				if u, err := url.Parse(m); err == nil && !strings.Contains(u.Host, "duckduckgo") && !strings.Contains(u.Host, "bing") {
					bases = append(bases, u.Scheme+"://"+u.Host)
				}
			}
			if len(bases) > 0 {
				break
			}
		}
	}
	if len(bases) == 0 {
		return nil, errors.New("no chamber directory found (set chamber URL)")
	}
	var out []model.Business
	var lastErr error
	tried := map[string]bool{}
	for _, b := range bases {
		if tried[b] || len(tried) >= 3 {
			continue
		}
		tried[b] = true
		for _, path := range []string{"/list/search?q={q}&st=0", "/search?q={q}", "/list/searchalpha/a", "/list"} {
			u := strings.TrimRight(b, "/") + fill(path, q)
			doc, _, err := get(ctx, f, u)
			if err != nil {
				lastErr = err
				continue
			}
			got := ParseChamber(doc, q.Category)
			if len(got) > 0 {
				out = append(out, got...)
				break
			}
		}
	}
	if len(out) == 0 {
		return nil, lastErr
	}
	return out, nil
}

// ParseChamber parses ChamberMaster/GrowthZone (gz-*/mn-*) and Weblink style member cards.
func ParseChamber(doc *goquery.Document, cat string) []model.Business {
	var out []model.Business
	doc.Find(".gz-list-card-wrapper, .mn-listing, .card.gz-directory-card, .ListingResults_All_CONTAINER, .member-listing").Each(func(_ int, s *goquery.Selection) {
		b := model.Business{Category: cat, Sources: []string{"chamber"}}
		b.Name = strings.TrimSpace(s.Find(".gz-card-title, .mn-title, h5, h3, .ListingResults_All_ENTRYTITLELEFTBOX").First().Text())
		b.Phone = strings.TrimSpace(s.Find(".gz-card-phone, .mn-phone, [itemprop=telephone]").First().Text())
		if b.Phone == "" {
			b.Phone = telRe.FindString(s.Text())
		}
		b.Website, _ = s.Find(".gz-card-website a, .mn-website a, a[itemprop=url], a:contains('Visit Website')").First().Attr("href")
		b.Address = strings.Join(strings.Fields(s.Find(".gz-card-address, .mn-address, [itemprop=address]").First().Text()), " ")
		if b.Name != "" {
			out = append(out, b)
		}
	})
	return out
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
