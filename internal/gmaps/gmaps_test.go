package gmaps

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const sampleCSV = "input_id,link,title,category,address,website,phone,review_count,review_rating,latitude,longitude,emails\n1,l,Pizza Co,Restaurant,1 Main St,https://pizza.co,+1 555,7,4.5,51.1,-2.2,info@pizza.co\n"

func TestParseCSV(t *testing.T) {
	bs, err := ParseCSV(strings.NewReader(sampleCSV))
	if err != nil || len(bs) != 1 || bs[0].ReviewCount != 7 || bs[0].Lon != -2.2 || bs[0].Emails[0] != "info@pizza.co" {
		t.Fatalf("%v %+v", err, bs)
	}
}

func TestScrapeFlow(t *testing.T) {
	polls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["lang"] != "en" {
			w.WriteHeader(422)
			return
		}
		w.WriteHeader(201)
		w.Write([]byte(`{"id":"abc"}`))
	})
	mux.HandleFunc("GET /api/v1/jobs/abc", func(w http.ResponseWriter, r *http.Request) {
		polls++
		st := "working"
		if polls > 1 {
			st = "ok"
		}
		w.Write([]byte(`{"Status":"` + st + `"}`))
	})
	mux.HandleFunc("GET /api/v1/jobs/abc/download", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(sampleCSV)) })
	s := httptest.NewServer(mux)
	defer s.Close()
	c := New(s.URL)
	c.Poll = 10 * time.Millisecond
	bs, err := c.Scrape(context.Background(), Request{Keywords: []string{"pizza"}, Lang: "en", MaxTime: 1}, nil)
	if err != nil || len(bs) != 1 {
		t.Fatal(err, bs)
	}
}
