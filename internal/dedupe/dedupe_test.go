package dedupe

import (
	"testing"

	"github.com/timmy2048/leadfinder/internal/model"
)

func TestMerge(t *testing.T) {
	in := []model.Business{
		{Name: "Joe's Pizza", Website: "https://www.joes.com/", Sources: []string{"osm"}, Lat: 51.38, Lon: -2.36},
		{Name: "Joes Pizza Bath", Website: "http://joes.com/menu", Phone: "+44 1225 123456", Sources: []string{"gmaps"}, ReviewCount: 40},
		{Name: "Other", Phone: "01225 123456"},
		{Name: "Café Rouge", Lat: 51.3800, Lon: -2.3600},
		{Name: "cafe rouge", Lat: 51.3802, Lon: -2.3601, Website: "https://facebook.com/caferouge"},
		{Name: "Far", Lat: 52, Lon: -2},
	}
	out := Merge(in)
	if len(out) != 3 {
		for _, b := range out {
			t.Log(b.Name)
		}
		t.Fatalf("want 3 got %d", len(out))
	}
	if out[0].Phone == "" || out[0].ReviewCount != 40 || len(out[0].Sources) != 2 {
		t.Fatalf("merge fill failed: %+v", out[0])
	}
}

func TestDomain(t *testing.T) {
	for in, want := range map[string]string{"https://www.A.com/x": "a.com", "b.org": "b.org", "https://instagram.com/x": "", "": ""} {
		if got := Domain(in); got != want {
			t.Errorf("%s: %s", in, got)
		}
	}
}
