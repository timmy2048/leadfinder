package social

import "testing"

func TestFBVariants(t *testing.T) {
	v := FBVariants("https://www.facebook.com/joescafe/")
	if len(v) != 3 || v[0] != "https://m.facebook.com/joescafe/about" || v[1] != "https://mbasic.facebook.com/joescafe/about" {
		t.Fatal(v)
	}
}
