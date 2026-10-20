package score

import (
	"testing"

	"github.com/timmy2048/leadfinder/internal/model"
)

func TestScore(t *testing.T) {
	a := model.Business{Sources: []string{"gmaps"}, ReviewCount: 3}
	Compute(&a)
	b := model.Business{Website: "https://x.com", SiteReached: true, HTTPS: true, HasTitle: true, HasMetaDesc: true, LoadMS: 300, Sources: []string{"gmaps"}, ReviewCount: 500, Rating: 4.8}
	Compute(&b)
	if a.SEOScore != 65 || b.SEOScore != 0 {
		t.Fatal(a.SEOScore, b.SEOScore, a.ScoreReasons)
	}
}
