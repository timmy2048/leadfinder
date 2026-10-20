package rdap

import "testing"

func TestClean(t *testing.T) {
	if Clean("pw-abc@privacyguardian.org") || Clean("abuse@godaddy.com") || !Clean("owner@joescafe.com") {
		t.Fatal()
	}
}
