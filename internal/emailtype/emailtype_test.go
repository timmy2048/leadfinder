package emailtype

import (
	"reflect"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := map[string]string{
		"info@kahwacoffee.com":             Generic,
		"hello@maraburestaurant.com":       Generic,
		"reservations@lafayette-miami.com": Generic,
		"Sales@x.com":                      Generic,
		"events2@x.com":                    Generic,
		"info.miami@x.com":                 Generic,
		"info@gmail.com":                   Generic,
		"micafecitofl@gmail.com":           Personal,
		"someone@hotmail.com":              Personal,
		"john@acme.com":                    Personal,
		"j.smith@acme.com":                 Personal,
		"maria.lopez@acme.com":             Personal,
		"johnsmith@acme.com":               Personal,
		"columbiacafe@crg1905.com":         Named,
		"piolabrickell@piolausa.com":       Named,
		"email@barilochegourmet.com":       Generic,
		"miam@miamcafe.com":                Named,
	}
	for e, want := range cases {
		if got := Classify(e); got != want {
			t.Errorf("%s: got %s want %s", e, got, want)
		}
	}
}

func TestRank(t *testing.T) {
	in := []string{"info@a.com", "shop@a.com", "bob@gmail.com", "x@other.com", "mike@a.com"}
	got := Rank(in, "a.com")
	want := []string{"mike@a.com", "bob@gmail.com", "x@other.com", "info@a.com", "shop@a.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
