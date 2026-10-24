package panel

import "testing"

func releaseFixture(version string, prerelease bool) PanelRelease {
	r := PanelRelease{Tag: "panel-v" + version, URL: "https://github.com/WhiteDNS/Soar-Server/releases/tag/panel-v" + version, Prerelease: prerelease}
	for _, arch := range []string{"amd64", "arm64"} {
		for _, suffix := range []string{".tar.gz", ".tar.gz.sha256"} {
			name := "soar-panel-" + version + "-linux-" + arch + suffix
			r.Assets = append(r.Assets, ReleaseAsset{Name: name, URL: "https://github.com/WhiteDNS/Soar-Server/releases/download/" + r.Tag + "/" + name, Size: 100})
		}
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

func TestReleaseRequiresAssetsForTargetArchitecture(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			r := releaseFixture("0.7.0", false)
			if e := validatePanelReleaseArch(r, "0.7.0", arch); e != nil {
				t.Fatal(e)
			}
			archive, e := PanelArchiveName("0.7.0", arch)
			if e != nil || archive != "soar-panel-0.7.0-linux-"+arch+".tar.gz" {
				t.Fatal("incorrect target archive", archive, e)
			}
			assets := []ReleaseAsset{}
			for _, a := range r.Assets {
				if a.Name != archive && a.Name != archive+".sha256" {
					assets = append(assets, a)
				}
			}
			r.Assets = assets
			if e := validatePanelReleaseArch(r, "0.7.0", arch); e == nil {
				t.Fatal("other architecture's assets accepted")
			}
			original := releaseFixture("0.7.0", false)
			for _, a := range original.Assets {
				if a.Name == archive {
					r.Assets = append(r.Assets, a, a)
				}
			}
			if e := validatePanelReleaseArch(r, "0.7.0", arch); e == nil {
				t.Fatal("duplicate archive substituted for missing checksum")
			}
		})
	}
	if _, e := PanelArchiveName("0.7.0", "ppc64le"); e == nil {
		t.Fatal("unsupported architecture accepted")
	}
}
