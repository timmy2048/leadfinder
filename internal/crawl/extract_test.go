package crawl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestDecodeCF(t *testing.T) {
	// key 0x42, "a@b.co"
	enc := "42"
	for _, c := range []byte("a@b.co") {
		enc += strings.ToLower(hexb(c ^ 0x42))
	}
	if got := DecodeCF(enc); got != "a@b.co" {
		t.Fatalf("got %q", got)
	}
}

func hexb(b byte) string { const h = "0123456789abcdef"; return string([]byte{h[b>>4], h[b&15]}) }

func TestExtractEmails(t *testing.T) {
	html := `<a href="mailto:Info@Shop.com?subject=hi">x</a> sales [at] shop [dot] com
	contact(at)shop(dot)de  logo@2x.png  abc@sentry.io  owner at shop dot com  user&#64;shop.org`
	got := ExtractEmails(html)
	want := map[string]bool{"info@shop.com": true, "sales@shop.com": true, "contact@shop.de": true, "owner@shop.com": true, "user@shop.org": true}
	for _, g := range got {
		if !want[g] {
			t.Errorf("unexpected %q", g)
		}
		delete(want, g)
	}
	if len(want) > 0 {
		t.Errorf("missing %v (got %v)", want, got)
	}
}

func TestRank(t *testing.T) {
	got := Rank([]string{"a@gmail.com", "b@shop.com"}, "shop.com")
	if !reflect.DeepEqual(got, []string{"b@shop.com", "a@gmail.com"}) {
		t.Fatal(got)
	}
}

func TestSocials(t *testing.T) {
	s := ExtractSocials(`<a href="https://www.facebook.com/tr">p</a><a href="https://www.instagram.com/shopx/">i</a><a href="https://facebook.com/sharer/sharer.php?u=1">s</a><a href="https://www.tiktok.com/@shopx">t</a>`)
	if len(s) != 2 {
		t.Fatal(s)
	}
}

func TestSiteCrawl(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head><title>Shop</title><meta name="description" content="x"></head><body><a href="/contact">Contact us</a><a href="/blog">Blog</a></body></html>`))
	})
	mux.HandleFunc("/contact", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<a href="/cdn-cgi/l/email-protection" data-cfemail="` + cf("hello@127.0.0.1x.com") + `">[email&#160;protected]</a> <a href="mailto:boss@other.net">b</a>`))
	})
	mux.HandleFunc("/blog", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`blog@nope.com`)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	res := Site(context.Background(), srv.URL, "", DefaultOptions())
	if !res.Reached || !res.HasTitle || !res.HasMetaDesc || res.HTTPS {
		t.Fatalf("signals %+v", res)
	}
	joined := strings.Join(res.Emails, ",")
	if !strings.Contains(joined, "boss@other.net") || !strings.Contains(joined, "hello@127.0.0.1x.com") || strings.Contains(joined, "blog@nope.com") {
		t.Fatalf("emails %v", res.Emails)
	}
}

func cf(s string) string {
	out := hexb(0x13)
	for _, c := range []byte(s) {
		out += hexb(c ^ 0x13)
	}
	return out
}
