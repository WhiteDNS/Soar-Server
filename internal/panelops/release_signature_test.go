package panelops

import "testing"

func TestUnsignedOrForgedReleaseIsRejected(t *testing.T) {
	sums := []byte("abc  soar-panel-0.14.2-linux-amd64.tar.gz\n")
	for name, signature := range map[string][]byte{"missing": nil, "truncated": make([]byte, 10), "forged": make([]byte, 64)} {
		if e := verifyRelease(sums, signature, "abc", "soar-panel-0.14.2-linux-amd64.tar.gz"); e == nil {
			t.Fatal(name, "signature accepted")
		}
	}
}
