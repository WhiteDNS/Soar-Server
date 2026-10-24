package panel

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync/atomic"
	"time"
)

func (m *Manager) writeDNSUDP(packet []byte, remote net.Addr) (int, error) {
	n, err := m.pc.WriteTo(packet, remote)
	if n > 0 {
		m.dnsOut.Add(uint64(n))
	}
	if err != nil {
		m.diagnostics.udpWriteErrors.Add(1)
	}
	return n, err
}

// RuntimeDiagnostics contains bounded aggregate counters only. Engine messages,
// DNS names, remote addresses, account names and credentials are never retained.
type RuntimeDiagnostics struct {
	MasterDNSQueueDrops    uint64    `json:"masterdns_queue_drops"`
	MasterDNSStarts        uint64    `json:"masterdns_starts"`
	MasterDNSRunning       int64     `json:"masterdns_running"`
	MasterDNSStartErrors   uint64    `json:"masterdns_start_errors"`
	MasterDNSCloseErrors   uint64    `json:"masterdns_close_errors"`
	MasterDNSSessions      int       `json:"masterdns_sessions"`
	MasterDNSActiveStreams int64     `json:"masterdns_active_streams"`
	MasterDNSOpenedStreams uint64    `json:"masterdns_opened_streams"`
	MasterDNSClosedStreams uint64    `json:"masterdns_closed_streams"`
	SoarQueueDrops         uint64    `json:"soar_queue_drops"`
	CottenQueueDrops       uint64    `json:"cotten_queue_drops"`
	UDPWriteErrors         uint64    `json:"udp_write_errors"`
	TCPAcceptRetries       uint64    `json:"tcp_accept_retries"`
	DialAttempts           uint64    `json:"dial_attempts"`
	DialFailures           uint64    `json:"dial_failures"`
	DialLatencyMicros      uint64    `json:"dial_latency_microseconds"`
	DialLatencyMaxMicros   uint64    `json:"dial_latency_max_microseconds"`
	DialLatencyBuckets     [5]uint64 `json:"dial_latency_buckets_10ms_100ms_1s_5s_over5s"`
	RejectedAccountLimit   uint64    `json:"rejected_account_limit"`
	RejectedServerLimit    uint64    `json:"rejected_server_limit"`
	RejectedPolicy         uint64    `json:"rejected_policy"`
	RejectedInactive       uint64    `json:"rejected_inactive"`
	SoarStarts             uint64    `json:"soar_starts"`
	SoarRunning            int64     `json:"soar_running"`
	SoarExits              uint64    `json:"soar_exits"`
	SoarUnexpectedExits    uint64    `json:"soar_unexpected_exits"`
	SoarServeErrors        uint64    `json:"soar_serve_errors"`
	SoarWarnings           uint64    `json:"soar_warnings"`
	SoarLogErrors          uint64    `json:"soar_log_errors"`
	CottenStarts           uint64    `json:"cotten_starts"`
	CottenRunning          int64     `json:"cotten_running"`
	CottenStartErrors      uint64    `json:"cotten_start_errors"`
	CottenCloseErrors      uint64    `json:"cotten_close_errors"`
	LastEngineIssueUnixMS  int64     `json:"last_engine_issue_unix_ms"`
	CottenSessions         int       `json:"cotten_sessions"`
	SoarActiveStreams      int64     `json:"soar_active_streams"`
	CottenActiveStreams    int64     `json:"cotten_active_streams"`
	SoarOpenedStreams      uint64    `json:"soar_opened_streams"`
	CottenOpenedStreams    uint64    `json:"cotten_opened_streams"`
	SoarClosedStreams      uint64    `json:"soar_closed_streams"`
	CottenClosedStreams    uint64    `json:"cotten_closed_streams"`
	UpstreamCloseErrors    uint64    `json:"upstream_close_errors"`
}

type runtimeCounters struct {
	masterQueueDrops, masterStarts, masterStartErrors, masterCloseErrors                                atomic.Uint64
	masterOpenedStreams, masterClosedStreams                                                            atomic.Uint64
	masterRunning, masterActiveStreams                                                                  atomic.Int64
	soarQueueDrops, cottenQueueDrops, udpWriteErrors, tcpAcceptRetries                                  atomic.Uint64
	dialAttempts, dialFailures, dialLatencyMicros, dialLatencyMaxMicros                                 atomic.Uint64
	dialLatencyBuckets                                                                                  [5]atomic.Uint64
	rejectedAccountLimit, rejectedServerLimit, rejectedPolicy, rejectedInactive                         atomic.Uint64
	soarStarts, soarExits, soarUnexpectedExits, soarServeErrors                                         atomic.Uint64
	soarWarnings, soarLogErrors, cottenStarts, cottenStartErrors, cottenCloseErrors                     atomic.Uint64
	soarRunning, cottenRunning, lastEngineIssueUnixMS                                                   atomic.Int64
	soarActiveStreams, cottenActiveStreams                                                              atomic.Int64
	soarOpenedStreams, cottenOpenedStreams, soarClosedStreams, cottenClosedStreams, upstreamCloseErrors atomic.Uint64
}

func (m *Manager) Diagnostics() RuntimeDiagnostics {
	c := &m.diagnostics
	d := RuntimeDiagnostics{
		MasterDNSQueueDrops: c.masterQueueDrops.Load(), MasterDNSStarts: c.masterStarts.Load(), MasterDNSRunning: c.masterRunning.Load(),
		MasterDNSStartErrors: c.masterStartErrors.Load(), MasterDNSCloseErrors: c.masterCloseErrors.Load(), MasterDNSActiveStreams: c.masterActiveStreams.Load(),
		MasterDNSOpenedStreams: c.masterOpenedStreams.Load(), MasterDNSClosedStreams: c.masterClosedStreams.Load(),
		SoarQueueDrops: c.soarQueueDrops.Load(), CottenQueueDrops: c.cottenQueueDrops.Load(), UDPWriteErrors: c.udpWriteErrors.Load(), TCPAcceptRetries: c.tcpAcceptRetries.Load(),
		DialAttempts: c.dialAttempts.Load(), DialFailures: c.dialFailures.Load(), DialLatencyMicros: c.dialLatencyMicros.Load(), DialLatencyMaxMicros: c.dialLatencyMaxMicros.Load(),
		RejectedAccountLimit: c.rejectedAccountLimit.Load(), RejectedServerLimit: c.rejectedServerLimit.Load(), RejectedPolicy: c.rejectedPolicy.Load(), RejectedInactive: c.rejectedInactive.Load(),
		SoarStarts: c.soarStarts.Load(), SoarRunning: c.soarRunning.Load(), SoarExits: c.soarExits.Load(), SoarUnexpectedExits: c.soarUnexpectedExits.Load(), SoarServeErrors: c.soarServeErrors.Load(), SoarWarnings: c.soarWarnings.Load(), SoarLogErrors: c.soarLogErrors.Load(),
		CottenStarts: c.cottenStarts.Load(), CottenRunning: c.cottenRunning.Load(), CottenStartErrors: c.cottenStartErrors.Load(), CottenCloseErrors: c.cottenCloseErrors.Load(), LastEngineIssueUnixMS: c.lastEngineIssueUnixMS.Load(),
	}
	for i := range d.DialLatencyBuckets {
		d.DialLatencyBuckets[i] = c.dialLatencyBuckets[i].Load()
	}
	d.SoarActiveStreams, d.CottenActiveStreams = c.soarActiveStreams.Load(), c.cottenActiveStreams.Load()
	d.SoarOpenedStreams, d.CottenOpenedStreams = c.soarOpenedStreams.Load(), c.cottenOpenedStreams.Load()
	d.SoarClosedStreams, d.CottenClosedStreams = c.soarClosedStreams.Load(), c.cottenClosedStreams.Load()
	d.UpstreamCloseErrors = c.upstreamCloseErrors.Load()
	m.mu.RLock()
	runtimes := make([]*accountRuntime, 0, len(m.accounts))
	for _, rt := range m.accounts {
		runtimes = append(runtimes, rt)
	}
	m.mu.RUnlock()
	for _, rt := range runtimes {
		rt.masterMu.Lock()
		master := rt.master
		rt.masterMu.Unlock()
		if master != nil {
			d.MasterDNSSessions += master.SessionCount()
		}
		rt.cottenMu.Lock()
		engine := rt.cotten
		rt.cottenMu.Unlock()
		if engine != nil {
			d.CottenSessions += engine.SessionCount()
		}
	}
	return d
}

type upstreamEngine uint8

const (
	upstreamUnknown upstreamEngine = iota
	upstreamSoar
	upstreamCotten
	upstreamMasterDNS
)

func (c *runtimeCounters) upstreamOpened(engine upstreamEngine) {
	switch engine {
	case upstreamSoar:
		c.soarActiveStreams.Add(1)
		c.soarOpenedStreams.Add(1)
	case upstreamMasterDNS:
		c.masterActiveStreams.Add(1)
		c.masterOpenedStreams.Add(1)
	case upstreamCotten:
		c.cottenActiveStreams.Add(1)
		c.cottenOpenedStreams.Add(1)
	}
}
func (c *runtimeCounters) upstreamClosed(engine upstreamEngine, err error) {
	switch engine {
	case upstreamSoar:
		c.soarActiveStreams.Add(-1)
		c.soarClosedStreams.Add(1)
	case upstreamMasterDNS:
		c.masterActiveStreams.Add(-1)
		c.masterClosedStreams.Add(1)
	case upstreamCotten:
		c.cottenActiveStreams.Add(-1)
		c.cottenClosedStreams.Add(1)
	}
	if err != nil && !errors.Is(err, net.ErrClosed) {
		c.upstreamCloseErrors.Add(1)
	}
}

func (c *runtimeCounters) recordDial(elapsed time.Duration, failed bool) {
	if failed {
		c.dialFailures.Add(1)
	}
	micros := uint64(max(elapsed.Microseconds(), 0))
	c.dialLatencyMicros.Add(micros)
	for old := c.dialLatencyMaxMicros.Load(); micros > old; old = c.dialLatencyMaxMicros.Load() {
		if c.dialLatencyMaxMicros.CompareAndSwap(old, micros) {
			break
		}
	}
	bucket := 4
	for i, bound := range [...]time.Duration{10 * time.Millisecond, 100 * time.Millisecond, time.Second, 5 * time.Second} {
		if elapsed <= bound {
			bucket = i
			break
		}
	}
	c.dialLatencyBuckets[bucket].Add(1)
}

// The engine's log attributes may contain traffic metadata. Record severity and
// health without forwarding either message text or attributes to another logger.
type engineDiagnosticHandler struct{ counters *runtimeCounters }

func (h engineDiagnosticHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelWarn
}
func (h engineDiagnosticHandler) Handle(_ context.Context, record slog.Record) error {
	if record.Level >= slog.LevelError {
		h.counters.soarLogErrors.Add(1)
	} else {
		h.counters.soarWarnings.Add(1)
	}
	h.counters.lastEngineIssueUnixMS.Store(time.Now().UnixMilli())
	return nil
}
func (h engineDiagnosticHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h engineDiagnosticHandler) WithGroup(string) slog.Handler      { return h }
