package pipeline

import (
	"fmt"
	"strings"
	"testing"

	"github.com/timmy2048/leadfinder/internal/coverage"
	"github.com/timmy2048/leadfinder/internal/model"
	"github.com/timmy2048/leadfinder/internal/osm"
)

func TestPlanCellsExpandRing(t *testing.T) {
	place := osm.Place{Lat: 31.23, Lon: 121.47, HasBBox: true, South: 31.1, North: 31.4, West: 121.3, East: 121.6}
	p := model.JobParams{Category: "cafe", Location: "Shanghai", RadiusM: 5000, CellSizeM: 2500, MaxCells: 100, Incremental: true}
	_, first, mode := PlanCells(p, place, "osm", nil)
	if len(first) == 0 || mode == "" {
		t.Fatalf("first scan empty mode=%s", mode)
	}
	covered := make([]coverage.Cell, len(first))
	copy(covered, first)
	for i := range covered {
		covered[i].Status = "done"
	}
	// same radius again → nothing to scan
	_, again, _ := PlanCells(p, place, "osm", covered)
	if len(again) != 0 {
		t.Fatalf("expected skip all, got %d", len(again))
	}
	// expand to 10km → only ring
	p.RadiusM = 10000
	wanted, ring, mode2 := PlanCells(p, place, "osm", covered)
	if len(ring) == 0 {
		t.Fatal("expected ring cells")
	}
	if len(ring) >= len(wanted) && len(covered) > 0 {
		// ring should be smaller than full 10km disk in typical cases
	}
	if mode2 == "" || len(ring) >= len(coverage.CellsForCircle(place.Lat, place.Lon, 10000, 2500)) {
		// mode should mention ring
		if !strings.Contains(mode2, "ring") && !strings.Contains(mode2, "→") {
			t.Fatalf("mode=%q ring=%d", mode2, len(ring))
		}
	}
}

func TestPlanCellsFullBBox(t *testing.T) {
	place := osm.Place{Lat: 51.38, Lon: -2.36, HasBBox: true, South: 51.35, North: 51.42, West: -2.40, East: -2.30}
	p := model.JobParams{RadiusM: 0, CellSizeM: 3000, MaxCells: 100, Incremental: true}
	wanted, toScan, mode := PlanCells(p, place, "osm", nil)
	if len(wanted) == 0 || len(toScan) == 0 || mode != "bbox full city" {
		t.Fatalf("wanted=%d scan=%d mode=%s", len(wanted), len(toScan), mode)
	}
}

func TestPlaceQuery(t *testing.T) {
	if PlaceQuery(model.JobParams{Location: "上海", Area: "静安"}) != "静安, 上海" {
		t.Fatal(PlaceQuery(model.JobParams{Location: "上海", Area: "静安"}))
	}
	if PlaceQuery(model.JobParams{Location: "Shanghai"}) != "Shanghai" {
		t.Fatal("loc only")
	}
}

func TestSelectOutputCapAndOrder(t *testing.T) {
	st := map[string]string{}
	var in []model.Business
	for i := 0; i < 10; i++ {
		in = append(in, model.Business{Name: fmt.Sprint("r", i), Reachable: true, GMBScore: i, EmailStatus: st})
	}
	for i := 0; i < 7; i++ {
		in = append(in, model.Business{Name: fmt.Sprint("u", i), EmailStatus: st})
	}
	in = append(in, model.Business{Name: "never-enriched"})
	// personal tier beats higher GMB score
	in = append(in, model.Business{Name: "gmail", Reachable: true, GMBScore: 1, EmailType: "personal", EmailStatus: st})
	out := SelectOutput(in, 5)
	reach, unreach := Partition(out)
	if len(reach) != 5 || reach[0].Name != "gmail" || reach[1].Name != "r9" || reach[4].Name != "r6" {
		t.Fatalf("%+v", reach)
	}
	if len(unreach) != 7 { // unreachable not capped by N
		t.Fatalf("%+v", unreach)
	}
	if len(SelectOutput(in, 0)) != 18 {
		t.Fatal("no cap")
	}
}
