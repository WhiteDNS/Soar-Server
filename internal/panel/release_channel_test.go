package panel

import "testing"

func releaseFixture(version string, prerelease bool) PanelRelease {
	r := PanelRelease{Tag: "panel-v" + version, URL: "https://github.com/WhiteDNS/Soar-Server/releases/tag/panel-v" + version, Prerelease: prerelease}
	for _, suffix := range []string{".tar.gz", ".tar.gz.sha256"} {
		name := "soar-panel-" + version + "-linux-amd64" + suffix
		r.Assets = append(r.Assets, ReleaseAsset{Name: name, URL: "https://github.com/WhiteDNS/Soar-Server/releases/download/" + r.Tag + "/" + name, Size: 100})
	}
	return r
}

func TestStableUpdateChannelSkipsBetasAndDrafts(t *testing.T) {
	draft := releaseFixture("0.5.0", false)
	draft.Draft = true
	missing := releaseFixture("0.6.0", false)
	missing.Assets = nil
	releases := []PanelRelease{releaseFixture("0.2.1", false), releaseFixture("0.3.0-beta1", true), releaseFixture("0.3.0-beta2", false), releaseFixture("0.4.0", true), draft, missing}
	r, err := selectPanelRelease(releases, "0.2.0")
	if err != nil || r.Tag != "panel-v0.2.1" {
		t.Fatal("stable update selected an experimental or incomplete release", r.Tag, err)
	}
}

func TestBetaCanGraduateToStable(t *testing.T) {
	r, err := selectPanelRelease([]PanelRelease{releaseFixture("0.2.0-beta10", true), releaseFixture("0.2.0", false)}, "0.2.0-beta10")
	if err != nil || r.Tag != "panel-v0.2.0" || !NewerVersion("0.2.0", "0.2.0-beta10") {
		t.Fatal("beta did not graduate to stable", r.Tag, err)
	}

	r, err = selectPanelRelease([]PanelRelease{releaseFixture("0.3.0-beta1", true)}, "0.2.0-beta10")
	if err != nil || r.Tag != "panel-v0.3.0-beta1" {
		t.Fatal("existing beta channel changed", r.Tag, err)
	}
}
