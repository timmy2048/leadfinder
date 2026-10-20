// Package fetch is a polite shared HTTP client: per-host rate limiting,
// optional proxy rotation (http/https/socks5 list), retry with backoff,
// and best-effort robots.txt checks.
package fetch

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/temoto/robotstxt"
)

const BrowserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0 Safari/537.36"

// Options configure a Client.
type Options struct {
	UserAgent     string
	Timeout       time.Duration
	PerHostDelay  time.Duration // min gap between requests to one host
	Retries       int
	Proxies       []string // http://, https://, socks5:// URLs; empty = direct (env proxy)
	RespectRobots bool
	MaxBody       int64
}

func DefaultOptions() Options {
	return Options{UserAgent: BrowserUA, Timeout: 15 * time.Second, PerHostDelay: 700 * time.Millisecond, Retries: 2, RespectRobots: true, MaxBody: 4 << 20}
}

// Client is safe for concurrent use.
type Client struct {
	o       Options
	clients []*http.Client
	next    atomic.Uint64
	mu      sync.Mutex
	hosts   map[string]*hostState
	robots  map[string]*robotstxt.RobotsData
}

type hostState struct {
	mu   sync.Mutex
	last time.Time
}

// LoadProxies reads one proxy URL per line (# comments allowed).
func LoadProxies(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if !strings.Contains(l, "://") {
			l = "http://" + l
		}
		if _, err := url.Parse(l); err == nil {
			out = append(out, l)
		}
	}
	return out, sc.Err()
}

func New(o Options) *Client {
	if o.UserAgent == "" {
		o.UserAgent = BrowserUA
	}
	if o.Timeout == 0 {
		o.Timeout = 15 * time.Second
	}
	if o.MaxBody == 0 {
		o.MaxBody = 4 << 20
	}
	c := &Client{o: o, hosts: map[string]*hostState{}, robots: map[string]*robotstxt.RobotsData{}}
	mk := func(proxy func(*http.Request) (*url.URL, error)) *http.Client {
		return &http.Client{Timeout: o.Timeout, Transport: &http.Transport{Proxy: proxy, MaxIdleConnsPerHost: 4, ResponseHeaderTimeout: o.Timeout, TLSHandshakeTimeout: 10 * time.Second}}
	}
	for _, p := range o.Proxies {
		u, err := url.Parse(p)
		if err != nil {
			continue
		}
		c.clients = append(c.clients, mk(http.ProxyURL(u)))
	}
	if len(c.clients) == 0 {
		c.clients = append(c.clients, mk(http.ProxyFromEnvironment))
	}
	return c
}

func (c *Client) NumProxies() int { return len(c.o.Proxies) }

func (c *Client) pick() *http.Client {
	return c.clients[int(c.next.Add(1)-1)%len(c.clients)]
}

func (c *Client) wait(ctx context.Context, host string) error {
	c.mu.Lock()
	h := c.hosts[host]
	if h == nil {
		h = &hostState{}
		c.hosts[host] = h
	}
	c.mu.Unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	if d := c.o.PerHostDelay - time.Since(h.last); d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	h.last = time.Now()
	return nil
}

// ErrRobots means robots.txt disallows the URL.
var ErrRobots = errors.New("disallowed by robots.txt")

// Allowed checks robots.txt (fetched once per host; failures = allowed).
func (c *Client) Allowed(ctx context.Context, u *url.URL) bool {
	if !c.o.RespectRobots {
		return true
	}
	key := u.Scheme + "://" + u.Host
	c.mu.Lock()
	r, ok := c.robots[key]
	c.mu.Unlock()
	if !ok {
		body, st, _, err := c.raw(ctx, key+"/robots.txt", nil, 0)
		if err == nil {
			r, _ = robotstxt.FromStatusAndBytes(st, body)
		}
		c.mu.Lock()
		c.robots[key] = r
		c.mu.Unlock()
	}
	if r == nil {
		return true
	}
	return r.TestAgent(u.EscapedPath(), "leadfinder")
}

// Response of a GET.
type Response struct {
	Body     []byte
	Status   int
	FinalURL *url.URL
	Elapsed  time.Duration
}

// Get fetches a URL with rate limit, robots, retry/backoff (429/5xx/network).
func (c *Client) Get(ctx context.Context, raw string, hdr map[string]string) (*Response, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if !c.Allowed(ctx, u) {
		return nil, ErrRobots
	}
	var lastErr error
	for attempt := 0; attempt <= c.o.Retries; attempt++ {
		if attempt > 0 {
			back := time.Duration(1<<attempt)*time.Second + time.Duration(rand.Intn(500))*time.Millisecond
			select {
			case <-time.After(back):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		start := time.Now()
		body, st, fu, err := c.raw(ctx, raw, hdr, attempt)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return nil, err
			}
			continue
		}
		if st == 429 || st >= 500 {
			lastErr = fmt.Errorf("http %d", st)
			continue
		}
		return &Response{Body: body, Status: st, FinalURL: fu, Elapsed: time.Since(start)}, nil
	}
	return nil, lastErr
}

func (c *Client) raw(ctx context.Context, raw string, hdr map[string]string, attempt int) ([]byte, int, *url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, 0, nil, err
	}
	if err := c.wait(ctx, u.Host); err != nil {
		return nil, 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, 0, nil, err
	}
	req.Header.Set("User-Agent", c.o.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.pick().Do(req)
	if err != nil {
		return nil, 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, c.o.MaxBody))
	return b, resp.StatusCode, resp.Request.URL, err
}
