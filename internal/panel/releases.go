package panel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const ProductName = "WhiteDNS Server Management"

var Version = "0.8.2"
var panelVersionPattern = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(?:-beta(\d+))?$`)

func ValidVersion(v string) bool { return panelVersionPattern.MatchString(v) && len(v) < 48 }
func NewerVersion(a, b string) bool {
	x, y := panelVersionPattern.FindStringSubmatch(a), panelVersionPattern.FindStringSubmatch(b)
	if x == nil || y == nil {
		return false
	}
	for i := 1; i <= 3; i++ {
		u, e := strconv.Atoi(x[i])
		if e != nil {
			return false
		}
		v, e := strconv.Atoi(y[i])
		if e != nil {
			return false
		}
		if u != v {
			return u > v
		}
	}
	if x[4] == "" {
		return y[4] != ""
	}
	if y[4] == "" {
		return false
	}
	u, _ := strconv.Atoi(x[4])
	v, _ := strconv.Atoi(y[4])
	return u > v
}

type ReleaseAsset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}
type PanelRelease struct {
	Tag        string         `json:"tag_name"`
	Name       string         `json:"name"`
	URL        string         `json:"html_url"`
	Draft      bool           `json:"draft"`
	Prerelease bool           `json:"prerelease"`
	Assets     []ReleaseAsset `json:"assets"`
	Notes      string         `json:"body"`
}

func ReleaseClient() *http.Client {
	return &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 5 || r.URL.Scheme != "https" {
			return errors.New("unsafe release redirect")
		}
		switch r.URL.Hostname() {
		case "github.com", "api.github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
			return nil
		}
		return errors.New("unexpected release host")
	}}
}
func getReleaseJSON(ctx context.Context, path string, out any) error {
	req, e := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/repos/WhiteDNS/Soar-Server/"+path, nil)
	if e != nil {
		return e
	}
	req.Header.Set("User-Agent", "WhiteDNS-Server-Management/"+Version)
	req.Header.Set("Accept", "application/vnd.github+json")
	response, e := ReleaseClient().Do(req)
	if e != nil {
		return e
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("GitHub release check failed (HTTP %d); retry later", response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(out)
}
func ReleaseForVersion(ctx context.Context, version string) (PanelRelease, error) {
	var r PanelRelease
	if !ValidVersion(version) {
		return r, errors.New("invalid panel version")
	}
	e := getReleaseJSON(ctx, "releases/tags/panel-v"+version, &r)
	if e == nil {
		e = validatePanelRelease(r, version)
	}
	return r, e
}
func validatePanelRelease(r PanelRelease, v string) error {
	return validatePanelReleaseArch(r, v, runtime.GOARCH)
}

func PanelArchiveName(version, arch string) (string, error) {
	if !ValidVersion(version) || (arch != "amd64" && arch != "arm64") {
		return "", errors.New("unsupported panel version or architecture")
	}
	return "soar-panel-" + version + "-linux-" + arch + ".tar.gz", nil
}

func validatePanelReleaseArch(r PanelRelease, v, arch string) error {
	archive, e := PanelArchiveName(v, arch)
	if e != nil {
		return e
	}
	if r.Draft || r.Tag != "panel-v"+v || r.URL != "https://github.com/WhiteDNS/Soar-Server/releases/tag/"+r.Tag {
		return errors.New("invalid official panel release")
	}
	found := map[string]bool{}
	for _, a := range r.Assets {
		for _, name := range []string{archive, archive + ".sha256"} {
			if a.Name == name && a.URL == "https://github.com/WhiteDNS/Soar-Server/releases/download/"+r.Tag+"/"+name && a.Size > 0 && a.Size <= 96<<20 {
				found[name] = true
			}
		}
	}
	if len(found) != 2 {
		return errors.New("official panel bundle is not available")
	}
	return nil
}
func LatestPanelRelease(ctx context.Context) (PanelRelease, error) {
	var releases []PanelRelease
	if e := getReleaseJSON(ctx, "releases?per_page=50", &releases); e != nil {
		return PanelRelease{}, e
	}
	return selectPanelRelease(releases, Version)
}

func selectPanelRelease(releases []PanelRelease, current string) (PanelRelease, error) {
	var best PanelRelease
	version := ""
	for _, r := range releases {
		v := strings.TrimPrefix(r.Tag, "panel-v")
		// Stable installations stay on stable updates. Beta installations may
		// still discover their next beta or graduate to the final stable release.
		if !strings.Contains(current, "-") && (r.Prerelease || strings.Contains(v, "-")) {
			continue
		}
		if !ValidVersion(v) || validatePanelRelease(r, v) != nil {
			continue
		}
		if version == "" || NewerVersion(v, version) {
			best = r
			version = v
		}
	}
	if version == "" {
		return best, errors.New("no compatible panel release found")
	}
	return best, nil
}
