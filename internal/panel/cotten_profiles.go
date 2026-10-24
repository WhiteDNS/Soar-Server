package panel

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

func (c Client) hasEngine(engine string) bool {
	for _, selected := range engineSelections[c.Engines] {
		if selected == engine {
			return true
		}
	}
	return false
}

func (c Client) cottenZone() string    { return "cot." + c.Zone }
func (c Client) masterDNSZone() string { return "mdns-1." + c.Zone }

func masterDNSKey(seed []byte) string {
	mac := hmac.New(sha256.New, seed)
	mac.Write([]byte("WhiteDNS MasterDNS account key v1"))
	return hex.EncodeToString(mac.Sum(nil))
}

// Derivation keeps keys distinct without a second secret table. Existing full
// backups already contain the seed, and rotating it revokes both engine keys.
func cottenKey(seed []byte) string {
	mac := hmac.New(sha256.New, seed)
	mac.Write([]byte("WhiteDNS CottenDNS account key v1"))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Store) profiles(c Client, cfg Settings) (map[string]string, error) {
	return s.profilesAt(c, cfg, cfg.primaryZone())
}

func (s *Store) profilesAt(c Client, cfg Settings, base string) (map[string]string, error) {
	original := c.Zone
	c.Zone = cfg.accountZone(c, base)
	if c.Zone != original {
		suffix := sha256.Sum256([]byte(base))
		c.ID += ":" + hex.EncodeToString(suffix[:6])
		c.Name += " · " + base
	}
	links := make(map[string]string)
	if c.hasEngine("soar") {
		link, err := profileFor(c, cfg)
		if err != nil {
			return nil, err
		}
		links["soar"] = link
	}
	if c.hasEngine("cotten") {
		id, _, _ := strings.Cut(c.ID, ":")
		seed, err := s.Seed(id)
		if err != nil {
			return nil, err
		}
		body := map[string]any{"schema": "whitedns.profile", "version": 1, "profile": map[string]any{
			"id": c.ID + ":cotten", "name": c.Name + " · Cotten", "engine": "cottendns",
			"server":    map[string]any{"domain": c.cottenZone(), "encryption_key": cottenKey(seed), "encryption_method": 5},
			"resolvers": cfg.Resolvers,
		}}
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		links["cotten"] = "cottendns://" + base64.RawURLEncoding.EncodeToString(raw)
	}
	if c.hasEngine("masterdns") {
		id, _, _ := strings.Cut(c.ID, ":")
		seed, err := s.Seed(id)
		if err != nil {
			return nil, err
		}
		body := map[string]any{"schema": "whitedns.profile", "version": 1, "profile": map[string]any{
			"id": c.ID + ":masterdns", "name": c.Name + " · MasterDNS", "engine": "masterdns",
			"server": map[string]any{"domain": c.masterDNSZone(), "encryption_key": masterDNSKey(seed), "encryption_method": 5},
		}}
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		links["masterdns"] = "masterdns://" + base64.RawURLEncoding.EncodeToString(raw)
	}
	return links, nil
}

func (s *Store) Profiles(id string) (map[string]string, error) {
	c, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	cfg, err := s.Settings()
	if err != nil {
		return nil, err
	}
	return s.profiles(c, cfg)
}

func selectedProfile(links map[string]string, engine string) (string, error) {
	if engine == "" {
		for _, candidate := range profileEngines {
			if links[candidate] != "" {
				engine = candidate
				break
			}
		}
	}
	if link := links[engine]; link != "" {
		return link, nil
	}
	return "", errors.New("this engine is not enabled for the account")
}
