package importer

import (
	"strings"
	"testing"
)

func TestParseOutscraper(t *testing.T) {
	in := "\ufeffname,full_address,phone,site,rating,reviews,email_1,email_2\nJoe's Cafe,\"1 Main St, Tampa\",+1 813-555-0100,joescafe.com,4.6,\"1,234\",joe@joescafe.com,info@joescafe.com\n"
	bs, err := Parse(strings.NewReader(in))
	if err != nil || len(bs) != 1 {
		t.Fatal(err, bs)
	}
	b := bs[0]
	if b.Website != "http://joescafe.com" || b.ReviewCount != 1234 || b.Rating != 4.6 || len(b.Emails) != 2 || b.EmailSources["joe@joescafe.com"] != "import" {
		t.Fatalf("%+v", b)
	}
}

func TestParseApify(t *testing.T) {
	in := "title,totalScore,reviewsCount,website,phone,address,categoryName,emails/0\nBrew,4.2,10,https://brew.com,123,Addr,Coffee shop,a@brew.com\n"
	bs, err := Parse(strings.NewReader(in))
	if err != nil || len(bs) != 1 || bs[0].Name != "Brew" || bs[0].ReviewCount != 10 || len(bs[0].Emails) != 1 || bs[0].Category != "Coffee shop" {
		t.Fatal(err, bs)
	}
}
