package panel

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"

	bolt "go.etcd.io/bbolt"
)

// Credentials live in a separate private database key, outside Settings and the
// diagnostic/assistant context. Ordinary settings saves cannot erase them.
type EngineEgress struct {
	intervals          []portInterval
	Mode               string `json:"mode"`
	Address            string `json:"address"`
	Username           string `json:"username"`
	Password           string `json:"password,omitempty"`
	PasswordConfigured bool   `json:"password_configured,omitempty"`
	ClearPassword      bool   `json:"clear_password,omitempty"`
	Ports              string `json:"ports"`
	Unrestricted       bool   `json:"unrestricted"`
}

type OutboundLogConfig struct {
	Enabled        bool `json:"enabled"`
	RetentionHours int  `json:"retention_hours"`
	MaxMiB         int  `json:"max_mib"`
}

type EgressConfig struct {
	Revision  uint64            `json:"revision"`
	Soar      EngineEgress      `json:"soar"`
	Cotten    EngineEgress      `json:"cotten"`
	MasterDNS EngineEgress      `json:"masterdns"`
	Logs      OutboundLogConfig `json:"logs"`
	// OwnHostPorts opens these ports on this panel's public IP. Empty keeps every
	// connection to the server blocked. It lives here so a restore cannot enable it.
	OwnHostPorts string `json:"own_host_ports,omitempty"`
	ownHost      []portInterval
}

func defaultEgressConfig() EgressConfig {
	r := EngineEgress{Mode: "direct", Ports: "80,443"}
	return EgressConfig{Soar: r, Cotten: r, MasterDNS: r, Logs: OutboundLogConfig{RetentionHours: 24, MaxMiB: 32}}
}

type portInterval struct{ first, last uint16 }

func parsePortIntervals(raw string) ([]portInterval, error) {
	if len(raw) > 2048 {
		return nil, errors.New("port policy is too long")
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' || r == '،' })
	if len(parts) == 0 || len(parts) > 128 {
		return nil, errors.New("provide 1–128 ports or port ranges")
	}
	out := make([]portInterval, 0, len(parts))
	for _, part := range parts {
		a, b, rangePort := strings.Cut(part, "-")
		if !rangePort {
			b = a
		}
		lo, e1 := strconv.ParseUint(a, 10, 16)
		hi, e2 := strconv.ParseUint(b, 10, 16)
		if e1 != nil || e2 != nil || lo == 0 || hi < lo {
			return nil, errors.New("ports must be 1–65535, with ascending ranges such as 1-2000")
		}
		out = append(out, portInterval{uint16(lo), uint16(hi)})
	}
	return out, nil
}

func (r EngineEgress) validate() error {
	if r.Mode != "direct" && r.Mode != "socks5" && r.Mode != "http" && r.Mode != "https" {
		return errors.New("egress mode must be direct, socks5, http or https")
	}
	if r.Mode == "direct" {
		if r.Unrestricted {
			return errors.New("unrestricted ports apply only to an upstream proxy")
		}
		return nil
	}
	host, port, err := net.SplitHostPort(r.Address)
	n, e := strconv.ParseUint(port, 10, 16)
	if err != nil || e != nil || n == 0 || host == "" || len(host) > 253 || strings.ContainsAny(host, "/@?#%\\ \t\r\n") {
		return errors.New("proxy address must be HOST:PORT without credentials or a URL path")
	}
	if net.ParseIP(host) == nil {
		if err := ValidateDomain(host); err != nil && host != "localhost" {
			return errors.New("invalid proxy hostname")
		}
	}
	if len(r.Username) > 255 || len(r.Password) > 255 || strings.ContainsAny(r.Username+r.Password, "\x00\r\n") {
		return errors.New("proxy credentials must be at most 255 bytes and cannot contain control line breaks")
	}
	if (r.Username == "") != (r.Password == "") {
		return errors.New("provide both proxy username and password, or neither")
	}
	if (r.Mode == "http" || r.Mode == "https") && strings.Contains(r.Username, ":") {
		return errors.New("HTTP proxy username cannot contain a colon")
	}
	if !r.Unrestricted {
		_, err = parsePortIntervals(r.Ports)
		return err
	}
	return nil
}

func (c EgressConfig) validate() error {
	for name, r := range map[string]EngineEgress{"soar": c.Soar, "cotten": c.Cotten, "masterdns": c.MasterDNS} {
		if err := r.validate(); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	if c.OwnHostPorts != "" {
		if _, err := parsePortIntervals(c.OwnHostPorts); err != nil {
			return fmt.Errorf("own server ports: %w", err)
		}
	}
	if c.Logs.RetentionHours < 1 || c.Logs.RetentionHours > 720 || c.Logs.MaxMiB < 4 || c.Logs.MaxMiB > 256 {
		return errors.New("log retention must be 1–720 hours and storage 4–256 MiB")
	}
	return nil
}

func (c EgressConfig) route(engine upstreamEngine) EngineEgress {
	switch engine {
	case upstreamSoar:
		return c.Soar
	case upstreamCotten:
		return c.Cotten
	case upstreamMasterDNS:
		return c.MasterDNS
	default:
		return EngineEgress{Mode: "direct"}
	}
}

func (c *EgressConfig) prepare() {
	for _, r := range []*EngineEgress{&c.Soar, &c.Cotten, &c.MasterDNS} {
		if r.proxy() && !r.Unrestricted {
			r.intervals, _ = parsePortIntervals(r.Ports)
		}
	}
	if c.OwnHostPorts != "" {
		c.ownHost, _ = parsePortIntervals(c.OwnHostPorts)
	}
}

func (c EgressConfig) ownHostAllows(port uint16) bool {
	intervals := c.ownHost
	if intervals == nil && c.OwnHostPorts != "" {
		intervals, _ = parsePortIntervals(c.OwnHostPorts)
	}
	return portsContain(intervals, port)
}

// Only a real public address may be exempted, never a private or shared one.
var ownIPExcluded = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
}

func usableOwnIP(raw string) (netip.Addr, bool) {
	ip, err := netip.ParseAddr(raw)
	if err != nil || !ip.Is4() || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return netip.Addr{}, false
	}
	for _, excluded := range ownIPExcluded {
		if excluded.Contains(ip) {
			return netip.Addr{}, false
		}
	}
	return ip, true
}

func (r EngineEgress) proxy() bool { return r.Mode != "" && r.Mode != "direct" }
func (r EngineEgress) allows(port uint16, direct []uint16) bool {
	if !r.proxy() {
		for _, p := range direct {
			if p == port {
				return true
			}
		}
		return false
	}
	if r.Unrestricted {
		return true
	}
	intervals := r.intervals
	if intervals == nil {
		var err error
		intervals, err = parsePortIntervals(r.Ports)
		if err != nil {
			return false
		}
	}
	return portsContain(intervals, port)
}

func portsContain(intervals []portInterval, port uint16) bool {
	for _, p := range intervals {
		if port >= p.first && port <= p.last {
			return true
		}
	}
	return false
}

func readEgress(tx *bolt.Tx) (EgressConfig, error) {
	c := defaultEgressConfig()
	if raw := tx.Bucket(meta).Get([]byte("egress")); raw != nil {
		if len(raw) > 16384 {
			return c, errors.New("outbound configuration exceeds storage bounds")
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return c, err
		}
	}
	return c, c.validate()
}
func (s *Store) Egress() (c EgressConfig, err error) {
	err = s.db.View(func(tx *bolt.Tx) error { var e error; c, e = readEgress(tx); return e })
	return
}
func (c EgressConfig) publicView() EgressConfig {
	for _, r := range []*EngineEgress{&c.Soar, &c.Cotten, &c.MasterDNS} {
		r.PasswordConfigured = r.Password != ""
		r.Password = ""
		r.ClearPassword = false
	}
	return c
}

func (s *Store) SaveEgress(next EgressConfig) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		old, err := readEgress(tx)
		if err != nil {
			return err
		}
		if next.Revision != old.Revision {
			return ErrConflict
		}
		before := []EngineEgress{old.Soar, old.Cotten, old.MasterDNS}
		for i, r := range []*EngineEgress{&next.Soar, &next.Cotten, &next.MasterDNS} {
			prev := before[i]
			if r.ClearPassword {
				r.Password = ""
			} else if r.Password == "" && r.Mode == prev.Mode && r.Address == prev.Address && r.Username == prev.Username {
				r.Password = prev.Password
			}
			r.ClearPassword = false
			r.PasswordConfigured = false
		}
		if err := next.validate(); err != nil {
			return err
		}
		if next.OwnHostPorts != "" {
			var settings Settings
			if err := json.Unmarshal(tx.Bucket(meta).Get([]byte("settings")), &settings); err != nil {
				return err
			}
			if _, ok := usableOwnIP(settings.PublicIP); !ok {
				return errors.New("own server ports need this panel's public IPv4; set it from the server menu first")
			}
		}
		next.Revision++
		if err := putJSON(tx.Bucket(meta), []byte("egress"), next); err != nil {
			return err
		}
		return addEvent(tx, "egress.updated", "")
	})
}
