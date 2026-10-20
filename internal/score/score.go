// Package score computes a Google Business Profile (GMB) opportunity score
// for reachable leads, as a sum of explainable improvement points.
package score

import (
	"fmt"
	"sort"

	"github.com/timmy2048/leadfinder/internal/emailtype"
	"strings"

	"github.com/timmy2048/leadfinder/internal/model"
)

// Thresholds (tunable).
var (
	MinReviews       = 50
	MinRating        = 4.2
	MinPhotos        = 10
	SlowMS     int64 = 3000
)

// Def describes a point; NeedsGMaps means it can only be judged with Google Maps data.
type Def struct {
	model.Point
	NeedsGMaps bool
}

func d(key, zh, en string, w int, g bool) Def {
	return Def{model.Point{Key: key, LabelZH: zh, PitchEN: en, Weight: w}, g}
}

// Catalog lists all points.
var Catalog = map[string]Def{
	"unclaimed":        d("unclaimed", "地图商家资料疑似未认领", "Your Google Business Profile appears unclaimed, so anyone can suggest edits and you can't reply to reviews.", 25, true),
	"few_reviews":      d("few_reviews", "评论数少", "Your Google profile has few reviews; a review-request system can lift trust and ranking.", 20, true),
	"low_rating":       d("low_rating", "评分偏低(<4.2)", "Your rating is below 4.2; review management and response templates can raise it.", 15, true),
	"no_hours":         d("no_hours", "地图缺营业时间", "Your profile is missing opening hours, which Google uses for 'open now' searches.", 10, true),
	"few_photos":       d("few_photos", "照片少", "Your profile has few photos; listings with fresh photos get more clicks and calls.", 10, true),
	"generic_category": d("generic_category", "仅一个笼统类目", "Only one generic category is set; adding precise secondary categories widens the searches you appear in.", 10, true),
	"no_description":   d("no_description", "缺商家描述", "Your profile has no business description; a keyword-rich one helps local ranking.", 10, true),
	"no_site_on_maps":  d("no_site_on_maps", "地图资料未关联网站", "Your Google profile doesn't link to your website, losing traffic and trust signals.", 10, true),
	"nap_mismatch":     d("nap_mismatch", "地图与官网NAP不一致", "Name/address/phone on your website doesn't match Google Maps; consistent NAP is a core local-ranking factor.", 15, true),
	"not_top3":         d("not_top3", "未进入地图前三", "You're not in the top 3 Google Maps results for \"%s\" near you, where most local clicks go.", 15, true),
	"site_no_maps":     d("site_no_maps", "官网未链接/嵌入谷歌地图", "Your website doesn't link to or embed your Google Maps listing.", 5, false),
	"no_https":         d("no_https", "官网无HTTPS", "Your website isn't on HTTPS, so browsers mark it 'Not secure'.", 10, false),
	"slow_site":        d("slow_site", "官网加载慢", "Your website loads slowly, which hurts mobile visitors and rankings.", 8, false),
	"no_title_meta":    d("no_title_meta", "缺title/meta描述", "Your homepage is missing a title or meta description, so Google shows a weak snippet.", 8, false),
	"no_schema":        d("no_schema", "缺LocalBusiness结构化数据", "Your site has no LocalBusiness schema markup to connect it with your Google profile.", 8, false),
	"not_mobile":       d("not_mobile", "非移动端适配(无viewport)", "Your site isn't mobile-friendly (no viewport tag); most local searches happen on phones.", 8, false),
}

// GMapsOnlyKeys lists points skipped for OSM-only leads.
func GMapsOnlyKeys() []string {
	var out []string
	for k, v := range Catalog {
		if v.NeedsGMaps {
			out = append(out, k)
		}
	}
	return out
}

var genericCats = map[string]bool{"restaurant": true, "store": true, "shop": true, "cafe": true, "bar": true, "business": true, "service": true, "point of interest": true, "establishment": true, "food": true}

// IsGeneric reports whether a single category is too broad.
func IsGeneric(c string) bool { return genericCats[strings.ToLower(strings.TrimSpace(c))] }

// Reachability sets Reachable, BestEmail, SameDomain (emails pre-ranked, same-domain first).
func Reachability(b *model.Business, domain string) {
	b.Reachable, b.BestEmail, b.SameDomain, b.EmailType = false, "", false, ""
	b.EmailTypes = map[string]string{}
	for _, e := range b.Emails {
		b.EmailTypes[e] = emailtype.Classify(e)
	}
	for _, e := range b.Emails {
		if b.EmailStatus != nil && b.EmailStatus[e] == "invalid" {
			continue
		}
		b.Reachable = true
		b.BestEmail = e
		b.EmailType = b.EmailTypes[e]
		at := strings.LastIndex(e, "@")
		b.SameDomain = domain != "" && at >= 0 && (e[at+1:] == domain || strings.HasSuffix(e[at+1:], "."+domain))
		return
	}
}

// Compute fills Points and GMBScore. Unreachable leads get score 0 (not ranked).
func Compute(b *model.Business) {
	b.Points, b.GMBScore = nil, 0
	gm := b.HasSource("gmaps")
	b.NeedsGMaps = !gm
	if !b.Reachable {
		b.SEOScore, b.ScoreReasons = 0, nil
		return
	}
	add := func(k string) { p := Catalog[k].Point; b.Points = append(b.Points, p); b.GMBScore += p.Weight }
	if gm {
		if b.ReviewCount < MinReviews {
			add("few_reviews")
		}
		if b.Rating > 0 && b.Rating < MinRating {
			add("low_rating")
		}
		if !b.HasHours {
			add("no_hours")
		}
		if !b.OwnerKnown {
			add("unclaimed")
		}
		if b.PhotosKnown && b.PhotoCount < MinPhotos {
			add("few_photos")
		}
		cats := b.Categories
		if len(cats) == 0 && b.Category != "" {
			cats = []string{b.Category}
		}
		if len(cats) <= 1 && (len(cats) == 0 || IsGeneric(cats[0])) {
			add("generic_category")
		}
		if strings.TrimSpace(b.Description) == "" {
			add("no_description")
		}
		if b.Website == "" {
			add("no_site_on_maps")
		}
		if len(b.NAPIssues) > 0 {
			add("nap_mismatch")
		}
		if b.MapsRank == -1 || b.MapsRank > 3 {
			p := Catalog["not_top3"].Point
			kw := b.RankKeyword
			if kw == "" {
				kw = b.Category
			}
			p.PitchEN = fmt.Sprintf(p.PitchEN, kw)
			b.Points = append(b.Points, p)
			b.GMBScore += p.Weight
		}
	}
	if b.Website != "" && b.SiteReached {
		if !b.LinksToMaps && !b.HasMapEmbed {
			add("site_no_maps")
		}
		if !b.HTTPS {
			add("no_https")
		}
		if b.LoadMS > SlowMS {
			add("slow_site")
		}
		if !b.HasTitle || !b.HasMetaDesc {
			add("no_title_meta")
		}
		if !b.HasLBSchema {
			add("no_schema")
		}
		if !b.HasViewport {
			add("not_mobile")
		}
	}
	b.SEOScore = b.GMBScore
	b.ScoreReasons = nil
	for _, p := range b.Points {
		b.ScoreReasons = append(b.ScoreReasons, p.LabelZH)
	}
}

// Less orders reachable first, then email tier (personal>named>generic), then score desc, then same-domain email.
func Less(a, b model.Business) bool {
	if a.Reachable != b.Reachable {
		return a.Reachable
	}
	if ta, tb := emailtype.Tier(a.EmailType), emailtype.Tier(b.EmailType); a.Reachable && ta != tb {
		return ta < tb
	}
	if a.GMBScore != b.GMBScore {
		return a.GMBScore > b.GMBScore
	}
	return a.SameDomain && !b.SameDomain
}

// Rerank re-classifies emails (for leads saved by older versions) and re-sorts.
func Rerank(bs []model.Business, domainOf func(string) string) {
	for i := range bs {
		d := domainOf(bs[i].Website)
		bs[i].Emails = emailtype.Rank(bs[i].Emails, d)
		Reachability(&bs[i], d)
	}
	sort.SliceStable(bs, func(i, j int) bool { return Less(bs[i], bs[j]) })
}
