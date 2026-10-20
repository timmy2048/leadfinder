// Package rdap looks up registrant/admin/tech contact emails via the free
// RDAP protocol (rdap.org bootstrap). Most registrations are privacy-redacted;
// those are skipped.
package rdap

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/timmy2048/leadfinder/internal/crawl"
	"github.com/timmy2048/leadfinder/internal/fetch"
)

var redacted = []string{"redact", "privacy", "proxy", "whoisguard", "domainsbyproxy", "withheld", "contactprivacy", "protect", "anonymi", "abuse", "registrar", "hostmaster", "dnsadmin", "godaddy", "namecheap", "squarespace", "wix", "google.com", "tucows", "networksolutions", "web.com", "ionos", "gandi", "porkbun", "cloudflare", "markmonitor", "csc", "enom"}

type entity struct {
	Roles    []string          `json:"roles"`
	Vcard    []json.RawMessage `json:"vcardArray"`
	Entities []entity          `json:"entities"`
}

// Emails returns non-redacted registrant/administrative/technical emails.
func Emails(ctx context.Context, f *fetch.Client, domain string) []string {
	if domain == "" {
		return nil
	}
	r, err := f.Get(ctx, "https://rdap.org/domain/"+domain, map[string]string{"Accept": "application/rdap+json"})
	if err != nil || r.Status != 200 {
		return nil
	}
	var doc struct {
		Entities []entity `json:"entities"`
	}
	if json.Unmarshal(r.Body, &doc) != nil {
		return nil
	}
	var out []string
	var walk func([]entity)
	walk = func(es []entity) {
		for _, e := range es {
			ok := false
			for _, ro := range e.Roles {
				if ro == "registrant" || ro == "administrative" || ro == "technical" {
					ok = true
				}
			}
			if ok {
				for _, em := range crawl.ExtractEmails(string(joinRaw(e.Vcard))) {
					if Clean(em) {
						out = append(out, em)
					}
				}
			}
			walk(e.Entities)
		}
	}
	walk(doc.Entities)
	return out
}

func joinRaw(v []json.RawMessage) []byte {
	var b []byte
	for _, x := range v {
		b = append(b, x...)
		b = append(b, ' ')
	}
	return b
}

// Clean reports whether an RDAP email looks like a real owner contact.
func Clean(e string) bool {
	l := strings.ToLower(e)
	for _, w := range redacted {
		if strings.Contains(l, w) {
			return false
		}
	}
	return true
}
