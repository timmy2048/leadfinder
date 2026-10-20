package crawl

import (
	"regexp"
	"strings"
)

// PageSignals are GMB-relevant checks on one HTML page.
type PageSignals struct {
	Viewport, Schema, MapEmbed, MapsLink bool
}

var (
	schemaTypeRe = regexp.MustCompile(`(?i)"@type"\s*:\s*\[?\s*"(localbusiness|restaurant|foodestablishment|cafeorcoffeeshop|barorpub|bakery|store|dentist|medicalbusiness|professionalservice|homeandconstructionbusiness|autorepair|beautysalon|hairsalon|dayspa|healthandbeautybusiness|lodgingbusiness|hotel|legalservice|attorney|realestateagent|plumber|electrician|locksmith|roofingcontractor|generalcontractor|hvacbusiness|movingcompany|financialservice|sportsactivitylocation|exerciseGym|healthclub|veterinarycare|childcare|automotivebusiness|entertainmentbusiness|fastfoodrestaurant|winery|brewery)"`)
	microRe      = regexp.MustCompile(`(?i)itemtype=["']https?://schema\.org/(localbusiness|restaurant|foodestablishment|store)`)
	mapsLinkRe   = regexp.MustCompile(`(?i)(google\.[a-z.]+/maps|maps\.google\.|goo\.gl/maps|maps\.app\.goo\.gl|g\.page/)`)
	embedRe      = regexp.MustCompile(`(?i)<iframe[^>]+src=["'][^"']*(google\.[a-z.]+/maps|maps\.google\.)`)
)

// Signals inspects raw HTML.
func Signals(body string) PageSignals {
	low := strings.ToLower(body)
	return PageSignals{
		Viewport: strings.Contains(low, `name="viewport"`) || strings.Contains(low, `name='viewport'`) || strings.Contains(low, `name=viewport`),
		Schema:   schemaTypeRe.MatchString(body) || microRe.MatchString(body),
		MapEmbed: embedRe.MatchString(body),
		MapsLink: mapsLinkRe.MatchString(body),
	}
}

var nonDigit = regexp.MustCompile(`\D`)

// Digits keeps only digits.
func Digits(s string) string { return nonDigit.ReplaceAllString(s, "") }

// NAPIssues compares Maps name/address/phone against site text (lowercased).
// Returns subset of {"phone","address","name"} that could not be found on the site.
func NAPIssues(name, address, phone, text string) []string {
	if text == "" {
		return nil
	}
	var out []string
	if d := Digits(phone); len(d) >= 7 {
		tail := d[len(d)-7:]
		if !strings.Contains(Digits(text), tail) {
			out = append(out, "phone")
		}
	}
	if address != "" {
		// street line: "123 Main St, City, ..." -> number + first street word
		street := strings.ToLower(strings.TrimSpace(strings.SplitN(address, ",", 2)[0]))
		f := strings.Fields(street)
		if len(f) >= 2 && Digits(f[0]) != "" {
			if !strings.Contains(text, f[0]) || !strings.Contains(text, strings.Trim(f[1], ".")) {
				out = append(out, "address")
			}
		}
	}
	if name != "" {
		found := false
		for _, w := range strings.Fields(strings.ToLower(name)) {
			w = strings.Trim(w, "'’&-.,()")
			if len(w) >= 4 && strings.Contains(text, w) {
				found = true
				break
			}
		}
		if !found && len(strings.TrimSpace(name)) >= 4 {
			out = append(out, "name")
		}
	}
	return out
}
