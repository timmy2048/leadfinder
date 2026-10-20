package crawl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/timmy2048/leadfinder/internal/fetch"
)

func TestSiteDeepCrawlSources(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`<html><title>x</title><body>Welcome <footer>Owner: Maria Lopez</footer></body></html>`))
	})
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<urlset><url><loc>/meet-the-team</loc></url></urlset>`))
	})
	mux.HandleFunc("/meet-the-team", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<span data-user="maria" data-domain="shop.test">x</span>`))
	})
	mux.HandleFunc("/contact", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<p>hello [at] shop [dot] test</p>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	o := fetch.DefaultOptions()
	o.PerHostDelay = time.Millisecond
	o.RespectRobots = false
	res := Site(context.Background(), srv.URL, "", Options{MaxPages: 10, Sitemap: true, Fetch: fetch.New(o)})
	if res.EmailSrc["hello@shop.test"] != "site:/contact" {
		t.Errorf("contact src: %v", res.EmailSrc)
	}
	if len(res.Names) == 0 || res.Names[0] != "maria lopez" {
		t.Errorf("names %v", res.Names)
	}
	if g := GuessEmails(res.Names, "shop.test"); len(g) != 2 || g[0] != "maria@shop.test" {
		t.Errorf("guess %v", g)
	}
}

func TestObfuscations(t *testing.T) {
	cases := map[string]string{
		`<script>var e = "info" + "@" + "cafe.com";</script>`:                          "info@cafe.com",
		`<a data-user="jo" data-domain="brew.co">mail</a>`:                             "jo@brew.co",
		`<span style="unicode-bidi:bidi-override; direction:rtl">moc.efac@nhoj</span>`: "john@cafe.com",
		`contact &#105;&#110;&#102;&#111;&#64;bar.com`:                                 "info@bar.com",
	}
	for in, want := range cases {
		got := ExtractEmails(in)
		ok := false
		for _, g := range got {
			ok = ok || g == want
		}
		if !ok {
			t.Errorf("%s -> %v want %s", in, got, want)
		}
	}
}
