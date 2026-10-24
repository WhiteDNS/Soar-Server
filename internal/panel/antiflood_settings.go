package panel

import (
	"errors"
	"fmt"
)

// AntiFloodPolicy is enforced on authenticated outbound streams, across engines
// and tunnel aliases. It deliberately never bans a recursive DNS resolver.
type AntiFloodPolicy struct {
	Mode               string `json:"mode"`
	AccountRate        int    `json:"account_rate"`
	AccountBurst       int    `json:"account_burst"`
	ServerRate         int    `json:"server_rate"`
	ServerBurst        int    `json:"server_burst"`
	TargetRate         int    `json:"target_rate"`
	TargetBurst        int    `json:"target_burst"`
	NetworkRate        int    `json:"network_rate"`
	NetworkBurst       int    `json:"network_burst"`
	AccountStreams     int    `json:"account_streams"`
	ServerStreams      int    `json:"server_streams"`
	TargetStreams      int    `json:"target_streams"`
	TargetsPerMinute   int    `json:"targets_per_minute"`
	FailureThreshold   int    `json:"failure_threshold"`
	FailurePercent     int    `json:"failure_percent"`
	ViolationThreshold int    `json:"violation_threshold"`
	CooldownSeconds    int    `json:"cooldown_seconds"`
	AccountUploadKiB   int    `json:"account_upload_kib"`
	TargetUploadKiB    int    `json:"target_upload_kib"`
	ServerUploadKiB    int    `json:"server_upload_kib"`
	ResolverQPS        int    `json:"resolver_qps"`
}

func DefaultAntiFloodPolicy() AntiFloodPolicy {
	return AntiFloodPolicy{Mode: "enforce", AccountRate: 20, AccountBurst: 80,
		ServerRate: 200, ServerBurst: 400, TargetRate: 10, TargetBurst: 40,
		NetworkRate: 40, NetworkBurst: 120, AccountStreams: 128, ServerStreams: 1024,
		TargetStreams: 128, TargetsPerMinute: 300, FailureThreshold: 40, FailurePercent: 90,
		ViolationThreshold: 120, CooldownSeconds: 300, AccountUploadKiB: 2048,
		TargetUploadKiB: 4096, ServerUploadKiB: 16384, ResolverQPS: 100}
}

func (p AntiFloodPolicy) Validate() error {
	if p.Mode != "off" && p.Mode != "monitor" && p.Mode != "enforce" {
		return errors.New("antiflood mode must be off, monitor or enforce")
	}
	for _, v := range []struct {
		name        string
		n, min, max int
	}{
		{"account_rate", p.AccountRate, 1, 10000}, {"account_burst", p.AccountBurst, 1, 20000},
		{"server_rate", p.ServerRate, 1, 100000}, {"server_burst", p.ServerBurst, 1, 200000},
		{"target_rate", p.TargetRate, 1, 10000}, {"target_burst", p.TargetBurst, 1, 20000},
		{"network_rate", p.NetworkRate, 1, 20000}, {"network_burst", p.NetworkBurst, 1, 40000},
		{"account_streams", p.AccountStreams, 1, 4096}, {"server_streams", p.ServerStreams, 1, 32768},
		{"target_streams", p.TargetStreams, 1, 4096}, {"targets_per_minute", p.TargetsPerMinute, 10, 2000},
		{"failure_threshold", p.FailureThreshold, 10, 10000}, {"failure_percent", p.FailurePercent, 50, 100},
		{"violation_threshold", p.ViolationThreshold, 10, 10000}, {"cooldown_seconds", p.CooldownSeconds, 30, 3600},
		{"account_upload_kib", p.AccountUploadKiB, 32, 524288},
		{"target_upload_kib", p.TargetUploadKiB, 32, 524288},
		{"server_upload_kib", p.ServerUploadKiB, 32, 524288}, {"resolver_qps", p.ResolverQPS, 1, 10000},
	} {
		if v.n < v.min || v.n > v.max {
			return fmt.Errorf("antiflood %s must be between %d and %d", v.name, v.min, v.max)
		}
	}
	return nil
}

func (s Settings) antiFloodPolicy() AntiFloodPolicy {
	if s.AntiFlood == nil {
		return DefaultAntiFloodPolicy()
	}
	return *s.AntiFlood
}
