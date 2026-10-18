package panel

import "fmt"

// ProfileSettings is the subset supported by the released WhiteDNS Soar engine.
// These settings travel in exported links; they do not remotely change imports.
type ProfileSettings struct {
	Transport            string `json:"transport"`
	Discovery            bool   `json:"discovery"`
	DiscoveryWant        int    `json:"discoveryWant"`
	DiscoveryConcurrency int    `json:"discoveryConcurrency"`
	DiscoveryPaceMs      int    `json:"discoveryPaceMs"`
	SafeQName            int    `json:"safeQName"`
	MaxQName             int    `json:"maxQName"`
	MaxAnswer            int    `json:"maxAnswer"`
	HoldMs               int    `json:"holdMs"`
	MaxInflight          int    `json:"maxInflight"`
	MaxPathWindow        int    `json:"maxPathWindow"`
	MaxStreams           int    `json:"maxStreams"`
	StreamWindowKiB      int    `json:"streamWindowKiB"`
	SendBufferKiB        int    `json:"sendBufferKiB"`
	SessionIdleSeconds   int    `json:"sessionIdleSeconds"`
	DialTimeoutSeconds   int    `json:"dialTimeoutSeconds"`
}

func DefaultProfileSettings() ProfileSettings {
	return ProfileSettings{
		Transport: "auto", Discovery: true, DiscoveryWant: 2,
		DiscoveryConcurrency: 2, DiscoveryPaceMs: 1000,
		SafeQName: 101, MaxQName: 253, MaxAnswer: 1232, HoldMs: 500,
		MaxInflight: 128, MaxPathWindow: 32, MaxStreams: 128,
		StreamWindowKiB: 256, SendBufferKiB: 64,
		SessionIdleSeconds: 90, DialTimeoutSeconds: 90,
	}
}

func (p ProfileSettings) Validate() error {
	switch p.Transport {
	case "auto", "on", "only", "off":
	default:
		return fmt.Errorf("transport must be auto, on, only or off")
	}
	for _, bound := range []struct {
		name             string
		value, low, high int
	}{
		{"discoveryWant", p.DiscoveryWant, 1, 32},
		{"discoveryConcurrency", p.DiscoveryConcurrency, 1, 16},
		{"discoveryPaceMs", p.DiscoveryPaceMs, 100, 5000},
		{"safeQName", p.SafeQName, 101, 253}, {"maxQName", p.MaxQName, p.SafeQName, 253},
		{"maxAnswer", p.MaxAnswer, 512, 1232}, {"holdMs", p.HoldMs, 100, 1500},
		{"maxInflight", p.MaxInflight, 8, 1024}, {"maxPathWindow", p.MaxPathWindow, 2, 128},
		// The panel server currently caps each account at 128 streams.
		{"maxStreams", p.MaxStreams, 8, 128},
		{"streamWindowKiB", p.StreamWindowKiB, 64, 1024}, {"sendBufferKiB", p.SendBufferKiB, 32, 1024},
		{"sessionIdleSeconds", p.SessionIdleSeconds, 15, 110},
		{"dialTimeoutSeconds", p.DialTimeoutSeconds, 5, 120},
	} {
		if bound.value < bound.low || bound.value > bound.high {
			return fmt.Errorf("%s must be between %d and %d", bound.name, bound.low, bound.high)
		}
	}
	return nil
}

func (s Settings) profileSettings() ProfileSettings {
	if s.SoarSettings == nil {
		return DefaultProfileSettings()
	}
	return *s.SoarSettings
}
