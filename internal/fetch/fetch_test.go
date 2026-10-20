package fetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetryAndRateLimit(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.Write([]byte("User-agent: *\nDisallow: /private"))
			return
		}
		if n.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	c := New(Options{PerHostDelay: 50 * time.Millisecond, Retries: 2, RespectRobots: true})
	r, err := c.Get(context.Background(), srv.URL+"/x", nil)
	if err != nil || string(r.Body) != "ok" {
		t.Fatal(err)
	}
	if _, err := c.Get(context.Background(), srv.URL+"/private/a", nil); err != ErrRobots {
		t.Fatal("robots not respected", err)
	}
}

func TestLoadProxies(t *testing.T) {
	f, _ := os.CreateTemp("", "p")
	f.WriteString("# c\n1.2.3.4:8080\nsocks5://u:p@5.6.7.8:1080\n")
	f.Close()
	ps, err := LoadProxies(f.Name())
	if err != nil || len(ps) != 2 || ps[0] != "http://1.2.3.4:8080" {
		t.Fatal(ps, err)
	}
	if New(Options{Proxies: ps}).NumProxies() != 2 {
		t.Fatal()
	}
}
