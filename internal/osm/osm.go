// Package osm queries Nominatim (geocoding) and Overpass (POIs).
package osm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/timmy2048/leadfinder/internal/model"
)

const UserAgent = "leadfinder/0.1 (personal research tool; https://github.com/timmy2048/leadfinder)"

// Client talks to public OSM services. Respect their usage policies:
// Nominatim max 1 req/s, Overpass keep queries light.
type Client struct {
	HTTP         *http.Client
	NominatimURL string
	OverpassURL  string
}

func New() *Client {
	return &Client{
		HTTP:         &http.Client{Timeout: 120 * time.Second},
		NominatimURL: "https://nominatim.openstreetmap.org/search",
		OverpassURL:  "https://overpass-api.de/api/interpreter",
	}
}

// Place is a geocoded location with optional bounding box.
type Place struct {
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	South       float64 `json:"south"`
	North       float64 `json:"north"`
	West        float64 `json:"west"`
	East        float64 `json:"east"`
	DisplayName string  `json:"display_name"`
	HasBBox     bool    `json:"has_bbox"`
}

// Geocode returns lat/lon for a free-form location.
func (c *Client) Geocode(ctx context.Context, location, country string) (float64, float64, error) {
	p, err := c.GeocodePlace(ctx, location, country)
	return p.Lat, p.Lon, err
}

// GeocodePlace returns lat/lon and Nominatim bounding box when available.
func (c *Client) GeocodePlace(ctx context.Context, location, country string) (Place, error) {
	q := url.Values{"q": {location}, "format": {"json"}, "limit": {"1"}}
	if country != "" {
		q.Set("countrycodes", strings.ToLower(country))
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.NominatimURL+"?"+q.Encode(), nil)
	req.Header.Set("User-Agent", UserAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Place{}, err
	}
	defer resp.Body.Close()
	var out []struct {
		Lat         string   `json:"lat"`
		Lon         string   `json:"lon"`
		DisplayName string   `json:"display_name"`
		BoundingBox []string `json:"boundingbox"` // south, north, west, east
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Place{}, fmt.Errorf("nominatim decode: %w", err)
	}
	if len(out) == 0 {
		return Place{}, fmt.Errorf("location not found: %q", location)
	}
	lat, _ := strconv.ParseFloat(out[0].Lat, 64)
	lon, _ := strconv.ParseFloat(out[0].Lon, 64)
	p := Place{Lat: lat, Lon: lon, DisplayName: out[0].DisplayName}
	if len(out[0].BoundingBox) == 4 {
		p.South, _ = strconv.ParseFloat(out[0].BoundingBox[0], 64)
		p.North, _ = strconv.ParseFloat(out[0].BoundingBox[1], 64)
		p.West, _ = strconv.ParseFloat(out[0].BoundingBox[2], 64)
		p.East, _ = strconv.ParseFloat(out[0].BoundingBox[3], 64)
		p.HasBBox = p.South != 0 || p.North != 0 || p.West != 0 || p.East != 0
	}
	return p, nil
}

// categoryTags maps common words to OSM tag filters.
var categoryTags = map[string][]string{
	"restaurant": {`"amenity"="restaurant"`}, "cafe": {`"amenity"="cafe"`}, "coffee": {`"amenity"="cafe"`},
	"bar": {`"amenity"="bar"`, `"amenity"="pub"`}, "dentist": {`"amenity"="dentist"`, `"healthcare"="dentist"`},
	"doctor": {`"amenity"="doctors"`, `"amenity"="clinic"`}, "clinic": {`"amenity"="clinic"`},
	"pharmacy": {`"amenity"="pharmacy"`}, "hotel": {`"tourism"="hotel"`, `"tourism"="guest_house"`},
	"lawyer": {`"office"="lawyer"`}, "accountant": {`"office"="accountant"`}, "real estate": {`"office"="estate_agent"`},
	"hairdresser": {`"shop"="hairdresser"`}, "salon": {`"shop"="hairdresser"`, `"shop"="beauty"`}, "beauty": {`"shop"="beauty"`},
	"gym": {`"leisure"="fitness_centre"`}, "fitness": {`"leisure"="fitness_centre"`}, "bakery": {`"shop"="bakery"`},
	"car repair": {`"shop"="car_repair"`}, "plumber": {`"craft"="plumber"`}, "electrician": {`"craft"="electrician"`},
	"florist": {`"shop"="florist"`}, "veterinary": {`"amenity"="veterinary"`}, "vet": {`"amenity"="veterinary"`},
	"supermarket": {`"shop"="supermarket"`}, "clothes": {`"shop"="clothes"`}, "furniture": {`"shop"="furniture"`},
}

// TagFilters returns Overpass tag filters for a category. Unknown categories
// fall back to raw "key=value" input or a name/cuisine/shop regex search.
func TagFilters(category string) []string {
	c := strings.ToLower(strings.TrimSpace(category))
	if f, ok := categoryTags[c]; ok {
		return f
	}
	if strings.HasSuffix(c, "s") {
		if f, ok := categoryTags[strings.TrimSuffix(c, "s")]; ok {
			return f
		}
	}
	if k, v, ok := strings.Cut(c, "="); ok {
		return []string{fmt.Sprintf("%q=%q", strings.TrimSpace(k), strings.TrimSpace(v))}
	}
	re := regexpQuote(c)
	return []string{
		fmt.Sprintf(`"amenity"~"%s",i`, re), fmt.Sprintf(`"shop"~"%s",i`, re),
		fmt.Sprintf(`"craft"~"%s",i`, re), fmt.Sprintf(`"office"~"%s",i`, re),
		fmt.Sprintf(`"cuisine"~"%s",i`, re),
	}
}

func regexpQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, ``, `.`, `\\.`, `*`, ``, `+`, ``, `?`, ``, `(`, ``, `)`, ``, `[`, ``, `]`, ``, ` `, `_?`)
	return r.Replace(s)
}

// BuildQuery builds an Overpass QL query around a point.
func BuildQuery(category string, lat, lon float64, radius, limit int) string {
	var b strings.Builder
	b.WriteString("[out:json][timeout:90];\n(\n")
	for _, f := range TagFilters(category) {
		fmt.Fprintf(&b, "  nwr[%s](around:%d,%f,%f);\n", f, radius, lat, lon)
	}
	b.WriteString(");\n")
	if limit <= 0 {
		limit = 100
	}
	fmt.Fprintf(&b, "out center tags %d;\n", limit*3)
	return b.String()
}

// BuildQueryBBox builds an Overpass QL query inside a bounding box.
func BuildQueryBBox(category string, south, west, north, east float64, limit int) string {
	var b strings.Builder
	b.WriteString("[out:json][timeout:90];\n(\n")
	for _, f := range TagFilters(category) {
		fmt.Fprintf(&b, "  nwr[%s](%f,%f,%f,%f);\n", f, south, west, north, east)
	}
	b.WriteString(");\n")
	if limit <= 0 {
		limit = 100
	}
	fmt.Fprintf(&b, "out center tags %d;\n", limit*3)
	return b.String()
}

type element struct {
	Type   string                      `json:"type"`
	ID     int64                       `json:"id"`
	Lat    float64                     `json:"lat"`
	Lon    float64                     `json:"lon"`
	Center *struct{ Lat, Lon float64 } `json:"center"`
	Tags   map[string]string           `json:"tags"`
}

func (c *Client) overpass(ctx context.Context, q string) ([]byte, error) {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			wait := time.Duration(attempt*attempt) * 3 * time.Second
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.OverpassURL, strings.NewReader(url.Values{"data": {q}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("User-Agent", UserAgent)
		resp, err := c.HTTP.Do(req)
		if err != nil {
			last = err
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == 200 {
			return body, nil
		}
		last = fmt.Errorf("overpass status %d: %.200s", resp.StatusCode, body)
		if resp.StatusCode != 429 && resp.StatusCode < 500 {
			return nil, last
		}
	}
	return nil, last
}

// Search runs an Overpass query around lat/lon.
func (c *Client) Search(ctx context.Context, category string, lat, lon float64, radius, limit int) ([]model.Business, error) {
	body, err := c.overpass(ctx, BuildQuery(category, lat, lon, radius, limit))
	if err != nil {
		return nil, err
	}
	return ParseResponse(body, category, limit)
}

// SearchBBox runs an Overpass query inside a bbox.
func (c *Client) SearchBBox(ctx context.Context, category string, south, west, north, east float64, limit int) ([]model.Business, error) {
	body, err := c.overpass(ctx, BuildQueryBBox(category, south, west, north, east, limit))
	if err != nil {
		return nil, err
	}
	return ParseResponse(body, category, limit)
}

// ParseResponse converts Overpass JSON into businesses.
func ParseResponse(body []byte, category string, limit int) ([]model.Business, error) {
	var out struct{ Elements []element }
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("overpass decode: %w", err)
	}
	var res []model.Business
	for _, e := range out.Elements {
		t := e.Tags
		if t["name"] == "" {
			continue
		}
		b := model.Business{Name: t["name"], Category: firstNonEmpty(t["amenity"], t["shop"], t["craft"], t["office"], t["tourism"], t["leisure"], category), Lat: e.Lat, Lon: e.Lon, Sources: []string{"osm"}}
		if e.Center != nil {
			b.Lat, b.Lon = e.Center.Lat, e.Center.Lon
		}
		b.Phone = firstNonEmpty(t["phone"], t["contact:phone"])
		b.Website = firstNonEmpty(t["website"], t["contact:website"], t["url"])
		for _, k := range []string{"email", "contact:email"} {
			for _, em := range strings.Split(t[k], ";") {
				if em = strings.TrimSpace(em); em != "" {
					b.Emails = append(b.Emails, strings.ToLower(em))
				}
			}
		}
		for _, k := range []string{"contact:facebook", "contact:instagram", "contact:tiktok", "facebook", "instagram"} {
			if v := t[k]; v != "" {
				b.Socials = append(b.Socials, v)
			}
		}
		b.Address = strings.TrimSpace(strings.Join(nonEmpty(t["addr:housenumber"]+" "+t["addr:street"], t["addr:postcode"]+" "+t["addr:city"]), ", "))
		res = append(res, b)
		if limit > 0 && len(res) >= limit {
			break
		}
	}
	return res, nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func nonEmpty(v ...string) []string {
	var o []string
	for _, s := range v {
		if s = strings.TrimSpace(s); s != "" {
			o = append(o, s)
		}
	}
	return o
}
