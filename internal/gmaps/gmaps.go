// Package gmaps drives a gosom/google-maps-scraper instance via its REST API
// (run it as the "gmaps" service in docker-compose, web mode).
package gmaps

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/timmy2048/leadfinder/internal/model"
)

type Client struct {
	BaseURL string
	HTTP    *http.Client
	Poll    time.Duration
}

func New(base string) *Client {
	return &Client{BaseURL: strings.TrimRight(base, "/"), HTTP: &http.Client{Timeout: 60 * time.Second}, Poll: 10 * time.Second}
}

type Request struct {
	Name     string   `json:"Name"`
	Keywords []string `json:"keywords"`
	Lang     string   `json:"lang"`
	Zoom     int      `json:"zoom"`
	Lat      string   `json:"lat,omitempty"`
	Lon      string   `json:"lon,omitempty"`
	Radius   int      `json:"radius,omitempty"`
	FastMode bool     `json:"fast_mode"`
	Depth    int      `json:"depth"`
	Email    bool     `json:"email"`
	MaxTime  int      `json:"max_time"` // seconds
}

// Scrape creates a job, waits for it, downloads CSV and parses it.
func (c *Client) Scrape(ctx context.Context, r Request, progress func(string)) ([]model.Business, error) {
	body, _ := json.Marshal(r)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/v1/jobs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gmaps service unreachable at %s: %w", c.BaseURL, err)
	}
	var created struct {
		ID string `json:"id"`
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 300 || json.Unmarshal(b, &created) != nil || created.ID == "" {
		return nil, fmt.Errorf("gmaps create job: %d %.200s", resp.StatusCode, b)
	}
	deadline := time.Now().Add(time.Duration(r.MaxTime)*time.Second + 5*time.Minute)
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(c.Poll):
		}
		st, err := c.status(ctx, created.ID)
		if err != nil {
			return nil, err
		}
		if progress != nil {
			progress("google maps job " + st)
		}
		if st == "ok" {
			break
		}
		if st == "failed" {
			return nil, fmt.Errorf("gmaps job failed")
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("gmaps job timeout")
		}
	}
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/v1/jobs/"+created.ID+"/download", nil)
	resp, err = c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return ParseCSV(resp.Body)
}

func (c *Client) status(ctx context.Context, id string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/v1/jobs/"+id, nil)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var j struct{ Status string }
	if err := json.NewDecoder(resp.Body).Decode(&j); err != nil {
		return "", err
	}
	return j.Status, nil
}

// ParseCSV parses gosom CSV output by header name.
func ParseCSV(r io.Reader) ([]model.Business, error) {
	cr := csv.NewReader(r)
	cr.LazyQuotes = true
	cr.FieldsPerRecord = -1
	rows, err := cr.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	idx := map[string]int{}
	for i, h := range rows[0] {
		idx[strings.TrimSpace(strings.TrimPrefix(h, "\ufeff"))] = i
	}
	get := func(row []string, k string) string {
		if i, ok := idx[k]; ok && i < len(row) {
			return strings.TrimSpace(row[i])
		}
		return ""
	}
	var out []model.Business
	for _, row := range rows[1:] {
		b := model.Business{Name: get(row, "title"), Category: get(row, "category"), Address: get(row, "address"),
			Phone: get(row, "phone"), Website: get(row, "website"), Sources: []string{"gmaps"}}
		b.Lat, _ = strconv.ParseFloat(get(row, "latitude"), 64)
		b.Lon, _ = strconv.ParseFloat(firstNE(get(row, "longitude"), get(row, "longtitude")), 64)
		b.ReviewCount, _ = strconv.Atoi(get(row, "review_count"))
		b.Rating, _ = strconv.ParseFloat(get(row, "review_rating"), 64)
		for _, e := range strings.FieldsFunc(get(row, "emails"), func(r rune) bool { return r == ',' || r == ';' || r == ' ' }) {
			b.Emails = append(b.Emails, strings.ToLower(e))
		}
		if b.Name != "" {
			out = append(out, b)
		}
	}
	return out, nil
}

func firstNE(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
