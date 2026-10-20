// Package coverage builds and diffs geographic scan cells for incremental jobs.
package coverage

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/timmy2048/leadfinder/internal/dedupe"
)

// Cell is one grid tile to scan or already scanned.
type Cell struct {
	Key      string    `json:"key"`
	Lat      float64   `json:"lat"`
	Lon      float64   `json:"lon"`
	SizeM    int       `json:"size_m"`
	Status   string    `json:"status"` // done, pending, failed
	ScannedAt time.Time `json:"scanned_at,omitempty"`
}

// Summary describes prior coverage for a city+category+source.
type Summary struct {
	Key         string  `json:"key"`
	CellCount   int     `json:"cell_count"`
	MaxRadiusM  int     `json:"max_radius_m"` // approximate from farthest done cell to origin
	FullBBox    bool    `json:"full_bbox"`
	LastScan    time.Time `json:"last_scan,omitempty"`
}

// NormalizeKey builds a stable coverage key from location + category + source.
func NormalizeKey(location, category, source string) string {
	loc := strings.ToLower(strings.TrimSpace(location))
	cat := strings.ToLower(strings.TrimSpace(category))
	src := strings.ToLower(strings.TrimSpace(source))
	loc = strings.Join(strings.Fields(loc), " ")
	cat = strings.Join(strings.Fields(cat), " ")
	return loc + "|" + cat + "|" + src
}

// LeadKey is the shared lead library key (no source).
func LeadKey(location, category string) string {
	loc := strings.ToLower(strings.TrimSpace(location))
	cat := strings.ToLower(strings.TrimSpace(category))
	loc = strings.Join(strings.Fields(loc), " ")
	cat = strings.Join(strings.Fields(cat), " ")
	return loc + "|" + cat
}

// cellKey quantizes lat/lon to a stable id for a given size.
func cellKey(lat, lon float64, sizeM int) string {
	dLat, dLon := stepDeg(lat, sizeM)
	i := int(math.Floor(lat / dLat))
	j := int(math.Floor(lon / dLon))
	return fmt.Sprintf("%d:%d:%d", sizeM, i, j)
}

func stepDeg(lat float64, sizeM int) (dLat, dLon float64) {
	if sizeM <= 0 {
		sizeM = 2500
	}
	dLat = float64(sizeM) / 111320.0
	cos := math.Cos(lat * math.Pi / 180)
	if cos < 0.1 {
		cos = 0.1
	}
	dLon = float64(sizeM) / (111320.0 * cos)
	return dLat, dLon
}

// CellsForCircle returns square-grid cells whose centers fall within radiusM of origin
// (plus half a cell, so the circle edge is covered).
func CellsForCircle(lat, lon float64, radiusM, sizeM int) []Cell {
	if sizeM <= 0 {
		sizeM = 2500
	}
	if radiusM <= 0 {
		return nil
	}
	dLat, dLon := stepDeg(lat, sizeM)
	nLat := int(math.Ceil(float64(radiusM)/float64(sizeM))) + 1
	nLon := nLat
	var out []Cell
	seen := map[string]bool{}
	limit := float64(radiusM) + float64(sizeM)*0.6
	for i := -nLat; i <= nLat; i++ {
		for j := -nLon; j <= nLon; j++ {
			clat := lat + float64(i)*dLat
			clon := lon + float64(j)*dLon
			clat2, clon2 := cellCenter(clat, clon, sizeM)
			if dedupe.DistanceM(lat, lon, clat2, clon2) > limit {
				continue
			}
			k := cellKey(clat2, clon2, sizeM)
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, Cell{Key: k, Lat: clat2, Lon: clon2, SizeM: sizeM, Status: "pending"})
		}
	}
	return out
}


func cellCenter(lat, lon float64, sizeM int) (float64, float64) {
	dLat, dLon := stepDeg(lat, sizeM)
	i := math.Floor(lat / dLat)
	j := math.Floor(lon / dLon)
	return (i+0.5)*dLat, (j+0.5)*dLon
}

// CellsForBBox tiles a geographic bounding box.
func CellsForBBox(south, west, north, east float64, sizeM int) []Cell {
	if sizeM <= 0 {
		sizeM = 2500
	}
	if north < south {
		south, north = north, south
	}
	if east < west {
		west, east = east, west
	}
	midLat := (south + north) / 2
	dLat, dLon := stepDeg(midLat, sizeM)
	var out []Cell
	seen := map[string]bool{}
	for lat := south; lat <= north+dLat*0.01; lat += dLat {
		for lon := west; lon <= east+dLon*0.01; lon += dLon {
			clat, clon := cellCenter(lat, lon, sizeM)
			if clat < south-dLat || clat > north+dLat || clon < west-dLon || clon > east+dLon {
				continue
			}
			k := cellKey(clat, clon, sizeM)
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, Cell{Key: k, Lat: clat, Lon: clon, SizeM: sizeM, Status: "pending"})
		}
	}
	return out
}

// RingCells returns cells in the disk of newRadius that are outside oldRadius
// (the expansion ring). oldRadius 0 means the full new disk.
func RingCells(lat, lon float64, oldRadiusM, newRadiusM, sizeM int) []Cell {
	all := CellsForCircle(lat, lon, newRadiusM, sizeM)
	if oldRadiusM <= 0 {
		return all
	}
	inner := float64(oldRadiusM) - float64(sizeM)*0.4
	if inner < 0 {
		inner = 0
	}
	var ring []Cell
	for _, c := range all {
		d := dedupe.DistanceM(lat, lon, c.Lat, c.Lon)
		if d > inner {
			ring = append(ring, c)
		}
	}
	return ring
}

// Diff returns wanted cells whose Key is not among covered (status done).
func Diff(wanted []Cell, covered []Cell) []Cell {
	have := map[string]bool{}
	for _, c := range covered {
		if c.Status == "done" || c.Status == "" {
			have[c.Key] = true
		}
	}
	var out []Cell
	for _, w := range wanted {
		if have[w.Key] {
			continue
		}
		out = append(out, w)
	}
	return out
}

// ApproxMaxRadius estimates the farthest covered cell from origin.
func ApproxMaxRadius(originLat, originLon float64, covered []Cell) int {
	max := 0.0
	for _, c := range covered {
		if c.Status != "done" && c.Status != "" {
			continue
		}
		d := dedupe.DistanceM(originLat, originLon, c.Lat, c.Lon) + float64(c.SizeM)/2
		if d > max {
			max = d
		}
	}
	return int(math.Round(max))
}

// Cap truncates cells to n (keeps first n).
func Cap(cells []Cell, n int) []Cell {
	if n <= 0 || len(cells) <= n {
		return cells
	}
	return cells[:n]
}
