package pipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/timmy2048/leadfinder/internal/model"
)

func TestAssignRanks(t *testing.T) {
	leads := []model.Business{
		{Name: "Kahwa Coffee", Phone: "(813) 228-7900", Reachable: true, Sources: []string{"gmaps"}, Lat: 27.95, Lon: -82.457, Emails: []string{"a@b.com"}},
		{Name: "Hidden Cafe", Reachable: true, Sources: []string{"gmaps"}, Lat: 28.2, Lon: -82.9},
		{Name: "No Email", Reachable: false},
	}
	calls := 0
	s := func(ctx context.Context, kw string, lat, lon float64) ([]model.Business, error) {
		calls++
		if lat != 27.95 || lon != -82.45 {
			t.Fatalf("search not at job center: %v,%v", lat, lon)
		}
		return []model.Business{{Name: "A"}, {Name: "B"}, {Name: "C"}, {Name: "x", Phone: "+1813-228-7900"}}, nil
	}
	AssignRanks(context.Background(), leads, "cafe", 2500, 27.95, -82.45, s, nil)
	if calls != 1 {
		t.Fatalf("want 1 center search, got %d", calls)
	}
	if leads[0].MapsRank != 4 || leads[0].InLocalPack {
		t.Fatalf("rank %+v", leads[0].MapsRank)
	}
	if leads[1].MapsRank != -1 || leads[2].MapsRank != 0 {
		t.Fatalf("ranks %d %d", leads[1].MapsRank, leads[2].MapsRank)
	}
	found := false
	for _, p := range leads[0].Points {
		if p.Key == "not_top3" && p.Weight == 15 && strings.Contains(p.PitchEN, `"cafe"`) {
			found = true
		}
	}
	if !found {
		t.Fatal("missing not_top3 point")
	}
}

func TestSamePlaceName(t *testing.T) {
	if !SamePlace(model.Business{Name: "Columbia Cafe"}, model.Business{Name: "Columbia Cafe at the Tampa Bay History Center"}) {
		t.Fatal("prefix match")
	}
	if SamePlace(model.Business{Name: "Cafe"}, model.Business{Name: "Cafe Hey"}) {
		t.Fatal("short name should not prefix-match")
	}
}
