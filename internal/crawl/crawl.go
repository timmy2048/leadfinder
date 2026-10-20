package crawl

import (
	"bytes"
	"context"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/timmy2048/leadfinder/internal/fetch"
	"github.com/timmy2048/leadfinder/internal/jsrender"
)

// Result of crawling one website.
type Result struct {
	Emails      []string
	EmailSrc    map[string]string // email -> source label (site:/contact, sitemap:/team, js:/ ...)
	Socials     []string
	Names       []string // person names near owner/founder/chef words (for pattern guess)
	Reached     bool
	HTTPS       bool
	LoadMS      int64
	HasTitle    bool
	HasMetaDesc bool
	HasViewport bool
	HasSchema   bool
	HasMapEmbed bool
	LinksToMaps bool
	Pages       int
	Text        string
	Err         string
}

// Options controls crawling.
type Options struct {
	MaxPages int
	Sitemap  bool
	JS       bool // headless-Chrome fallback when no email found
	Fetch    *fetch.Client
}

func DefaultOptions() Options { return Options{MaxPages: 12, Sitemap: true} }

// CommonPaths are probed even when not linked from the homepage.
var CommonPaths = []string{"/contact", "/contact-us", "/about", "/about-us", "/team", "/our-team", "/privacy-policy", "/privacy", "/impressum", "/legal"}

var locRe = regexp.MustCompile(`<loc>\s*([^<\s]+)\s*</loc>`)

// Site crawls homepage, contact-like links (incl. footer), common paths and
// sitemap contact URLs on the same host, up to MaxPages pages.
func Site(ctx context.Context, site, domain string, o Options) Result {
	res := Result{EmailSrc: map[string]string{}}
	if o.Fetch == nil {
		o.Fetch = fetch.New(fetch.DefaultOptions())
	}
	if o.MaxPages <= 0 {
		o.MaxPages = 12
	}
	if !strings.Contains(site, "://") {
		site = "http://" + site
	}
	var socials []string
	add := func(body, src string) {
		for _, e := range ExtractEmails(body) {
			if _, ok := res.EmailSrc[e]; !ok {
				res.EmailSrc[e] = src
				res.Emails = append(res.Emails, e)
			}
		}
		socials = append(socials, ExtractSocials(body)...)
		res.Names = append(res.Names, ExtractNames(body)...)
		sig := Signals(body)
		res.HasViewport = res.HasViewport || sig.Viewport
		res.HasSchema = res.HasSchema || sig.Schema
		res.HasMapEmbed = res.HasMapEmbed || sig.MapEmbed
		res.LinksToMaps = res.LinksToMaps || sig.MapsLink
		if len(res.Text) < 400000 {
			res.Text += "\n" + strings.ToLower(body)
		}
	}
	home, err := o.Fetch.Get(ctx, site, nil)
	if err != nil || home.Status >= 400 {
		if err != nil {
			res.Err = err.Error()
		} else {
			res.Err = "http " + strconv.Itoa(home.Status)
		}
		return res
	}
	res.Reached, res.Pages = true, 1
	base := home.FinalURL
	res.HTTPS = base.Scheme == "https"
	res.LoadMS = home.Elapsed.Milliseconds()
	low := strings.ToLower(string(home.Body))
	if i := strings.Index(low, "<title"); i >= 0 {
		if j := strings.Index(low[i:], "</title>"); j > 0 {
			in := low[i:][:j]
			if k := strings.Index(in, ">"); k >= 0 && strings.TrimSpace(in[k+1:]) != "" {
				res.HasTitle = true
			}
		}
	}
	res.HasMetaDesc = strings.Contains(low, `name="description"`) || strings.Contains(low, `name='description'`) || strings.Contains(low, "name=description")
	add(string(home.Body), "site:/")

	sameHost := func(u *url.URL) bool {
		h := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
		bh := strings.TrimPrefix(strings.ToLower(base.Hostname()), "www.")
		return h == bh || (domain != "" && (h == domain || strings.HasSuffix(h, "."+domain)))
	}
	seen := map[string]bool{norm(base): true}
	type item struct{ u, src string }
	var queue []item
	push := func(raw, src string) {
		u, err := base.Parse(strings.TrimSpace(raw))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || !sameHost(u) {
			return
		}
		u.Fragment = ""
		if k := norm(u); !seen[k] {
			seen[k] = true
			queue = append(queue, item{u.String(), src})
		}
	}
	links := contactLinks(home.Body)
	for _, l := range links {
		push(l, "site")
	}
	if o.Sitemap && ctx.Err() == nil {
		for _, l := range sitemapContacts(ctx, o.Fetch, base) {
			push(l, "sitemap")
		}
	}
	for _, p := range CommonPaths {
		push(p, "site")
	}
	for _, it := range queue {
		if res.Pages >= o.MaxPages || ctx.Err() != nil {
			break
		}
		r, err := o.Fetch.Get(ctx, it.u, nil)
		if err != nil || r.Status >= 400 {
			continue
		}
		res.Pages++
		pu, _ := url.Parse(it.u)
		add(string(r.Body), it.src+":"+pu.Path)
		if len(res.Emails) >= 3 && res.Pages >= 6 {
			break
		}
	}
	if o.JS && len(res.Emails) == 0 && ctx.Err() == nil && LooksSPA(home.Body) {
		targets := []string{base.String()}
		for _, l := range links {
			if u, err := base.Parse(l); err == nil && sameHost(u) {
				targets = append(targets, u.String())
				break
			}
		}
		for _, t := range targets {
			if h, err := jsrender.Render(ctx, t); err == nil {
				pu, _ := url.Parse(t)
				add(h, "js:"+pu.Path)
			}
		}
	}
	res.Emails = Rank(res.Emails, domain)
	res.Socials = dedup(socials)
	res.Names = dedup(res.Names)
	return res
}

func norm(u *url.URL) string {
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.") + strings.TrimRight(strings.ToLower(u.Path), "/")
}

func contactLinks(body []byte) []string {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil
	}
	var out []string
	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		h, _ := s.Attr("href")
		lh := strings.ToLower(h)
		if strings.HasPrefix(lh, "mailto:") || strings.HasPrefix(lh, "tel:") {
			return
		}
		if IsContactLink(h, s.Text()) {
			out = append(out, h)
		}
	})
	return out
}

func sitemapContacts(ctx context.Context, f *fetch.Client, base *url.URL) []string {
	sm, _ := base.Parse("/sitemap.xml")
	r, err := f.Get(ctx, sm.String(), nil)
	if err != nil || r.Status >= 400 {
		return nil
	}
	var out, subs []string
	for _, m := range locRe.FindAllStringSubmatch(string(r.Body), 500) {
		l := m[1]
		if strings.HasSuffix(strings.ToLower(l), ".xml") {
			if len(subs) < 2 && (strings.Contains(l, "page") || len(subs) == 0) {
				subs = append(subs, l)
			}
			continue
		}
		if IsContactLink(l, "") {
			out = append(out, l)
		}
	}
	for _, s := range subs {
		r, err := f.Get(ctx, s, nil)
		if err != nil || r.Status >= 400 {
			continue
		}
		for _, m := range locRe.FindAllStringSubmatch(string(r.Body), 1000) {
			if IsContactLink(m[1], "") {
				out = append(out, m[1])
			}
		}
	}
	if len(out) > 6 {
		out = out[:6]
	}
	return out
}

// LooksSPA: little visible text or typical SPA roots / site builders.
func LooksSPA(body []byte) bool {
	l := strings.ToLower(string(body))
	for _, k := range []string{`id="root"`, `id="app"`, `id="__next"`, "__nuxt", "wix.com", "squarespace", "ng-version", "data-reactroot", "webflow", "toasttab", "popmenu", "bentobox", "spothopper"} {
		if strings.Contains(l, k) {
			return true
		}
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return false
	}
	doc.Find("script,style,noscript").Remove()
	return len(strings.Fields(doc.Text())) < 150
}

var (
	nameRe  = regexp.MustCompile(`(?i:owner|co-owner|founder|co-founder|chef|proprietor|ceo|general manager|president)\s*(?:[:,\-–—]|is)?\s*([A-Z][a-z]{2,15})\s+([A-Z][a-z]{2,20})`)
	nameRe2 = regexp.MustCompile(`([A-Z][a-z]{2,15})\s+([A-Z][a-z]{2,20})\s*[,\-–—|]\s*(?:Owner|Co-Owner|Founder|Co-Founder|Chef|Proprietor|CEO|General Manager)`)
)

// ExtractNames finds "Owner: John Smith" / "John Smith, Founder"; returns "first last" lowercase.
func ExtractNames(raw string) []string {
	text := raw
	if doc, err := goquery.NewDocumentFromReader(strings.NewReader(raw)); err == nil {
		doc.Find("script,style").Remove()
		text = doc.Text()
	}
	text = strings.Join(strings.Fields(text), " ")
	var out []string
	for _, re := range []*regexp.Regexp{nameRe, nameRe2} {
		for _, m := range re.FindAllStringSubmatch(text, 5) {
			f, l := strings.ToLower(m[1]), strings.ToLower(m[2])
			if stopName[f] || stopName[l] {
				continue
			}
			out = append(out, f+" "+l)
		}
	}
	return out
}

var stopName = map[string]bool{"the": true, "and": true, "our": true, "with": true, "for": true, "his": true, "her": true, "coffee": true, "cafe": true, "restaurant": true, "kitchen": true, "team": true, "about": true, "from": true, "this": true, "that": true, "has": true, "was": true, "who": true}

// GuessEmails builds first@domain / first.last@domain from names (lowest priority, label "guessed").
func GuessEmails(names []string, domain string) []string {
	if domain == "" {
		return nil
	}
	var out []string
	for _, n := range names {
		p := strings.Fields(n)
		if len(p) != 2 {
			continue
		}
		out = append(out, p[0]+"@"+domain, p[0]+"."+p[1]+"@"+domain)
	}
	return dedup(out)
}

func dedup(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		k := strings.ToLower(s)
		if !seen[k] {
			seen[k] = true
			out = append(out, s)
		}
	}
	return out
}
