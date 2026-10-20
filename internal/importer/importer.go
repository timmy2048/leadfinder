// Package importer reads lead CSVs exported by Outscraper, Apify (Google Maps
// Scraper), PhantomBuster and similar tools, mapping common column names.
package importer

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/timmy2048/leadfinder/internal/crawl"
	"github.com/timmy2048/leadfinder/internal/model"
)

var aliases = map[string][]string{
	"name":     {"name", "title", "business_name", "businessname", "company", "company_name", "place_name", "query_name"},
	"phone":    {"phone", "phone_number", "phonenumber", "phoneunformatted", "telephone", "international_phone_number", "phone_1"},
	"website":  {"website", "site", "url_website", "domain", "web", "website_url", "company_website"},
	"address":  {"address", "full_address", "fulladdress", "street", "formatted_address", "location"},
	"rating":   {"rating", "totalscore", "total_score", "stars", "review_rating"},
	"reviews":  {"reviews", "reviews_count", "reviewscount", "review_count", "user_ratings_total", "reviews_number"},
	"category": {"category", "type", "categoryname", "category_name", "main_category", "subtypes"},
	"lat":      {"latitude", "lat", "location/lat"},
	"lon":      {"longitude", "lng", "lon", "location/lng"},
	"maps":     {"location_link", "url", "google_maps_url", "place_url", "link"},
	"desc":     {"description", "about"},
}

func norm(h string) string {
	h = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")))
	return strings.NewReplacer(" ", "_", "-", "_", ".", "/").Replace(h)
}

// Parse reads a CSV and returns businesses with Sources=["import"].
// Any column whose header contains "email" contributes emails (email_1, emails, ...).
func Parse(r io.Reader) ([]model.Business, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	head, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	idx := map[string]int{}
	var emailCols []int
	for i, h := range head {
		n := norm(h)
		for key, al := range aliases {
			for _, a := range al {
				if n == a {
					if _, ok := idx[key]; !ok {
						idx[key] = i
					}
				}
			}
		}
		if strings.Contains(n, "email") && !strings.Contains(n, "status") && !strings.Contains(n, "valid") {
			emailCols = append(emailCols, i)
		}
	}
	if _, ok := idx["name"]; !ok {
		return nil, fmt.Errorf("no name/title column in %v", head)
	}
	var out []model.Business
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return out, err
		}
		col := func(k string) string {
			if i, ok := idx[k]; ok && i < len(rec) {
				return strings.TrimSpace(rec[i])
			}
			return ""
		}
		b := model.Business{Name: col("name"), Phone: col("phone"), Website: col("website"), Address: col("address"),
			Category: col("category"), Description: col("desc"), MapsLink: col("maps"), Sources: []string{"import"}}
		if b.Name == "" {
			continue
		}
		if b.Website != "" && !strings.Contains(b.Website, "://") {
			b.Website = "http://" + b.Website
		}
		b.Rating, _ = strconv.ParseFloat(col("rating"), 64)
		b.ReviewCount, _ = strconv.Atoi(strings.ReplaceAll(col("reviews"), ",", ""))
		b.Lat, _ = strconv.ParseFloat(col("lat"), 64)
		b.Lon, _ = strconv.ParseFloat(col("lon"), 64)
		b.EmailSources = map[string]string{}
		for _, i := range emailCols {
			if i < len(rec) {
				for _, e := range crawl.ExtractEmails(rec[i]) {
					if _, ok := b.EmailSources[e]; !ok {
						b.Emails = append(b.Emails, e)
						b.EmailSources[e] = "import"
					}
				}
			}
		}
		if b.Rating > 0 || b.ReviewCount > 0 || b.MapsLink != "" {
			b.Sources = append(b.Sources, "gmaps") // Maps-derived exports: enable Maps-profile points
		}
		out = append(out, b)
	}
	return out, nil
}
