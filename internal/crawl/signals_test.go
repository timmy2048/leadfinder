package crawl

import "testing"

func TestSignals(t *testing.T) {
	s := Signals(`<meta name="viewport" content="width=device-width"><script type="application/ld+json">{"@type": "Restaurant"}</script><iframe src="https://www.google.com/maps/embed?pb=1"></iframe>`)
	if !s.Viewport || !s.Schema || !s.MapEmbed || !s.MapsLink {
		t.Fatalf("%+v", s)
	}
	if n := Signals(`<html><a href="https://maps.app.goo.gl/x">map</a></html>`); n.Viewport || n.Schema || n.MapEmbed || !n.MapsLink {
		t.Fatalf("%+v", n)
	}
}

func TestNAP(t *testing.T) {
	text := "joe's pizza 123 main st call (305) 555-1234"
	if got := NAPIssues("Joe's Pizza", "123 Main St, Miami, FL", "+1 305-555-1234", text); len(got) != 0 {
		t.Fatal(got)
	}
	got := NAPIssues("Joe's Pizza", "999 Ocean Dr, Miami", "+1 305-555-9999", text)
	if len(got) != 2 || got[0] != "phone" || got[1] != "address" {
		t.Fatal(got)
	}
	if NAPIssues("x", "", "", "") != nil {
		t.Fatal("empty text -> no judgement")
	}
}
