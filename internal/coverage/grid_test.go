package coverage

import (
	"testing"

	"github.com/timmy2048/leadfinder/internal/dedupe"
)

func TestNormalizeKey(t *testing.T) {
	a := NormalizeKey("  Shanghai ", "Cafe", "OSM")
	b := NormalizeKey("shanghai", "cafe", "osm")
	if a != b || a != "shanghai|cafe|osm" {
		t.Fatalf("%q %q", a, b)
	}
	if LeadKey("Bath, UK", "restaurant") != "bath, uk|restaurant" {
		t.Fatal(LeadKey("Bath, UK", "restaurant"))
	}
}

func TestCellsForCircleStableKeys(t *testing.T) {
	c1 := CellsForCircle(51.38, -2.36, 5000, 2000)
	c2 := CellsForCircle(51.38, -2.36, 5000, 2000)
	if len(c1) < 3 {
		t.Fatalf("too few cells: %d", len(c1))
	}
	if len(c1) != len(c2) || c1[0].Key != c2[0].Key {
		t.Fatalf("unstable: %v vs %v", c1[0], c2[0])
	}
	for _, c := range c1 {
		if dedupe.DistanceM(51.38, -2.36, c.Lat, c.Lon) > 5000+2000*0.6+1 {
			t.Fatalf("cell outside: %+v", c)
		}
	}
}

func TestRingDiffExpand(t *testing.T) {
	lat, lon := 31.23, 121.47 // Shanghai-ish
	size := 2500
	inner := CellsForCircle(lat, lon, 5000, size)
	outer := CellsForCircle(lat, lon, 10000, size)
	ring := RingCells(lat, lon, 5000, 10000, size)
	if len(outer) <= len(inner) {
		t.Fatalf("outer %d should exceed inner %d", len(outer), len(inner))
	}
	if len(ring) == 0 || len(ring) >= len(outer) {
		t.Fatalf("ring %d outer %d inner %d", len(ring), len(outer), len(inner))
	}
	// Mark inner as covered → Diff(outer) should roughly equal ring
	covered := make([]Cell, len(inner))
	copy(covered, inner)
	for i := range covered {
		covered[i].Status = "done"
	}
	need := Diff(outer, covered)
	if len(need) == 0 {
		t.Fatal("expected uncovered outer cells")
	}
	// Every needed cell should be outside ~inner radius
	for _, c := range need {
		d := dedupe.DistanceM(lat, lon, c.Lat, c.Lon)
		if d < 5000-float64(size) {
			t.Fatalf("diff returned inner cell dist=%.0f: %+v", d, c)
		}
	}
	// Re-scan same radius: Diff(inner, covered) empty
	if left := Diff(inner, covered); len(left) != 0 {
		t.Fatalf("same radius should skip all, got %d", len(left))
	}
}

func TestCellsForBBox(t *testing.T) {
	cells := CellsForBBox(51.35, -2.40, 51.42, -2.30, 3000)
	if len(cells) < 2 {
		t.Fatalf("%d", len(cells))
	}
	keys := map[string]bool{}
	for _, c := range cells {
		if keys[c.Key] {
			t.Fatal("dup key")
		}
		keys[c.Key] = true
	}
}

func TestApproxMaxRadiusAndCap(t *testing.T) {
	cells := []Cell{
		{Lat: 0, Lon: 0, SizeM: 1000, Status: "done"},
		{Lat: 0.05, Lon: 0, SizeM: 1000, Status: "done"}, // ~5.5km
	}
	r := ApproxMaxRadius(0, 0, cells)
	if r < 4000 || r > 8000 {
		t.Fatalf("radius %d", r)
	}
	if len(Cap(cells, 1)) != 1 {
		t.Fatal("cap")
	}
}
