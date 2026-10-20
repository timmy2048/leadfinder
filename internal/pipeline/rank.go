package pipeline

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/timmy2048/leadfinder/internal/dedupe"
	"github.com/timmy2048/leadfinder/internal/gmaps"
	"github.com/timmy2048/leadfinder/internal/model"
	"github.com/timmy2048/leadfinder/internal/score"
)

// RankDepth is how many Google Maps results are inspected per search.
const RankDepth = 20

// RankSearcher returns ordered Maps results for keyword around lat/lon.
type RankSearcher func(ctx context.Context, keyword string, lat, lon float64) ([]model.Business, error)

// GMapsRankSearcher uses gosom fast mode (one search page, results in rank order).
func GMapsRankSearcher(url, lang string) RankSearcher {
	return func(ctx context.Context, kw string, lat, lon float64) ([]model.Business, error) {
		return gmaps.New(url).Scrape(ctx, gmaps.Request{
			Name: fmt.Sprintf("lf-rank-%.4f,%.4f", lat, lon), Keywords: []string{kw}, Lang: lang, Zoom: 15,
			Lat: strconv.FormatFloat(lat, 'f', 6, 64), Lon: strconv.FormatFloat(lon, 'f', 6, 64),
			Radius: 3000, FastMode: true, Depth: 1, MaxTime: 180,
		}, nil)
	}
}

// AssignRanks sets MapsRank for reachable leads. Leads are grouped into
// cells of cellM meters so one search serves all leads in a cell.
func AssignRanks(ctx context.Context, bs []model.Business, kw string, cellM int, centerLat, centerLon float64, search RankSearcher, prog func(string)) {
	if cellM <= 0 {
		cellM = 2500
	}
	type key struct{ a, b int64 }
	groups := map[key][]int{}
	var order []key
	for i := range bs {
		if !bs[i].Reachable {
			continue
		}
		lat, lon := bs[i].Lat, bs[i].Lon
		if lat == 0 && lon == 0 {
			lat, lon = centerLat, centerLon
		}
		step := float64(cellM) / 111320.0
		k := key{int64(math.Floor(lat / step)), int64(math.Floor(lon / step))}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], i)
	}
	for n, k := range order {
		idx := groups[k]
		var la, lo float64
		for _, i := range idx {
			lat, lon := bs[i].Lat, bs[i].Lon
			if lat == 0 && lon == 0 {
				lat, lon = centerLat, centerLon
			}
			la += lat
			lo += lon
		}
		la, lo = la/float64(len(idx)), lo/float64(len(idx))
		if prog != nil {
			prog(fmt.Sprintf("maps rank: search %d/%d %q @ %.4f,%.4f (%d leads)", n+1, len(order), kw, la, lo, len(idx)))
		}
		res, err := search(ctx, kw, la, lo)
		if err != nil {
			if prog != nil {
				prog("maps rank error: " + err.Error())
			}
			continue
		}
		if len(res) > RankDepth {
			res = res[:RankDepth]
		}
		for _, i := range idx {
			b := &bs[i]
			b.MapsRank, b.MapsRankN, b.RankKeyword = -1, len(res), kw
			for r, c := range res {
				if SamePlace(*b, c) {
					b.MapsRank = r + 1
					break
				}
			}
			b.InLocalPack = b.MapsRank >= 1 && b.MapsRank <= 3
			score.Compute(b)
		}
	}
}

// SamePlace matches a lead to a Maps result by maps link id, phone, website domain or name.
func SamePlace(a, b model.Business) bool {
	if ia, ib := placeID(a.MapsLink), placeID(b.MapsLink); ia != "" && ia == ib {
		return true
	}
	if pa, pb := digits(a.Phone), digits(b.Phone); len(pa) >= 7 && pa == pb {
		return true
	}
	if da, db := dedupe.Domain(a.Website), dedupe.Domain(b.Website); da != "" && da == db {
		return true
	}
	na, nb := normName(a.Name), normName(b.Name)
	if na == "" || nb == "" {
		return false
	}
	if na == nb {
		return true
	}
	return len(na) >= 6 && len(nb) >= 6 && (strings.HasPrefix(nb, na) || strings.HasPrefix(na, nb))
}

func placeID(link string) string {
	i := strings.Index(link, "!1s0x")
	if i < 0 {
		return ""
	}
	s := link[i+3:]
	if j := strings.Index(s, "!"); j >= 0 {
		s = s[:j]
	}
	return s
}

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	d := b.String()
	if len(d) > 10 {
		d = d[len(d)-10:]
	}
	return d
}

func normName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r > 127 {
			b.WriteRune(r)
		}
	}
	return b.String()
}
