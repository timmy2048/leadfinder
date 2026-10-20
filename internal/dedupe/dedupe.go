// Package dedupe merges businesses that refer to the same entity.
package dedupe

import (
	"math"
	"net/url"
	"strings"

	"github.com/timmy2048/leadfinder/internal/model"
)

// Domain returns the registrable-ish host of a website URL (strips www.).
func Domain(site string) string {
	s := strings.TrimSpace(site)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	h := strings.ToLower(u.Hostname())
	h = strings.TrimPrefix(h, "www.")
	// social / generic hosts are not unique business domains
	for _, g := range []string{"facebook.com", "instagram.com", "tiktok.com", "google.com", "linktr.ee", "wa.me", "yelp.com", "tripadvisor."} {
		if strings.Contains(h, g) {
			return ""
		}
	}
	return h
}

// Phone normalizes a phone number to its last 9 digits.
func Phone(p string) string {
	var d []rune
	for _, r := range p {
		if r >= '0' && r <= '9' {
			d = append(d, r)
		}
	}
	if len(d) < 6 {
		return ""
	}
	if len(d) > 9 {
		d = d[len(d)-9:]
	}
	return string(d)
}

// DistanceM is the haversine distance in meters.
func DistanceM(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371000
	toRad := math.Pi / 180
	dLat := (lat2 - lat1) * toRad
	dLon := (lon2 - lon1) * toRad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*toRad)*math.Cos(lat2*toRad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * R * math.Asin(math.Sqrt(a))
}

var accents = strings.NewReplacer("é", "e", "è", "e", "ê", "e", "ë", "e", "à", "a", "á", "a", "â", "a", "ä", "a", "ã", "a", "ö", "o", "ó", "o", "ò", "o", "ô", "o", "ü", "u", "ú", "u", "ù", "u", "í", "i", "ì", "i", "î", "i", "ï", "i", "ñ", "n", "ç", "c", "ß", "ss")

func normName(s string) string {
	var b strings.Builder
	for _, r := range accents.Replace(strings.ToLower(s)) {
		if r > 127 || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func same(a, b *model.Business) bool {
	if da, db := Domain(a.Website), Domain(b.Website); da != "" && da == db {
		return true
	}
	if pa, pb := Phone(a.Phone), Phone(b.Phone); pa != "" && pa == pb {
		return true
	}
	if a.Lat != 0 && b.Lat != 0 && DistanceM(a.Lat, a.Lon, b.Lat, b.Lon) < 60 {
		na, nb := normName(a.Name), normName(b.Name)
		if na != "" && (strings.Contains(na, nb) || strings.Contains(nb, na)) {
			return true
		}
	}
	return false
}

// Merge returns deduplicated businesses; first occurrence wins, empty
// fields are filled from duplicates and list fields are unioned.
func Merge(in []model.Business) []model.Business {
	var out []model.Business
	for _, b := range in {
		merged := false
		for i := range out {
			if same(&out[i], &b) {
				fill(&out[i], &b)
				merged = true
				break
			}
		}
		if !merged {
			out = append(out, b)
		}
	}
	return out
}

func fill(dst, src *model.Business) {
	if dst.Website == "" {
		dst.Website = src.Website
	}
	if dst.Phone == "" {
		dst.Phone = src.Phone
	}
	if dst.Address == "" {
		dst.Address = src.Address
	}
	if dst.Category == "" {
		dst.Category = src.Category
	}
	if src.ReviewCount > dst.ReviewCount {
		dst.ReviewCount, dst.Rating = src.ReviewCount, src.Rating
	}
	if len(src.Categories) > len(dst.Categories) {
		dst.Categories = src.Categories
	}
	dst.HasHours = dst.HasHours || src.HasHours
	dst.PhotosKnown = dst.PhotosKnown || src.PhotosKnown
	dst.OwnerKnown = dst.OwnerKnown || src.OwnerKnown
	if src.PhotoCount > dst.PhotoCount {
		dst.PhotoCount = src.PhotoCount
	}
	if dst.Description == "" {
		dst.Description = src.Description
	}
	if dst.MapsLink == "" {
		dst.MapsLink = src.MapsLink
	}
	if src.EnrichVer < dst.EnrichVer && !hasSrc(dst.Sources, "gmaps") && hasSrc(src.Sources, "gmaps") {
		dst.EnrichVer = 0 // new Maps data: re-enrich for NAP check
	}
	dst.Emails = Union(dst.Emails, src.Emails)
	for e, v := range src.EmailSources {
		if dst.EmailSources == nil {
			dst.EmailSources = map[string]string{}
		}
		if _, ok := dst.EmailSources[e]; !ok {
			dst.EmailSources[e] = v
		}
	}
	dst.Socials = Union(dst.Socials, src.Socials)
	dst.Sources = Union(dst.Sources, src.Sources)
}

// Union merges string slices case-insensitively preserving order.
func Union(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range append(append([]string{}, a...), b...) {
		k := strings.ToLower(strings.TrimSpace(s))
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, strings.TrimSpace(s))
	}
	return out
}

func hasSrc(ss []string, x string) bool {
	for _, s := range ss {
		if s == x {
			return true
		}
	}
	return false
}
