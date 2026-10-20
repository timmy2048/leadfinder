// Package commoncrawl looks up archived contact pages of a domain in the
// free Common Crawl index and extracts emails from the WARC records.
// Used when the live site is down or yields nothing.
package commoncrawl

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"

	"github.com/timmy2048/leadfinder/internal/crawl"
	"github.com/timmy2048/leadfinder/internal/fetch"
)

var (
	idxOnce sync.Once
	idxAPI  string
)

func latest(ctx context.Context, f *fetch.Client) string {
	idxOnce.Do(func() {
		r, err := f.Get(ctx, "https://index.commoncrawl.org/collinfo.json", nil)
		if err != nil || r.Status != 200 {
			return
		}
		var cs []struct {
			CDX string `json:"cdx-api"`
		}
		if json.Unmarshal(r.Body, &cs) == nil && len(cs) > 0 {
			idxAPI = cs[0].CDX
		}
	})
	return idxAPI
}

type rec struct {
	URL      string `json:"url"`
	Filename string `json:"filename"`
	Offset   string `json:"offset"`
	Length   string `json:"length"`
	Status   string `json:"status"`
}

// Emails returns emails found in up to maxRecords archived pages (contact-like first).
func Emails(ctx context.Context, f *fetch.Client, domain string, maxRecords int) map[string]string {
	out := map[string]string{}
	api := latest(ctx, f)
	if api == "" || domain == "" {
		return out
	}
	q := api + "?output=json&limit=200&filter=status:200&filter=mime:text/html&matchType=domain&url=" + url.QueryEscape(domain)
	r, err := f.Get(ctx, q, nil)
	if err != nil || r.Status != 200 {
		return out
	}
	var contact, home []rec
	sc := bufio.NewScanner(bytes.NewReader(r.Body))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var x rec
		if json.Unmarshal(sc.Bytes(), &x) != nil {
			continue
		}
		u, _ := url.Parse(x.URL)
		if u != nil && (u.Path == "" || u.Path == "/") {
			home = append(home, x)
		} else if crawl.IsContactLink(x.URL, "") {
			contact = append(contact, x)
		}
	}
	picks := append(contact, home...)
	if len(picks) > maxRecords {
		picks = picks[:maxRecords]
	}
	for _, x := range picks {
		body, err := record(ctx, f, x)
		if err != nil {
			continue
		}
		u, _ := url.Parse(x.URL)
		for _, e := range crawl.ExtractEmails(body) {
			if _, ok := out[e]; !ok {
				out[e] = "commoncrawl:" + u.Path
			}
		}
	}
	return out
}

func record(ctx context.Context, f *fetch.Client, x rec) (string, error) {
	var off, ln int64
	fmt.Sscan(x.Offset, &off)
	fmt.Sscan(x.Length, &ln)
	if ln <= 0 || ln > 5<<20 {
		return "", fmt.Errorf("bad length")
	}
	r, err := f.Get(ctx, "https://data.commoncrawl.org/"+x.Filename, map[string]string{"Range": fmt.Sprintf("bytes=%d-%d", off, off+ln-1)})
	if err != nil {
		return "", err
	}
	if r.Status != 206 && r.Status != 200 {
		return "", fmt.Errorf("http %d", r.Status)
	}
	zr, err := gzip.NewReader(bytes.NewReader(r.Body))
	if err != nil {
		return "", err
	}
	b, err := io.ReadAll(io.LimitReader(zr, 3<<20))
	s := string(b)
	if i := strings.Index(s, "\r\n\r\n"); i > 0 { // skip WARC header
		s = s[i:]
	}
	return s, err
}
