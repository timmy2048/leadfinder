// Package crawl visits business websites and extracts emails and SEO signals.
package crawl

import (
	"encoding/hex"
	"html"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

var (
	emailRe = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,24}`)
	// "info [at] example [dot] com", "info(at)example(dot)com", "info at example dot com" (bracketed only for plain words)
	obfAt      = regexp.MustCompile(`(?i)\s*(\[|\(|\{|<)\s*(at|@)\s*(\]|\)|\}|>)\s*`)
	obfDot     = regexp.MustCompile(`(?i)\s*(\[|\(|\{|<)\s*(dot|\.)\s*(\]|\)|\}|>)\s*`)
	obfAtW     = regexp.MustCompile(`(?i)([a-z0-9._%+\-]+)\s+at\s+([a-z0-9\-]+)\s+dot\s+([a-z]{2,24})\b`)
	cfAttr     = regexp.MustCompile(`data-cfemail="([0-9a-fA-F]+)"`)
	cfHref     = regexp.MustCompile(`/cdn-cgi/l/email-protection#([0-9a-fA-F]+)`)
	badTLDs    = map[string]bool{"png": true, "jpg": true, "jpeg": true, "gif": true, "webp": true, "svg": true, "css": true, "js": true, "ico": true, "avif": true, "mp4": true, "pdf": true}
	badDomains = []string{"sentry", "wixpress.com", "example.com", "example.org", "domain.com", "email.com", "yourdomain", "godaddy.com", "sentry.io", "schema.org", "w3.org", "cloudflare.com", "googleusercontent", "@2x", "@3x"}
)

// DecodeCF decodes a Cloudflare email-protection hex string.
func DecodeCF(enc string) string {
	b, err := hex.DecodeString(enc)
	if err != nil || len(b) < 2 {
		return ""
	}
	key := b[0]
	out := make([]byte, len(b)-1)
	for i, c := range b[1:] {
		out[i] = c ^ key
	}
	return string(out)
}

// Deobfuscate rewrites [at]/(dot) style text into plain email form.
func Deobfuscate(s string) string {
	s = obfAt.ReplaceAllString(s, "@")
	s = obfDot.ReplaceAllString(s, ".")
	s = obfAtW.ReplaceAllString(s, "$1@$2.$3")
	return s
}

// ExtractEmails returns cleaned, unique emails found in raw HTML.
func ExtractEmails(raw string) []string {
	var cands []string
	for _, m := range cfAttr.FindAllStringSubmatch(raw, -1) {
		cands = append(cands, DecodeCF(m[1]))
	}
	for _, m := range cfHref.FindAllStringSubmatch(raw, -1) {
		cands = append(cands, DecodeCF(m[1]))
	}
	text := html.UnescapeString(raw)
	if u, err := url.QueryUnescape(text); err == nil {
		text = text + "\n" + u
	}
	text = Deobfuscate(text)
	cands = append(cands, emailRe.FindAllString(text, -1)...)
	seen := map[string]bool{}
	var out []string
	for _, e := range cands {
		e = strings.ToLower(strings.Trim(e, ".-_ "))
		e = strings.TrimPrefix(e, "mailto:")
		if !Valid(e) || seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	return out
}

// Valid filters obvious junk.
func Valid(e string) bool {
	at := strings.LastIndex(e, "@")
	if at < 1 || at == len(e)-1 || !emailRe.MatchString(e) {
		return false
	}
	tld := e[strings.LastIndex(e, ".")+1:]
	if badTLDs[tld] {
		return false
	}
	for _, d := range badDomains {
		if strings.Contains(e, d) {
			return false
		}
	}
	// hashed tracking ids like 3f2a...@sentry
	if local := e[:at]; len(local) > 30 && !strings.ContainsAny(local, ".-_") {
		return false
	}
	return true
}

// Rank sorts emails: same-domain first, then role/personal. Ties keep order.
func Rank(emails []string, domain string) []string {
	out := append([]string{}, emails...)
	score := func(e string) int {
		d := e[strings.LastIndex(e, "@")+1:]
		if domain != "" && (d == domain || strings.HasSuffix(d, "."+domain) || strings.HasSuffix(domain, "."+d)) {
			return 0
		}
		return 1
	}
	sort.SliceStable(out, func(i, j int) bool { return score(out[i]) < score(out[j]) })
	return out
}

var contactWords = []string{"contact", "kontakt", "contacto", "contatti", "about", "impressum", "imprint", "team", "legal", "mentions", "uber-uns", "about-us", "联系", "关于", "nous-contacter", "get-in-touch", "reach"}

// IsContactLink reports whether a link likely leads to contact info.
func IsContactLink(href, text string) bool {
	s := strings.ToLower(href + " " + text)
	for _, w := range contactWords {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

var socialRe = regexp.MustCompile(`(?i)https?://(www\.)?(instagram\.com|facebook\.com|fb\.com|tiktok\.com)/[A-Za-z0-9_.@\-/]+`)

// ExtractSocials finds social profile links in HTML.
func ExtractSocials(raw string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range socialRe.FindAllString(raw, -1) {
		m = strings.TrimRight(m, "/\"'")
		low := strings.ToLower(m)
		if strings.Contains(low, "/sharer") || strings.Contains(low, "/share") || strings.Contains(low, "/plugins") || strings.Contains(low, "/tr?") || strings.HasSuffix(low, "/tr") || strings.HasSuffix(low, ".com") || seen[low] {
			continue
		}
		seen[low] = true
		out = append(out, m)
	}
	return out
}
