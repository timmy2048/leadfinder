package osm

import (
	"strings"
	"testing"
)

func TestBuildQuery(t *testing.T) {
	q := BuildQuery("restaurants", 1, 2, 500, 5)
	if !strings.Contains(q, `nwr["amenity"="restaurant"](around:500,1.000000,2.000000)`) || !strings.Contains(q, "out center tags 15") {
		t.Fatal(q)
	}
	if !strings.Contains(BuildQuery("shop=bakery", 1, 2, 1, 1), `"shop"="bakery"`) {
		t.Fatal("raw tag")
	}
}

func TestBuildQueryBBox(t *testing.T) {
	q := BuildQueryBBox("cafe", 31.1, 121.3, 31.2, 121.4, 10)
	if !strings.Contains(q, `nwr["amenity"="cafe"](31.100000,121.300000,31.200000,121.400000)`) {
		t.Fatal(q)
	}
}

func TestParse(t *testing.T) {
	body := `{"elements":[{"type":"node","lat":1,"lon":2,"tags":{"name":"A","amenity":"restaurant","website":"https://a.com","contact:email":"X@a.com;y@a.com","phone":"123","contact:instagram":"https://instagram.com/a"}},
	{"type":"way","center":{"lat":3,"lon":4},"tags":{"name":"B"}},{"type":"node","tags":{}}]}`
	bs, err := ParseResponse([]byte(body), "restaurant", 10)
	if err != nil || len(bs) != 2 || len(bs[0].Emails) != 2 || bs[0].Emails[0] != "x@a.com" || bs[1].Lat != 3 || len(bs[0].Socials) != 1 {
		t.Fatalf("%v %+v", err, bs)
	}
}
