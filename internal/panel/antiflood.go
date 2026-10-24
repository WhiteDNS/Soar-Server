package panel

import (
	"errors"
	"net/netip"
	"sort"
	"sync"
	"time"
)

var errAntiFlood = errors.New("outbound traffic temporarily limited by antiflood policy")

const maxFloodTargets = 8192
const maxFloodNetworks = 4096

// Every bucket is accessed under floodGuard.mu. Tokens are checked before a
// network operation, never after bytes/SYNs have already left the host.
type floodBucket struct {
	tokens float64
	at     time.Time
}

func (b *floodBucket) available(now time.Time, rate, burst, cost int) bool {
	if b.at.IsZero() {
		b.tokens, b.at = float64(burst), now
	} else if now.After(b.at) {
		b.tokens = min(float64(burst), b.tokens+now.Sub(b.at).Seconds()*float64(rate))
		b.at = now
	}
	b.tokens = min(b.tokens, float64(burst))
	return b.tokens >= float64(cost)
}

type floodWindow struct {
	slots [60]struct {
		second int64
		n      int
	}
}

func (w *floodWindow) add(now time.Time) {
	s := now.Unix()
	i := s % 60
	if w.slots[i].second != s {
		w.slots[i].second, w.slots[i].n = s, 0
	}
	w.slots[i].n++
}

func (w *floodWindow) count(now time.Time, seconds int64) int {
	n := 0
	for _, s := range w.slots {
		if age := now.Unix() - s.second; age >= 0 && age < seconds {
			n += s.n
		}
	}
	return n
}

type floodAccount struct {
	rate, upload, resolver         floodBucket
	active                         int
	targets                        map[netip.Addr]time.Time
	failedTargets                  map[netip.Addr]time.Time
	attempts, failures, violations floodWindow
	lastSeen                       time.Time
	lastEvent                      map[string]time.Time
	denied, observed               uint64
}

type floodDestination struct {
	rate, upload floodBucket
	active       int
	lastSeen     time.Time
}

type FloodBlock struct {
	Until   int64  `json:"until"`
	At      int64  `json:"at"`
	Strikes int    `json:"strikes"`
	Reason  string `json:"reason"`
}

type FloodEvent struct {
	At       int64  `json:"at"`
	ClientID string `json:"client_id"`
	Reason   string `json:"reason"`
	Action   string `json:"action"`
}

type FloodAccountStatus struct {
	ClientID string     `json:"client_id"`
	Active   int        `json:"active"`
	Denied   uint64     `json:"denied"`
	Observed uint64     `json:"observed"`
	Block    FloodBlock `json:"block"`
}

type FloodStatus struct {
	Policy            AntiFloodPolicy      `json:"policy"`
	Denied            uint64               `json:"denied"`
	Observed          uint64               `json:"observed"`
	Quarantines       uint64               `json:"quarantines"`
	Active            int                  `json:"active"`
	Reasons           map[string]uint64    `json:"reasons"`
	Accounts          []FloodAccountStatus `json:"accounts"`
	Events            []FloodEvent         `json:"events"`
	PersistenceErrors uint64               `json:"persistence_errors"`
	// Traffic destinations are deliberately never part of this API/log.
}

type floodGuard struct {
	mu                                               sync.Mutex
	store                                            *Store
	policy                                           AntiFloodPolicy
	loaded                                           bool
	accounts                                         map[string]*floodAccount
	targets                                          map[netip.Addr]*floodDestination
	networks                                         map[netip.Prefix]*floodDestination
	blocks                                           map[string]FloodBlock
	server, upload, resolver                         floodBucket
	active                                           int
	denied, observed, quarantines, persistenceErrors uint64
	reasons                                          map[string]uint64
	events                                           []FloodEvent
	pruned                                           time.Time
}

func newFloodGuard(s *Store) *floodGuard {
	return &floodGuard{store: s, policy: DefaultAntiFloodPolicy(), accounts: map[string]*floodAccount{},
		targets: map[netip.Addr]*floodDestination{}, networks: map[netip.Prefix]*floodDestination{},
		blocks: map[string]FloodBlock{}, reasons: map[string]uint64{}}
}

func (g *floodGuard) configure(p AntiFloodPolicy, clients []Client) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.loaded {
		blocks, err := g.store.floodBlocks()
		if err != nil {
			return err
		}
		g.blocks, g.loaded = blocks, true
	}
	g.policy = p
	valid := map[string]bool{}
	for _, c := range clients {
		valid[c.ID] = true
	}
	for id, a := range g.accounts {
		if !valid[id] && a.active == 0 {
			delete(g.accounts, id)
		}
	}
	for id := range g.blocks {
		if !valid[id] {
			delete(g.blocks, id)
		}
	}
	return nil
}

func (g *floodGuard) accountLocked(id string, now time.Time) *floodAccount {
	a := g.accounts[id]
	if a == nil {
		// IDs come only from Store.Get or authenticated engine callbacks. A
		// deleted account with live leases may briefly remain during reconciliation.
		if len(g.accounts) >= 2*(MaxClients+1) {
			return nil
		}
		a = &floodAccount{targets: map[netip.Addr]time.Time{}, failedTargets: map[netip.Addr]time.Time{}, lastEvent: map[string]time.Time{}}
		g.accounts[id] = a
	}
	a.lastSeen = now
	return a
}

func (g *floodGuard) blockedLocked(id string, now time.Time) bool {
	return g.policy.Mode == "enforce" && g.blocks[id].Until > now.UnixMilli()
}

func (g *floodGuard) blocked(id string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.blockedLocked(id, time.Now())
}

func (g *floodGuard) noteLocked(id, reason string, accountable bool, now time.Time) (bool, bool) {
	g.reasons[reason]++
	a := g.accountLocked(id, now)
	action := "observed"
	if g.policy.Mode == "enforce" {
		g.denied++
		action = "denied"
		if a != nil {
			a.denied++
		}
	} else {
		g.observed++
		if a != nil {
			a.observed++
		}
	}
	if a != nil && (a.lastEvent[reason].IsZero() || now.Sub(a.lastEvent[reason]) >= 10*time.Second) {
		a.lastEvent[reason] = now
		g.eventLocked(FloodEvent{At: now.UnixMilli(), ClientID: id, Reason: reason, Action: action})
	}
	quarantined := false
	if accountable && a != nil && g.policy.Mode == "enforce" {
		a.violations.add(now)
		if a.violations.count(now, 10) >= g.policy.ViolationThreshold {
			quarantined = g.quarantineLocked(id, reason, now)
		}
	}
	return g.policy.Mode == "enforce", quarantined
}

func (g *floodGuard) eventLocked(ev FloodEvent) {
	if len(g.events) == 200 {
		copy(g.events, g.events[1:])
		g.events = g.events[:199]
	}
	g.events = append(g.events, ev)
}

func (g *floodGuard) quarantineLocked(id, reason string, now time.Time) bool {
	if g.blockedLocked(id, now) {
		return false
	}
	old := g.blocks[id]
	strikes := 1
	if now.UnixMilli()-old.At < int64(24*time.Hour/time.Millisecond) {
		strikes = min(old.Strikes+1, 5)
	}
	seconds := min(g.policy.CooldownSeconds*(1<<(strikes-1)), 3600)
	b := FloodBlock{At: now.UnixMilli(), Until: now.Add(time.Duration(seconds) * time.Second).UnixMilli(), Strikes: strikes, Reason: reason}
	g.blocks[id] = b
	g.quarantines++
	g.eventLocked(FloodEvent{At: b.At, ClientID: id, Reason: reason, Action: "quarantined"})
	// Only quarantine transitions write to disk, never individual packets/dials.
	if err := g.store.saveFloodBlock(id, &b); err != nil {
		g.persistenceErrors++
	}
	return true
}

type floodLease struct {
	guard           *floodGuard
	id              string
	account         *floodAccount
	target, network *floodDestination
	targetIP        netip.Addr
	once            sync.Once
}

func (l *floodLease) release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		g := l.guard
		g.mu.Lock()
		defer g.mu.Unlock()
		g.active--
		l.account.active--
		if l.target != nil {
			l.target.active--
		}
		if l.network != nil {
			l.network.active--
		}
	})
}

// begin runs BEFORE resolution, bounding both pending DNS lookups and sockets.
// A lease is held until the actual connection closes, including failed dials.
func (g *floodGuard) begin(id string, resolver bool, now time.Time) (*floodLease, error, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.blockedLocked(id, now) {
		g.noteLocked(id, "quarantine", false, now)
		return nil, errAntiFlood, false
	}
	a := g.accountLocked(id, now)
	if a == nil {
		deny, q := g.noteLocked(id, "capacity", false, now)
		if deny {
			return nil, errAntiFlood, q
		}
		return nil, nil, false
	}
	p := g.policy
	checks := []struct {
		ok          bool
		reason      string
		accountable bool
	}{
		{g.active < p.ServerStreams, "server_streams", false},
		{a.active < p.AccountStreams, "account_streams", false},
	}
	global, local := &g.server, &a.rate
	globalRate, globalBurst, localRate, localBurst := p.ServerRate, p.ServerBurst, p.AccountRate, p.AccountBurst
	if resolver {
		global, local = &g.resolver, &a.resolver
		globalRate, globalBurst, localRate, localBurst = p.ResolverQPS*4, p.ResolverQPS*8, p.ResolverQPS, p.ResolverQPS*2
	}
	globalOK, localOK := global.available(now, globalRate, globalBurst, 1), local.available(now, localRate, localBurst, 1)
	if resolver {
		checks = append(checks, struct {
			ok          bool
			reason      string
			accountable bool
		}{globalOK, "server_resolver_rate", false}, struct {
			ok          bool
			reason      string
			accountable bool
		}{localOK, "resolver_rate", false})
	} else {
		checks = append(checks, struct {
			ok          bool
			reason      string
			accountable bool
		}{globalOK, "server_rate", false}, struct {
			ok          bool
			reason      string
			accountable bool
		}{localOK, "account_rate", true})
	}
	for _, c := range checks {
		if !c.ok && p.Mode != "off" {
			if deny, q := g.noteLocked(id, c.reason, c.accountable, now); deny {
				return nil, errAntiFlood, q
			}
		}
	}
	global.tokens--
	local.tokens--
	// Monitor mode must not accumulate negative debt indefinitely.
	global.tokens = max(global.tokens, 0)
	local.tokens = max(local.tokens, 0)
	g.active++
	a.active++
	return &floodLease{guard: g, id: id, account: a}, nil, false
}

func floodNetwork(ip netip.Addr) netip.Prefix {
	ip = floodTargetIP(ip)
	bits := 64
	if ip.Is4() {
		bits = 24
	}
	return netip.PrefixFrom(ip, bits).Masked()
}

func floodTargetIP(ip netip.Addr) netip.Addr {
	ip = ip.Unmap()
	if ip.Is6() {
		b := ip.As16()
		// The well-known NAT64 and 6to4 encodings can reach the same IPv4
		// endpoint; account them as that endpoint rather than a fresh target.
		if netip.MustParsePrefix("64:ff9b::/96").Contains(ip) {
			return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
		}
		if netip.MustParsePrefix("2002::/16").Contains(ip) {
			return netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]})
		}
	}
	return ip
}

// Canonical keys exclude hostnames, ports and engine names. Rotating aliases,
// ports, accounts, IPv4-mapped addresses or IPv6 hosts cannot reset a target cap.
func (g *floodGuard) target(l *floodLease, ip netip.Addr, now time.Time) (error, bool) {
	if l == nil {
		return nil, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.blockedLocked(l.id, now) {
		return errAntiFlood, false
	}
	g.pruneLocked(now)
	ip = floodTargetIP(ip)
	prefix := floodNetwork(ip)
	a, p := l.account, g.policy
	for key, at := range a.targets {
		if now.Sub(at) >= time.Minute {
			delete(a.targets, key)
		}
	}
	if _, seen := a.targets[ip]; !seen && len(a.targets) >= p.TargetsPerMinute {
		if g.policy.Mode != "off" {
			if deny, q := g.noteLocked(l.id, "target_scan", true, now); deny {
				return errAntiFlood, q
			}
		}
	}
	if len(a.targets) < 2000 {
		a.targets[ip] = now
	} else if _, ok := a.targets[ip]; ok {
		a.targets[ip] = now
	}
	t, n := g.targets[ip], g.networks[prefix]
	if t == nil && len(g.targets) >= maxFloodTargets || n == nil && len(g.networks) >= maxFloodNetworks {
		if g.policy.Mode != "off" {
			if deny, q := g.noteLocked(l.id, "capacity", false, now); deny {
				return errAntiFlood, q
			}
		}
		return nil, false
	}
	if t == nil {
		t = &floodDestination{}
		g.targets[ip] = t
	}
	if n == nil {
		n = &floodDestination{}
		g.networks[prefix] = n
	}
	t.lastSeen, n.lastSeen = now, now
	for _, c := range []struct {
		ok     bool
		reason string
	}{
		{t.active < p.TargetStreams, "target_streams"},
		{t.rate.available(now, p.TargetRate, p.TargetBurst, 1), "target_rate"},
		{n.rate.available(now, p.NetworkRate, p.NetworkBurst, 1), "network_rate"},
	} {
		if !c.ok && p.Mode != "off" {
			if deny, q := g.noteLocked(l.id, c.reason, false, now); deny {
				return errAntiFlood, q
			}
		}
	}
	t.rate.tokens = max(t.rate.tokens-1, 0)
	n.rate.tokens = max(n.rate.tokens-1, 0)
	t.active++
	n.active++
	l.target, l.network = t, n
	l.targetIP = ip
	return nil, false
}

func (g *floodGuard) pruneLocked(now time.Time) {
	if !g.pruned.IsZero() && now.Sub(g.pruned) < 10*time.Second {
		return
	}
	g.pruned = now
	// Evict only refilled idle buckets: churning targets cannot reset a debt.
	p := g.policy
	for ip, t := range g.targets {
		if t.active == 0 && now.Sub(t.lastSeen) > time.Minute && t.rate.available(now, p.TargetRate, p.TargetBurst, p.TargetBurst) && t.upload.available(now, p.TargetUploadKiB*1024, p.TargetUploadKiB*2048, p.TargetUploadKiB*2048) {
			delete(g.targets, ip)
		}
	}
	for prefix, t := range g.networks {
		if t.active == 0 && now.Sub(t.lastSeen) > time.Minute && t.rate.available(now, p.NetworkRate, p.NetworkBurst, p.NetworkBurst) {
			delete(g.networks, prefix)
		}
	}
}

func (g *floodGuard) outcome(l *floodLease, failed bool, now time.Time) bool {
	if l == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.policy.Mode == "off" {
		return false
	}
	a := l.account
	a.attempts.add(now)
	if failed {
		a.failures.add(now)
		for ip, at := range a.failedTargets {
			if now.Sub(at) >= time.Minute {
				delete(a.failedTargets, ip)
			}
		}
		if l.targetIP.IsValid() && len(a.failedTargets) < 2000 {
			a.failedTargets[l.targetIP] = now
		}
	}
	fails, total := a.failures.count(now, 60), a.attempts.count(now, 60)
	// A single unavailable website must not label an account as a scanner.
	if failed && len(a.failedTargets) >= 10 && fails >= g.policy.FailureThreshold && fails*100 >= total*g.policy.FailurePercent {
		g.noteLocked(l.id, "dial_failures", false, now)
		if g.policy.Mode == "enforce" {
			return g.quarantineLocked(l.id, "dial_failures", now)
		}
	}
	return false
}

// Upload caps protect already-established encrypted streams. No TLS inspection
// or inference about HTTP requests is performed. Downloads are not upload.
func (g *floodGuard) write(l *floodLease, bytes int, now time.Time) (error, bool) {
	if l == nil {
		return nil, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.policy.Mode == "off" {
		return nil, false
	}
	if g.blockedLocked(l.id, now) {
		return errAntiFlood, false
	}
	p := g.policy
	checks := []struct {
		bucket *floodBucket
		rate   int
		reason string
	}{
		{&g.upload, p.ServerUploadKiB * 1024, "server_upload"},
		{&l.account.upload, p.AccountUploadKiB * 1024, "account_upload"},
	}
	if l.target != nil {
		checks = append(checks, struct {
			bucket *floodBucket
			rate   int
			reason string
		}{&l.target.upload, p.TargetUploadKiB * 1024, "target_upload"})
	}
	for _, c := range checks {
		if !c.bucket.available(now, c.rate, 2*c.rate, bytes) {
			if deny, q := g.noteLocked(l.id, c.reason, false, now); deny {
				return errAntiFlood, q
			}
		}
	}
	for _, c := range checks {
		c.bucket.tokens = max(c.bucket.tokens-float64(bytes), 0)
	}
	return nil, false
}

func (g *floodGuard) snapshot() FloodStatus {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := FloodStatus{Policy: g.policy, Denied: g.denied, Observed: g.observed, Quarantines: g.quarantines, Active: g.active,
		PersistenceErrors: g.persistenceErrors, Reasons: map[string]uint64{}, Accounts: []FloodAccountStatus{}, Events: []FloodEvent{}}
	for k, v := range g.reasons {
		s.Reasons[k] = v
	}
	for id, a := range g.accounts {
		s.Accounts = append(s.Accounts, FloodAccountStatus{ClientID: id, Active: a.active, Denied: a.denied, Observed: a.observed, Block: g.blocks[id]})
	}
	for id, b := range g.blocks {
		if g.accounts[id] == nil {
			s.Accounts = append(s.Accounts, FloodAccountStatus{ClientID: id, Block: b})
		}
	}
	sort.Slice(s.Accounts, func(i, j int) bool { return s.Accounts[i].ClientID < s.Accounts[j].ClientID })
	for i := len(g.events) - 1; i >= 0; i-- {
		s.Events = append(s.Events, g.events[i])
	}
	return s
}

func (g *floodGuard) reset(id string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.store.saveFloodBlock(id, nil); err != nil {
		return err
	}
	delete(g.blocks, id)
	if a := g.accounts[id]; a != nil {
		a.violations = floodWindow{}
		a.attempts = floodWindow{}
		a.failures = floodWindow{}
		clear(a.targets)
		clear(a.failedTargets)
		// Keep rate/upload buckets: manual release must not create a burst bypass.
	}
	g.eventLocked(FloodEvent{At: time.Now().UnixMilli(), ClientID: id, Reason: "manual_release", Action: "released"})
	return nil
}
