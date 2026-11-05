package panel

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

func releaseFixture(version string, prerelease bool) PanelRelease {
	r := PanelRelease{Tag: "panel-v" + version, URL: "https://github.com/WhiteDNS/Soar-Server/releases/tag/panel-v" + version, Prerelease: prerelease}
	for _, arch := range []string{"amd64", "arm64"} {
		for _, suffix := range []string{".tar.gz", ".tar.gz.sha256", ".tar.gz.sha256.sig"} {
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

func TestReleaseSignatureBindsChecksumContent(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if VerifyReleaseSignature([]byte("x"), ed25519.Sign(private, []byte("x"))) == nil {
		t.Fatal("signature from another key accepted")
	}
	original := releaseSigningKeys
	t.Cleanup(func() { releaseSigningKeys = original })
	releaseSigningKeys = []string{original[0], base64.StdEncoding.EncodeToString(public)}
	sums := []byte("abc  soar-panel-0.8.0-linux-amd64.tar.gz\n")
	signature := ed25519.Sign(private, sums)
	if e := VerifyReleaseSignature(sums, signature); e != nil {
		t.Fatal(e)
	}
	if VerifyReleaseSignature([]byte("abc  soar-panel-0.7.0-linux-amd64.tar.gz\n"), signature) == nil {
		t.Fatal("signature accepted for a different release")
	}
}

func TestReleaseWithOnlyWorkflowSignatureIsOffered(t *testing.T) {
	r := releaseFixture("0.8.0", false)
	for i := range r.Assets {
		if strings.HasSuffix(r.Assets[i].Name, ".sha256.sig") {
			r.Assets[i].Name = strings.TrimSuffix(r.Assets[i].Name, OfflineSignatureSuffix) + WorkflowSignatureSuffix
			r.Assets[i].URL = "https://github.com/WhiteDNS/Soar-Server/releases/download/" + r.Tag + "/" + r.Assets[i].Name
		}
	}
	selected, err := selectPanelRelease([]PanelRelease{releaseFixture("0.7.0", false), r}, "0.6.0")
	if err != nil || selected.Tag != "panel-v0.8.0" {
		t.Fatal("workflow-signed release was not offered", selected.Tag, err)
	}
}

func TestUnsignedReleaseIsNotOffered(t *testing.T) {
	unsigned := releaseFixture("0.8.0", false)
	assets := []ReleaseAsset{}
	for _, a := range unsigned.Assets {
		if !strings.HasSuffix(a.Name, ".sig") {
			assets = append(assets, a)
		}
	}
	unsigned.Assets = assets
	r, err := selectPanelRelease([]PanelRelease{releaseFixture("0.7.0", false), unsigned}, "0.6.0")
	if err != nil || r.Tag != "panel-v0.7.0" {
		t.Fatal("unsigned release was offered", r.Tag, err)
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
