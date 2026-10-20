package crawl

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gocolly/colly/v2"
)

// Result of crawling one website.
type Result struct {
	Emails      []string
	Socials     []string
	Reached     bool
	HTTPS       bool
	LoadMS      int64
	HasTitle    bool
	HasMetaDesc bool
	Err         string
}

// Options controls crawling.
type Options struct {
	MaxPages  int
	Timeout   time.Duration
	UserAgent string
}

func DefaultOptions() Options {
	return Options{MaxPages: 8, Timeout: 15 * time.Second, UserAgent: "Mozilla/5.0 (compatible; leadfinder/0.1; +https://github.com/timmy2048/leadfinder)"}
}

// Site crawls a homepage and contact-like pages (depth 2, same host).
func Site(ctx context.Context, site, domain string, o Options) Result {
	var res Result
	if !strings.Contains(site, "://") {
		site = "http://" + site
	}
	c := colly.NewCollector(colly.MaxDepth(2), colly.UserAgent(o.UserAgent), colly.Async(false))
	c.SetRequestTimeout(o.Timeout)
	c.WithTransport(&http.Transport{Proxy: http.ProxyFromEnvironment, ResponseHeaderTimeout: o.Timeout, MaxIdleConns: 4})
	c.IgnoreRobotsTxt = false
	if domain != "" {
		c.AllowedDomains = []string{domain, "www." + domain}
	}
	var mu sync.Mutex
	pages := 0
	emails := []string{}
	socials := []string{}
	start := map[string]time.Time{}
	first := true

	c.OnRequest(func(r *colly.Request) {
		mu.Lock()
		defer mu.Unlock()
		if ctx.Err() != nil || pages >= o.MaxPages {
			r.Abort()
			return
		}
		pages++
		start[r.URL.String()] = time.Now()
	})
	c.OnResponse(func(r *colly.Response) {
		mu.Lock()
		defer mu.Unlock()
		body := string(r.Body)
		if first {
			first = false
			res.Reached = true
			res.HTTPS = r.Request.URL.Scheme == "https"
			if t, ok := start[r.Request.URL.String()]; ok {
				res.LoadMS = time.Since(t).Milliseconds()
			}
			low := strings.ToLower(body)
			if i := strings.Index(low, "<title"); i >= 0 {
				if j := strings.Index(low[i:], "</title>"); j > 0 {
					inner := low[i:][:j]
					if k := strings.Index(inner, ">"); k >= 0 && strings.TrimSpace(inner[k+1:]) != "" {
						res.HasTitle = true
					}
				}
			}
			res.HasMetaDesc = strings.Contains(low, `name="description"`) || strings.Contains(low, `name='description'`) || strings.Contains(low, "name=description")
		}
		emails = append(emails, ExtractEmails(body)...)
		socials = append(socials, ExtractSocials(body)...)
	})
	c.OnHTML("a[href]", func(e *colly.HTMLElement) {
		href := e.Attr("href")
		if strings.HasPrefix(strings.ToLower(href), "mailto:") {
			addr := strings.SplitN(strings.TrimPrefix(strings.ToLower(href), "mailto:"), "?", 2)[0]
			mu.Lock()
			emails = append(emails, ExtractEmails(addr)...)
			mu.Unlock()
			return
		}
		if e.Request.Depth == 1 && IsContactLink(href, e.Text) {
			_ = e.Request.Visit(href)
		}
	})
	c.OnError(func(r *colly.Response, err error) {
		mu.Lock()
		defer mu.Unlock()
		if first && res.Err == "" {
			res.Err = err.Error()
		}
	})
	_ = c.Visit(site)
	c.Wait()
	res.Emails = Rank(dedup(emails), domain)
	res.Socials = dedup(socials)
	return res
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
