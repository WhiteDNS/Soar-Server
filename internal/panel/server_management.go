package panel

import (
	"errors"
	"strconv"
	"strings"
)

type ServerConfiguration struct {
	TunnelDomains, Primary, Nameservers, Resolvers, Ports, Country, PublicIP string
}

// ServerManagementInfo omits password hashes, account keys and provider secrets.
func (s *Store) ServerManagementInfo() (any, error) {
	cfg, err := s.Settings()
	if err != nil {
		return nil, err
	}
	admin, err := s.admin()
	if err != nil {
		return nil, err
	}
	clients, err := s.List()
	if err != nil {
		return nil, err
	}
	return struct {
		Administrator string   `json:"administrator"`
		Accounts      int      `json:"accounts"`
		Settings      Settings `json:"settings"`
	}{admin.Name, len(clients), cfg}, nil
}

func (s *Store) ConfigureServer(in ServerConfiguration, tunnels bool) error {
	cfg, err := s.Settings()
	if err != nil {
		return err
	}
	if tunnels {
		if strings.TrimSpace(in.TunnelDomains) == "" {
			return errors.New("keep at least one tunnel domain")
		}
		cfg.TunnelZones = normalizedCSV(in.TunnelDomains)
		cfg.PrimaryZone = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(in.Primary), "."))
		if cfg.PrimaryZone == "" {
			cfg.PrimaryZone = cfg.TunnelZones[0]
		}
		previous := cfg.TunnelNameservers
		cfg.TunnelNameservers = map[string]string{}
		for _, zone := range cfg.TunnelZones {
			if previous[zone] != "" {
				cfg.TunnelNameservers[zone] = previous[zone]
			}
		}
		if in.Nameservers != "" {
			for _, pair := range normalizedCSV(in.Nameservers) {
				zone, host, ok := strings.Cut(pair, "=")
				if !ok || host == "" {
					return errors.New("nameservers must use zone=hostname pairs")
				}
				cfg.TunnelNameservers[zone] = host
			}
		}
	} else {
		if in.Resolvers != "" {
			cfg.Resolvers = normalizedCSV(in.Resolvers)
		}
		if in.Ports != "" {
			cfg.AllowPorts = nil
			for _, raw := range normalizedCSV(in.Ports) {
				port, err := strconv.ParseUint(raw, 10, 16)
				if err != nil || port == 0 {
					return errors.New("destination ports must be numbers from 1 to 65535")
				}
				cfg.AllowPorts = append(cfg.AllowPorts, uint16(port))
			}
		}
		if in.Country != "" {
			cfg.Country = in.Country
			if in.Country == "GLOBAL" {
				cfg.Country = ""
			}
		}
		if in.PublicIP != "" {
			cfg.PublicIP = strings.TrimSpace(in.PublicIP)
		}
	}
	// SaveSettings validates everything before committing, preserving BaseZone,
	// account identities, keys, subscriptions, usage and all unrelated settings.
	return s.SaveSettings(cfg)
}

func normalizedCSV(value string) []string {
	parts := strings.Split(value, ",")
	for i := range parts {
		parts[i] = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(parts[i]), "."))
	}
	return parts
}
