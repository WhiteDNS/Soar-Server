package panel

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestPanelBrandingAndVersionBeforeLogin(t *testing.T) {
	original := Version
	t.Cleanup(func() { Version = original })
	h := NewHTTP(nil, nil, false)
	versions := regexp.MustCompile(`data-panel-version[^>]*>([^<]+)`)
	for _, version := range []string{"7.8.9", "8.0.0-beta2"} {
		Version = version
		for _, path := range []string{"/", "/index.html"} {
			response := httptest.NewRecorder()
			h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("%s: status=%d headers=%v", path, response.Code, response.Header())
			}
			body := response.Body.String()
			if strings.Contains(body, "{{PANEL_") || !strings.Contains(body, "<title>"+ProductName+"</title>") {
				t.Fatal("product metadata was not rendered before authentication")
			}
			matches := versions.FindAllStringSubmatch(body, -1)
			if len(matches) != 5 {
				t.Fatalf("want login, sidebar, dashboard, update and footer versions; got %d", len(matches))
			}
			for _, match := range matches {
				if match[1] != version {
					t.Fatalf("stale displayed version: got %q want %q", match[1], version)
				}
			}
			for _, stale := range []string{"WhiteDNS Soar Panel", "Soar Panel</title>", "Lantern", "سرور Soar"} {
				if strings.Contains(body, stale) {
					t.Fatalf("stale panel branding %q", stale)
				}
			}
		}
	}
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest(http.MethodHead, "/", nil))
	if response.Code != http.StatusOK || response.Body.Len() != 0 {
		t.Fatal("HEAD must retain an empty response body")
	}
}
