package score

import (
	"sort"
	"testing"

	"github.com/timmy2048/leadfinder/internal/model"
)

func keys(t *testing.T, b model.Business) map[string]bool {
	m := map[string]bool{}
	for _, p := range b.Points {
		m[p.Key] = true
		if p.LabelZH == "" || p.PitchEN == "" || p.Weight <= 0 {
			t.Fatalf("bad point %+v", p)
		}
	}
	return m
}

func TestReachability(t *testing.T) {
	b := model.Business{Emails: []string{"a@x.com", "b@gmail.com"}, EmailStatus: map[string]string{"a@x.com": "invalid"}}
	Reachability(&b, "x.com")
	if !b.Reachable || b.BestEmail != "b@gmail.com" || b.SameDomain {
		t.Fatalf("%+v", b)
	}
	c := model.Business{Emails: []string{"info@shop.x.com"}}
	Reachability(&c, "x.com")
	if !c.SameDomain {
		t.Fatal("subdomain should be same domain")
	}
	u := model.Business{Sources: []string{"gmaps"}, ReviewCount: 1}
	Reachability(&u, "")
	Compute(&u)
	if u.Reachable || u.GMBScore != 0 || len(u.Points) != 0 {
		t.Fatalf("unreachable must not be scored: %+v", u)
	}
}

func TestGMapsLeadAllPoints(t *testing.T) {
	b := model.Business{Sources: []string{"gmaps"}, Reachable: true, ReviewCount: 3, Rating: 3.9, Category: "Restaurant", PhotosKnown: true, PhotoCount: 2,
		Website: "http://x.com", SiteReached: true, LoadMS: 4000, NAPIssues: []string{"phone"}}
	Compute(&b)
	k := keys(t, b)
	for _, want := range []string{"unclaimed", "few_reviews", "low_rating", "no_hours", "few_photos", "generic_category", "no_description", "nap_mismatch", "site_no_maps", "no_https", "slow_site", "no_title_meta", "no_schema", "not_mobile"} {
		if !k[want] {
			t.Errorf("missing %s", want)
		}
	}
	if k["no_site_on_maps"] || b.NeedsGMaps {
		t.Error("unexpected")
	}
	sum := 0
	for _, p := range b.Points {
		sum += p.Weight
	}
	if b.GMBScore != sum || b.SEOScore != sum {
		t.Fatal("score must equal weight sum")
	}
}

func TestNoWebsiteOnProfile(t *testing.T) {
	b := model.Business{Sources: []string{"gmaps"}, Reachable: true, ReviewCount: 100, Rating: 4.5, HasHours: true, PhotoCount: 20, PhotosKnown: true, OwnerKnown: true, Description: "x", Categories: []string{"Sushi restaurant"}}
	Compute(&b)
	if k := keys(t, b); !k["no_site_on_maps"] || len(k) != 1 {
		t.Fatal(k)
	}
}

func TestPerfectGMapsLead(t *testing.T) {
	b := model.Business{Sources: []string{"gmaps"}, Reachable: true, ReviewCount: 500, Rating: 4.7, HasHours: true, PhotoCount: 80, PhotosKnown: true, OwnerKnown: true,
		Categories: []string{"Italian restaurant", "Pizza restaurant"}, Description: "Wood-fired pizza", Website: "https://x.com",
		SiteReached: true, HTTPS: true, LoadMS: 500, HasTitle: true, HasMetaDesc: true, HasLBSchema: true, HasViewport: true, LinksToMaps: true}
	Compute(&b)
	if b.GMBScore != 0 {
		t.Fatalf("%+v", b.Points)
	}
}

func TestOSMOnlySkipsMapsPoints(t *testing.T) {
	b := model.Business{Sources: []string{"osm"}, Reachable: true, Website: "https://x.com", SiteReached: true, HTTPS: true, HasTitle: true, HasMetaDesc: true}
	Compute(&b)
	k := keys(t, b)
	if !b.NeedsGMaps {
		t.Fatal("should flag needs gmaps")
	}
	for _, g := range GMapsOnlyKeys() {
		if k[g] {
			t.Errorf("osm lead got gmaps-only point %s", g)
		}
	}
	if !k["no_schema"] || !k["not_mobile"] || !k["site_no_maps"] || len(k) != 3 {
		t.Fatalf("%v", k)
	}
}

func TestSortReachableFirst(t *testing.T) {
	bs := []model.Business{{Name: "u"}, {Name: "lo", Reachable: true, GMBScore: 5}, {Name: "hi", Reachable: true, GMBScore: 50}}
	sort.SliceStable(bs, func(i, j int) bool { return Less(bs[i], bs[j]) })
	if bs[0].Name != "hi" || bs[1].Name != "lo" || bs[2].Name != "u" {
		t.Fatal(bs)
	}
}

func TestUnknownPhotosNotPenalised(t *testing.T) {
	b := model.Business{Sources: []string{"gmaps"}, Reachable: true, ReviewCount: 100, Rating: 4.5, HasHours: true, OwnerKnown: true, Description: "x", Categories: []string{"Sushi restaurant"}, Website: "x"}
	Compute(&b)
	if keys(t, b)["few_photos"] {
		t.Fatal("photos unknown must not be flagged")
	}
}

func TestSortTierBeforeScore(t *testing.T) {
	bs := []model.Business{
		{Name: "generic", Reachable: true, GMBScore: 50, EmailType: "generic"},
		{Name: "named", Reachable: true, GMBScore: 20, EmailType: "named"},
		{Name: "personal", Reachable: true, GMBScore: 5, EmailType: "personal"},
		{Name: "personal2", Reachable: true, GMBScore: 30, EmailType: "personal"},
	}
	sort.SliceStable(bs, func(i, j int) bool { return Less(bs[i], bs[j]) })
	got := []string{bs[0].Name, bs[1].Name, bs[2].Name, bs[3].Name}
	if got[0] != "personal2" || got[1] != "personal" || got[2] != "named" || got[3] != "generic" {
		t.Fatal(got)
	}
}

func TestReachabilityPrefersPersonal(t *testing.T) {
	b := model.Business{Emails: []string{"info@shop.com", "mike@shop.com"}}
	Rerank([]model.Business{b}, func(string) string { return "shop.com" })
	bs := []model.Business{b}
	Rerank(bs, func(string) string { return "shop.com" })
	if bs[0].BestEmail != "mike@shop.com" || bs[0].EmailType != "personal" || len(bs[0].Emails) != 2 {
		t.Fatal(bs[0])
	}
}

func TestGuessedNeverPrimary(t *testing.T) {
	b := model.Business{Emails: []string{"maria@shop.test"}, EmailStatus: map[string]string{"maria@shop.test": "guessed"}}
	Reachability(&b, "shop.test")
	if b.Reachable || b.BestEmail != "" {
		t.Fatal(b.BestEmail)
	}
}
