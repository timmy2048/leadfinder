package gmaps

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ReleaseAssetName returns the gosom release asset suffix for this OS.
// gosom publishes amd64 builds only; Apple Silicon runs darwin-amd64 via Rosetta 2.
func ReleaseAssetName(goos string) (string, error) {
	switch goos {
	case "linux":
		return "linux-amd64", nil
	case "darwin":
		return "darwin-amd64", nil
	case "windows":
		return "windows-amd64.exe", nil
	}
	return "", fmt.Errorf("no gosom release for %s", goos)
}

// Download fetches the latest gosom/google-maps-scraper release binary into dir
// (skips if already present) and returns its path.
func Download(ctx context.Context, dir string) (string, error) {
	suffix, err := ReleaseAssetName(runtime.GOOS)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := "google-maps-scraper"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dst := filepath.Join(dir, name)
	if _, err := os.Stat(dst); err == nil {
		return dst, nil
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/gosom/google-maps-scraper/releases/latest", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	var rel struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	err = json.NewDecoder(resp.Body).Decode(&rel)
	resp.Body.Close()
	if err != nil {
		return "", err
	}
	var url string
	for _, a := range rel.Assets {
		if strings.HasSuffix(a.Name, suffix) {
			url = a.URL
		}
	}
	if url == "" {
		return "", fmt.Errorf("asset *%s not found in release %s", suffix, rel.TagName)
	}
	log.Printf("downloading gosom %s (%s)...", rel.TagName, suffix)
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download status %d", resp.StatusCode)
	}
	tmp := dst + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return "", err
	}
	f.Close()
	return dst, os.Rename(tmp, dst)
}

// Launch starts gosom in web mode as a child process on addr (e.g.
// 127.0.0.1:8081) and waits until its API answers. gosom installs the
// Playwright browser automatically on first run (can take several minutes).
func Launch(ctx context.Context, bin, dataDir, addr string) (*exec.Cmd, string, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, "", err
	}
	cmd := exec.CommandContext(ctx, bin, "-web", "-data-folder", dataDir, "-addr", addr)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, "", err
	}
	base := "http://" + addr
	deadline := time.Now().Add(15 * time.Minute)
	for time.Now().Before(deadline) {
		if r, err := http.Get(base + "/api/v1/jobs"); err == nil {
			r.Body.Close()
			return cmd, base, nil
		}
		select {
		case <-ctx.Done():
			return cmd, "", ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	_ = cmd.Process.Kill()
	return nil, "", fmt.Errorf("gosom did not become ready on %s", addr)
}
