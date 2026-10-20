package store

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/timmy2048/leadfinder/internal/coverage"
	"github.com/timmy2048/leadfinder/internal/model"
)

func TestStore(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := s.CreateJob(model.JobParams{Category: "c", Location: "l"})
	_ = s.SaveBusinesses(id, []model.Business{{Name: "A", SEOScore: 10, Emails: []string{"a@a.com"}, EmailStatus: map[string]string{"a@a.com": "valid"}}, {Name: "B", SEOScore: 50}})
	_ = s.UpdateJob(id, "done", "ok", "")
	j, _ := s.GetJob(id)
	bs, _ := s.Businesses(id)
	if j.Count != 2 || j.Params.Category != "c" || bs[0].Name != "A" { // insertion order (pipeline pre-sorts)
		t.Fatalf("%+v %+v", j, bs)
	}
	var buf bytes.Buffer
	_ = WriteCSV(&buf, bs)
	if strings.Contains(buf.String(), "valid") || strings.Contains(buf.String(), "sources") || !strings.Contains(buf.String(), "maps_rank") || !strings.Contains(buf.String(), "a@a.com") {
		t.Fatal(buf.String())
	}
}

func TestCoverageAndLeads(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	key := coverage.NormalizeKey("shanghai", "cafe", "osm")
	cells := []coverage.Cell{{Key: "k1", Lat: 31.2, Lon: 121.4, SizeM: 2500}, {Key: "k2", Lat: 31.21, Lon: 121.41, SizeM: 2500}}
	if err := s.MarkCellsDone(key, cells); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListCoverage(key)
	if err != nil || len(got) != 2 || got[0].Status != "done" {
		t.Fatalf("%v %+v", err, got)
	}
	lk := coverage.LeadKey("shanghai", "cafe")
	if err := s.UpsertLeads(lk, []model.Business{{Name: "A", Website: "https://a.com", Emails: []string{"a@a.com"}}, {Name: "A2", Website: "https://a.com"}}); err != nil {
		t.Fatal(err)
	}
	leads, _ := s.LoadLeads(lk)
	if len(leads) != 1 {
		t.Fatalf("dedupe leads: %d", len(leads))
	}
	_ = s.UpsertLeads(lk, []model.Business{{Name: "B", Phone: "+86 138 0000 0000"}})
	leads, _ = s.LoadLeads(lk)
	if len(leads) != 2 {
		t.Fatalf("got %d", len(leads))
	}
}
