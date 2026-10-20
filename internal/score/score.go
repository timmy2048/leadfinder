// Package score computes an SEO-need score (0-100, higher = more need).
package score

import "github.com/timmy2048/leadfinder/internal/model"

// Compute fills SEOScore and ScoreReasons.
func Compute(b *model.Business) {
	s := 0
	var r []string
	add := func(n int, why string) { s += n; r = append(r, why) }
	if b.Website == "" {
		add(45, "无网站")
	} else if !b.SiteReached {
		add(35, "网站无法访问")
	} else {
		if !b.HTTPS {
			add(15, "无HTTPS")
		}
		if b.LoadMS > 3000 {
			add(15, "网站慢(>3s)")
		} else if b.LoadMS > 1500 {
			add(7, "网站较慢(>1.5s)")
		}
		if !b.HasTitle {
			add(10, "缺title")
		}
		if !b.HasMetaDesc {
			add(10, "缺meta描述")
		}
	}
	hasReviews := false
	for _, src := range b.Sources {
		if src == "gmaps" {
			hasReviews = true
		}
	}
	if hasReviews {
		switch {
		case b.ReviewCount < 10:
			add(20, "评论很少(<10)")
		case b.ReviewCount < 50:
			add(10, "评论较少(<50)")
		}
		if b.Rating > 0 && b.Rating < 4.0 {
			add(5, "评分<4.0")
		}
	}
	if s > 100 {
		s = 100
	}
	b.SEOScore, b.ScoreReasons = s, r
}
