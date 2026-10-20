// Package jsrender renders JS-heavy (SPA) pages with a local headless
// Chrome/Chromium (`--headless --dump-dom`). No extra Go dependency; used
// only as a fallback when static HTML yields no email. Set LF_CHROME to
// override the browser path.
package jsrender

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"time"
)

var sem = make(chan struct{}, 2)

// Browser returns the chrome binary path or "".
func Browser() string {
	if p := os.Getenv("LF_CHROME"); p != "" {
		return p
	}
	for _, n := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome",
		`C:\Program Files\Google\Chrome\Application\chrome.exe`, "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}

// Render returns the post-JS DOM of url (max ~25s).
func Render(ctx context.Context, url string) (string, error) {
	bin := Browser()
	if bin == "" {
		return "", errors.New("no chrome/chromium found (set LF_CHROME)")
	}
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-sem }()
	tctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	dir, _ := os.MkdirTemp("", "lfchrome")
	defer os.RemoveAll(dir)
	cmd := exec.CommandContext(tctx, bin, "--headless=new", "--disable-gpu", "--no-sandbox", "--no-first-run", "--blink-settings=imagesEnabled=false",
		"--user-data-dir="+dir, "--virtual-time-budget=6000", "--timeout=20000", "--dump-dom", url)
	out, err := cmd.Output()
	if len(out) > 0 {
		return string(out), nil
	}
	return "", err
}
