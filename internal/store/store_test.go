package store

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/timmy2048/leadfinder/internal/model"
)

func TestStore(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := s.CreateJob(model.JobParams{Category: "c", Location: "l"})
	_ = s.SaveBusinesses(id, []model.Business{{Name: "A", SEOScore: 10, Emails: []string{"a@a.com"}, EmailStatus: map[string]string{"a@a.com": "valid"}}, {Name: "B", SEOScore: 50}})
	_ = s.UpdateJob(id, "done", "ok", "")
	j, _ := s.GetJob(id)
	bs, _ := s.Businesses(id)
	if j.Count != 2 || j.Params.Category != "c" || bs[0].Name != "B" {
		t.Fatalf("%+v %+v", j, bs)
	}
	var buf bytes.Buffer
	_ = WriteCSV(&buf, bs)
	if !strings.Contains(buf.String(), "a@a.com:valid") {
		t.Fatal(buf.String())
	}
}
