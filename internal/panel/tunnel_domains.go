package panel

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/publicsuffix"
)

// BaseZone remains the identity namespace of old accounts. Serving and preferring
// a different delegation never changes their seed, counters or subscription token.
func (s Settings) tunnelZones() []string {
	if len(s.TunnelZones) == 0 {
		return []string{s.BaseZone}
	}
	return append([]string(nil), s.TunnelZones...)
}
func (s Settings) primaryZone() string {
	if s.PrimaryZone != "" {
		return s.PrimaryZone
	}
	return s.tunnelZones()[0]
}
func (s Settings) orderedZones() []string {
	zones := []string{s.primaryZone()}
	for _, z := range s.tunnelZones() {
		if z != zones[0] {
			zones = append(zones, z)
		}
	}
	return zones
}
func (s Settings) validateTunnelZones() error {
	if len(s.TunnelZones) > 8 {
		return errors.New("use at most eight tunnel domains")
	}
	seen := map[string]bool{}
	for _, z := range s.tunnelZones() {
		if len(z) < 3 || len(z) > 24 || ValidateDomain(z) != nil || seen[z] {
			return errors.New("tunnel domains must be unique ASCII domains of 3–24 characters")
		}
		for other := range seen {
			if strings.HasSuffix(z, "."+other) || strings.HasSuffix(other, "."+z) {
				return errors.New("tunnel domains cannot be nested")
			}
		}
		seen[z] = true
	}
	if s.PrimaryZone != "" && !seen[s.PrimaryZone] {
		return errors.New("primary tunnel domain must be in the domain list")
	}
	if len(s.TunnelNameservers) > 8 {
		return errors.New("use at most eight tunnel domains")
	}
	for zone, host := range s.TunnelNameservers {
		if !seen[zone] || ValidateDomain(host) != nil {
			return errors.New("invalid tunnel nameserver")
		}
	}
	return nil
}

func (s Settings) nameserverFor(zone string) string {
	if host := s.TunnelNameservers[zone]; host != "" {
		return host
	}
	// Old installations did not store their delegation target. Never publish a
	// guessed NS for their existing zone: recursive caches could replace the
	// working parent delegation with a nonexistent nameserver.
	if zone == s.BaseZone {
		return s.NameserverHost
	}
	root, err := publicsuffix.EffectiveTLDPlusOne(zone)
	if err != nil {
		root = zone
	}
	return "ns1." + root
}
func (s Settings) accountZone(c Client, base string) string {
	label, _, _ := strings.Cut(c.Zone, ".")
	label = label[:min(len(label), 44-len(base))]
	return label + "." + base
}
func (s Settings) accountZones(c Client) []string {
	var zones []string
	// Configuration order, rather than preference, keeps live connections intact
	// when only the preferred domain changes.
	for _, base := range s.tunnelZones() {
		zones = append(zones, s.accountZone(c, base))
	}
	return zones
}
func (s Settings) matchTunnelZone(name string) string {
	for _, base := range s.tunnelZones() {
		if name == base || strings.HasSuffix(name, "."+base) {
			return base
		}
	}
	return ""
}

// A fresh nonce checks the recursive route to this specific panel, rather than
// merely accepting any SOA response (including one from an unrelated DNS server).
func (m *Manager) domainProbeReply(query []byte, name string, typ uint16, base string) []byte {
	if base == "" || typ != 16 {
		return nil
	}
	label := strings.TrimSuffix(name, "."+base)
	if len(label) != 42 || !strings.HasPrefix(label, "_whitedns-") {
		return nil
	}
	if _, err := hex.DecodeString(label[10:]); err != nil {
		return nil
	}
	var request dnsmessage.Message
	if request.Unpack(query) != nil || len(request.Questions) != 1 {
		return nil
	}
	request.Header = dnsmessage.Header{ID: request.ID, Response: true, Authoritative: true, RecursionDesired: request.RecursionDesired}
	request.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: request.Questions[0].Name, Type: dnsmessage.TypeTXT, Class: dnsmessage.ClassINET, TTL: 0}, Body: &dnsmessage.TXTResource{TXT: []string{m.domainProof(label)}}}}
	request.Authorities = nil
	request.Additionals = nil
	out, _ := request.Pack()
	return out
}

func (m *Manager) domainProof(label string) string {
	mac := hmac.New(sha256.New, m.domainProofKey[:])
	mac.Write([]byte(label))
	return hex.EncodeToString(mac.Sum(nil))
}

func checkTunnelRoute(ctx context.Context, zone string, resolvers []string, proof func(string) string) error {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return err
	}
	label := "_whitedns-" + hex.EncodeToString(raw[:])
	name, _ := dnsmessage.NewName(label + "." + zone + ".")
	request := dnsmessage.Message{Header: dnsmessage.Header{ID: uint16(raw[0])<<8 | uint16(raw[1]), RecursionDesired: true}, Questions: []dnsmessage.Question{{Name: name, Type: dnsmessage.TypeTXT, Class: dnsmessage.ClassINET}}}
	query, _ := request.Pack()
	for _, resolver := range resolvers {
		if _, _, err := net.SplitHostPort(resolver); err != nil {
			resolver = net.JoinHostPort(resolver, "53")
		}
		probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		conn, err := (&net.Dialer{}).DialContext(probeCtx, "udp", resolver)
		if err == nil {
			deadline, _ := probeCtx.Deadline()
			conn.SetDeadline(deadline)
			_, err = conn.Write(query)
			if err == nil {
				buf := make([]byte, 4096)
				var n int
				n, err = conn.Read(buf)
				var reply dnsmessage.Message
				if err == nil && reply.Unpack(buf[:n]) == nil && reply.Response && reply.ID == request.ID {
					for _, answer := range reply.Answers {
						if txt, ok := answer.Body.(*dnsmessage.TXTResource); ok && answer.Header.Name == name && len(txt.TXT) == 1 && txt.TXT[0] == proof(label) {
							conn.Close()
							cancel()
							return nil
						}
					}
				}
			}
			conn.Close()
		}
		cancel()
		if ctx.Err() != nil {
			break
		}
	}
	return errors.New("DNS route to this server could not be verified. Check the NS delegation, nameserver A record and UDP/TCP 53. This does not establish whether a resolver or domain is filtered on the user's network")
}

func (h *HTTP) tunnelDomains(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.store.Settings()
	if err != nil {
		errorReply(w, 500, err)
		return
	}
	if r.Method == "GET" {
		nameservers := map[string]string{}
		for _, zone := range cfg.tunnelZones() {
			nameservers[zone] = cfg.nameserverFor(zone)
		}
		jsonReply(w, 200, map[string]any{"zones": cfg.tunnelZones(), "primary": cfg.primaryZone(), "nameservers": nameservers, "public_ip": cfg.PublicIP})
		return
	}
	if r.URL.Path == "/api/tunnel-domains/check" && r.Method == "POST" {
		var in struct {
			Zone string `json:"zone"`
		}
		if err = decodeJSON(w, r, &in); err != nil {
			errorReply(w, 400, err)
			return
		}
		if cfg.matchTunnelZone(in.Zone) != in.Zone {
			errorReply(w, 400, errors.New("save the tunnel domain before checking it"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 17*time.Second)
		defer cancel()
		if err = checkTunnelRoute(ctx, in.Zone, cfg.Resolvers, h.manager.domainProof); err != nil {
			errorReply(w, 400, err)
			return
		}
		jsonReply(w, 200, map[string]bool{"reachable": true})
		return
	}
	if r.Method != "PUT" || r.URL.Path != "/api/tunnel-domains" {
		w.WriteHeader(405)
		return
	}
	var in struct {
		Zones       []string          `json:"zones"`
		Primary     string            `json:"primary"`
		Nameservers map[string]string `json:"nameservers"`
	}
	if err = decodeJSON(w, r, &in); err != nil {
		errorReply(w, 400, err)
		return
	}
	if len(in.Zones) == 0 {
		errorReply(w, 400, errors.New("keep at least one tunnel domain"))
		return
	}
	for i := range in.Zones {
		in.Zones[i] = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(in.Zones[i]), "."))
	}
	cfg.TunnelZones = in.Zones
	previous := cfg.TunnelNameservers
	cfg.TunnelNameservers = map[string]string{}
	for _, zone := range in.Zones {
		host := in.Nameservers[zone]
		if host == "" {
			host = previous[zone]
		}
		if host != "" {
			cfg.TunnelNameservers[zone] = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
		}
	}
	cfg.PrimaryZone = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(in.Primary), "."))
	if err = cfg.Validate(); err != nil {
		errorReply(w, 400, err)
		return
	}
	if err = h.store.SaveSettings(cfg); err != nil {
		errorReply(w, 400, err)
		return
	}
	if err = h.manager.Reconcile(); err != nil {
		errorReply(w, 500, err)
		return
	}
	jsonReply(w, 200, map[string]any{"zones": cfg.tunnelZones(), "primary": cfg.primaryZone()})
}
