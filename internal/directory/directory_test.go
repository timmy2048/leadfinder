package directory

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func doc(s string) *goquery.Document {
	d, _ := goquery.NewDocumentFromReader(strings.NewReader(s))
	return d
}

func TestParseYP(t *testing.T) {
	bs := ParseYP(doc(`<div class="result"><div class="info"><a class="business-name">Cafe One</a><div class="phones">(813) 555-0100</div><a class="track-visit-website" href="https://cafeone.com">Website</a><div class="street-address">1 Main</div><div class="locality">Tampa, FL</div></div></div>`), "cafe")
	if len(bs) != 1 || bs[0].Website != "https://cafeone.com" || bs[0].Phone == "" {
		t.Fatal(bs)
	}
}

func TestParseChamber(t *testing.T) {
	bs := ParseChamber(doc(`<div class="gz-list-card-wrapper"><h5 class="gz-card-title">Bean Co</h5><li class="gz-card-phone">813-555-0101</li><li class="gz-card-website"><a href="https://bean.co">Visit Website</a></li></div>`), "cafe")
	if len(bs) != 1 || bs[0].Name != "Bean Co" || bs[0].Website != "https://bean.co" {
		t.Fatal(bs)
	}
}

func TestParseDetailYelpRedirect(t *testing.T) {
	raw := `<h1>Joe Cafe</h1><a href="tel:+18135550199">call</a><a href="/biz_redir?url=https%3A%2F%2Fjoecafe.com%2F&amp;x=1">site</a>`
	b := ParseDetail(doc(raw), raw, "https://www.yelp.com")
	if b.Name != "Joe Cafe" || b.Website != "https://joecafe.com/" || b.Phone != "+18135550199" {
		t.Fatalf("%+v", b)
	}
}
