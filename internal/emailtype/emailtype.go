// Package emailtype classifies emails into priority tiers:
// personal (free provider or person-name local part) > named (other
// non-generic same-domain) > generic (role mailboxes like info@).
package emailtype

import (
	"regexp"
	"sort"
	"strings"
)

const (
	Personal = "personal"
	Named    = "named"
	Generic  = "generic"
)

var freeProviders = map[string]bool{
	"gmail.com": true, "googlemail.com": true, "outlook.com": true, "hotmail.com": true, "live.com": true, "msn.com": true,
	"yahoo.com": true, "ymail.com": true, "icloud.com": true, "me.com": true, "mac.com": true, "aol.com": true,
	"proton.me": true, "protonmail.com": true, "gmx.com": true, "zoho.com": true, "qq.com": true, "163.com": true, "126.com": true,
	"hotmail.co.uk": true, "yahoo.co.uk": true, "outlook.es": true, "hotmail.es": true, "yahoo.es": true,
}

var roleWords = []string{"info", "contact", "contacts", "hello", "hi", "hey", "reservations", "reservation", "reserve", "sales", "support", "admin",
	"office", "events", "event", "booking", "bookings", "book", "orders", "order", "catering", "team", "mail", "email", "enquiries", "enquiry",
	"inquiries", "inquiry", "help", "service", "customerservice", "care", "billing", "accounts", "accounting", "marketing", "press", "media",
	"pr", "jobs", "careers", "hr", "noreply", "no-reply", "donotreply", "webmaster", "postmaster", "general", "feedback", "comments", "comment", "welcome",
	"store", "shop", "online", "manager", "management", "frontdesk", "reception", "privatedining", "private", "party", "parties",
	"giftcards", "gifts", "wholesale", "partners", "partnerships", "community", "social", "news", "newsletter", "ask", "questions", "owner", "eat", "dine", "coffee", "cafe"}

var roleSet = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range roleWords {
		m[w] = true
	}
	return m
}()

// common first names (lowercase) for person-name detection
var firstNames = map[string]bool{}

func init() {
	for _, n := range strings.Fields(`james john robert michael william david richard joseph thomas charles christopher daniel matthew anthony mark donald steven paul andrew joshua kenneth kevin brian george timothy ronald edward jason jeffrey ryan jacob gary nicholas eric jonathan stephen larry justin scott brandon benjamin samuel gregory alexander frank patrick raymond jack dennis jerry tyler aaron jose adam nathan henry douglas zachary peter kyle noah ethan jeremy walter christian keith roger terry austin sean gerald carl harold dylan arthur lawrence jordan jesse bryan billy bruce gabriel joe logan alan juan albert willie elijah wayne randy vincent mason roy ralph bobby russell bradley philip eugene carlos luis miguel jorge pedro antonio manuel francisco javier diego alejandro ricardo fernando rafael marco mario sergio andres pablo mike tom tim bob steve dave dan chris matt nick tony alex sam ben max leo luca marie
mary patricia jennifer linda elizabeth barbara susan jessica sarah karen lisa nancy betty margaret sandra ashley kimberly emily donna michelle carol amanda dorothy melissa deborah stephanie rebecca sharon laura cynthia kathleen amy angela shirley anna brenda pamela emma nicole helen samantha katherine christine debra rachel carolyn janet catherine maria heather diane ruth julie olivia joyce virginia victoria kelly lauren christina joan evelyn judith megan andrea cheryl hannah jacqueline martha gloria teresa ann sara madison frances kathryn janice jean abigail alice judy sophia grace denise amber doris marilyn danielle beverly isabella theresa diana natalie brittany charlotte marie kayla alexis lori ana sofia lucia carmen rosa elena isabel paula valentina camila daniela gabriela natalia jenny jen kate katie liz beth sue sally lily mia chloe zoe ella amelia ava`) {
		firstNames[n] = true
	}
}

var (
	sepRe   = regexp.MustCompile(`[._\-]`)
	alphaRe = regexp.MustCompile(`^[a-z]+$`)
)

func split(e string) (local, domain string) {
	e = strings.ToLower(strings.TrimSpace(e))
	at := strings.LastIndex(e, "@")
	if at < 0 {
		return e, ""
	}
	local = e[:at]
	if p := strings.Index(local, "+"); p >= 0 {
		local = local[:p]
	}
	return local, e[at+1:]
}

// IsGenericLocal reports whether a local part is a role mailbox.
func IsGenericLocal(local string) bool {
	if roleSet[local] {
		return true
	}
	parts := sepRe.Split(local, -1)
	if len(parts) > 1 && roleSet[parts[0]] {
		return true // info.miami, reservations-brickell
	}
	// roleword + digits/location suffix like info2, events123
	for _, w := range []string{"info", "contact", "hello", "reservations", "reservation", "sales", "support", "admin", "office", "events", "booking", "bookings", "orders", "catering", "enquiries", "inquiries", "noreply"} {
		if strings.HasPrefix(local, w) && len(local) > len(w) {
			rest := local[len(w):]
			if strings.Trim(rest, "0123456789") == "" {
				return true
			}
		}
	}
	return false
}

// IsPersonLocal reports whether a local part looks like a person's name.
func IsPersonLocal(local string) bool {
	parts := sepRe.Split(local, -1)
	if len(parts) == 2 && alphaRe.MatchString(parts[0]) && alphaRe.MatchString(parts[1]) {
		a, b := parts[0], parts[1]
		if firstNames[a] || firstNames[b] {
			return true
		}
		if len(a) == 1 && len(b) >= 2 { // j.smith
			return true
		}
		if len(b) == 1 && len(a) >= 2 { // john.s
			return true
		}
	}
	if len(parts) == 1 && alphaRe.MatchString(local) {
		if firstNames[local] {
			return true
		}
		// firstname+lastname concatenation, e.g. johnsmith / jsmith handled below
		for n := 3; n < len(local)-1; n++ {
			if firstNames[local[:n]] && len(local)-n >= 3 && len(local) <= 16 {
				return true
			}
		}
	}
	return false
}

// Classify returns personal / named / generic.
func Classify(email string) string {
	local, domain := split(email)
	if IsGenericLocal(local) {
		return Generic
	}
	if freeProviders[domain] || IsPersonLocal(local) {
		return Personal
	}
	return Named
}

// Tier: lower is better (0 personal, 1 named, 2 generic, 3 none).
func Tier(t string) int {
	switch t {
	case Personal:
		return 0
	case Named:
		return 1
	case Generic:
		return 2
	}
	return 3
}

func sameDomain(d, domain string) bool {
	return domain != "" && (d == domain || strings.HasSuffix(d, "."+domain) || strings.HasSuffix(domain, "."+d))
}

// Rank sorts by tier, then same-domain first. Ties keep order. Nothing is dropped.
func Rank(emails []string, domain string) []string {
	out := append([]string{}, emails...)
	key := func(e string) int {
		_, d := split(e)
		k := Tier(Classify(e)) * 2
		if !sameDomain(d, strings.ToLower(domain)) {
			k++
		}
		return k
	}
	sort.SliceStable(out, func(i, j int) bool { return key(out[i]) < key(out[j]) })
	return out
}
