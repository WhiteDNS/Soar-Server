package panel

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ListenerConfig describes the actual running listener. It contains public
// certificates only; private keys and account credentials never enter this file.
type ListenerConfig struct {
	Management string `json:"management"`
	DNS        string `json:"dns"`
	TLS        bool   `json:"tls"`
	ServerName string `json:"server_name,omitempty"`
	PublicCert string `json:"public_certificate,omitempty"`
}

func WriteListenerConfig(db, management, dns, certFile, serverName string) error {
	c := ListenerConfig{Management: management, DNS: dns, TLS: certFile != "", ServerName: serverName}
	if c.TLS {
		raw, err := os.ReadFile(certFile)
		if err != nil {
			return err
		}
		if len(raw) > 64<<10 {
			return errors.New("certificate chain is too large")
		}
		for len(raw) > 0 {
			block, rest := pem.Decode(raw)
			if block == nil {
				break
			}
			raw = rest
			if block.Type != "CERTIFICATE" {
				continue
			}
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return err
			}
			if c.PublicCert == "" && c.ServerName == "" {
				if len(cert.DNSNames) > 0 {
					c.ServerName = cert.DNSNames[0]
					if strings.HasPrefix(c.ServerName, "*.") {
						c.ServerName = "panel." + strings.TrimPrefix(c.ServerName, "*.")
					}
				} else if len(cert.IPAddresses) > 0 {
					c.ServerName = cert.IPAddresses[0].String()
				}
			}
			c.PublicCert += string(pem.EncodeToMemory(block))
		}
		if c.PublicCert == "" || c.ServerName == "" {
			return errors.New("TLS certificate needs a DNS/IP subject alternative name or --domain")
		}
	}
	return WritePrivateJSON(filepath.Join(filepath.Dir(db), "listener.json"), c)
}

func ReadListenerConfig(db string) (c ListenerConfig, found bool, err error) {
	f, err := os.Open(filepath.Join(filepath.Dir(db), "listener.json"))
	if os.IsNotExist(err) {
		return c, false, nil
	}
	if err != nil {
		return c, false, err
	}
	defer f.Close()
	err = json.NewDecoder(io.LimitReader(f, 96<<10)).Decode(&c)
	return c, err == nil, err
}

func localListenerAddress(address string) (string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", err
	}
	ip := net.ParseIP(host)
	if host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
		if ip != nil && ip.To4() == nil {
			host = "::1"
		}
		return net.JoinHostPort(host, port), nil
	}
	if ip == nil {
		return "", errors.New("local listener requires an IP address")
	}
	if ip.IsLoopback() {
		return address, nil
	}
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}
	for _, address := range addresses {
		if network, ok := address.(*net.IPNet); ok && network.IP.Equal(ip) {
			return net.JoinHostPort(host, port), nil
		}
	}
	return "", errors.New("health listener address is not assigned to this server")
}

func managementHealthRequest(ctx context.Context, db, fallback string) (*http.Request, *http.Transport, error) {
	cfg, err := ReadAccessConfig(filepath.Join(filepath.Dir(db), "access.json"))
	if err != nil {
		return nil, nil, err
	}
	live, found, err := ReadListenerConfig(db)
	if err != nil {
		return nil, nil, err
	}
	address := fallback
	if found {
		address = live.Management
	}
	address, err = localListenerAddress(address)
	if err != nil {
		return nil, nil, err
	}
	transport := &http.Transport{Proxy: nil}
	origin := "http://" + address
	if found && live.TLS {
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if live.ServerName == "" || !roots.AppendCertsFromPEM([]byte(live.PublicCert)) {
			return nil, nil, errors.New("invalid public listener certificate")
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: roots, ServerName: live.ServerName, MinVersion: tls.VersionTLS12}
		origin = "https://" + address
	} else if cfg.HTTPS {
		// Built-in ACME intentionally serves HTTPS on 443 independently of the
		// private management HTTP port. Manual TLS uses its actual port above.
		host, _, _ := net.SplitHostPort(address)
		target := net.JoinHostPort(host, "443")
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", target)
		}
		origin = "https://" + cfg.Domain
	}
	prefix := cfg.BasePath
	if prefix == "" {
		prefix = "/"
	}
	req, err := http.NewRequestWithContext(ctx, "GET", origin+prefix+"api/health", nil)
	return req, transport, err
}

func CheckManagementHealth(ctx context.Context, db, fallback string) error {
	req, transport, err := managementHealthRequest(ctx, db, fallback)
	if err != nil {
		return err
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("panel readiness HTTP %d", response.StatusCode)
	}
	return nil
}
