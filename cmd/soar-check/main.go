// soar-check is an installation diagnostic, not an application proxy.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"

	cottenprobe "cottendns-go/probe"
	"github.com/getlantern/soar"
)

const version = "0.1.0"

type result struct {
	OK         bool   `json:"ok"`
	Stage      string `json:"stage"`
	Handshake  bool   `json:"handshake"`
	HTTP       int    `json:"http_status,omitempty"`
	Error      string `json:"error,omitempty"`
	ErrorClass string `json:"error_class,omitempty"`
}

func resolverList(raw string) ([]string, error) {
	var out []string
	for _, value := range strings.Split(raw, ",") {
		value = strings.TrimSpace(value)
		if ip, err := netip.ParseAddr(value); err == nil {
			out = append(out, net.JoinHostPort(ip.String(), "53"))
			continue
		}
		ap, err := netip.ParseAddrPort(value)
		if err != nil || ap.Port() == 0 {
			return nil, fmt.Errorf("resolver must be an IP address or IP:port")
		}
		out = append(out, ap.String())
	}
	return out, nil
}

func targetURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return nil, fmt.Errorf("test URL must be an HTTPS URL without credentials or a fragment")
	}
	return u, nil
}

func check(ctx context.Context, cfg soar.Config, target string, roots *x509.CertPool) result {
	r := result{Stage: "configuration"}
	u, err := targetURL(target)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	c, err := soar.NewClient(cfg)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	defer c.Close()
	r.Stage = "tunnel"
	tr := &http.Transport{
		DialContext:           c.DialContext,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots},
		TLSHandshakeTimeout:   12 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		DisableKeepAlives:     true,
		// Proxy is deliberately nil: the request must use Soar, not an environment proxy.
	}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	req.Header.Set("User-Agent", "soar-server-check/"+version)
	resp, err := hc.Do(req)
	r.Handshake = c.Established()
	if err != nil {
		if r.Handshake {
			r.Stage = "https"
		}
		// Do not include the URL, zone, keys or resolver addresses in error output.
		if ctx.Err() != nil {
			r.Error = "test timed out; check DNS delegation, UDP/53 and resolver reachability"
		} else if r.Handshake {
			r.Error = "server authenticated, but HTTPS failed; check outbound DNS, web access and CA certificates"
		} else {
			r.Error = "no authenticated tunnel; check server key, delegation, UDP/53 and resolver reachability"
		}
		return r
	}
	defer resp.Body.Close()
	r.Stage, r.HTTP = "https", resp.StatusCode
	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, 1024)); err != nil {
		r.Error = "HTTPS response could not be read"
		return r
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		r.Error = "test destination returned an unsuccessful HTTP status"
		return r
	}
	r.OK, r.Stage = true, "complete"
	return r
}

func run(args []string, output io.Writer) int {
	fs := flag.NewFlagSet("soar-check", flag.ContinueOnError)
	fs.SetOutput(output)
	zone := fs.String("zone", "", "delegated tunnel domain")
	pubkey := fs.String("pubkey", "", "base64 server public key (never the private key)")
	cottenFile := fs.String("cotten-profile-file", "", "Private installer-only Cotten probe credentials; never put a shared key in argv")
	resolvers := fs.String("resolver", "", "comma-separated resolver IPs or IP:port values")
	target := fs.String("url", "https://example.com/", "HTTPS destination for the tunnel test")
	tcp := fs.String("tcp", "auto", "resolver transport: auto, on, only or off")
	timeout := fs.Duration("timeout", 45*time.Second, "overall test timeout (1s to 2m)")
	asJSON := fs.Bool("json", false, "print a structured result")
	showVersion := fs.Bool("version", false, "print diagnostic version")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Fprintln(output, "soar-check "+version+"; Soar v0.2.2")
		return 0
	}
	key, err := base64.StdEncoding.DecodeString(*pubkey)
	if (*cottenFile == "" && (err != nil || len(key) != ed25519.PublicKeySize || *zone == "")) || fs.NArg() != 0 {
		fmt.Fprintln(output, "[ERROR] Supply -zone, -pubkey and -resolver. Use -help for examples.")
		return 2
	}
	if *tcp != "auto" && *tcp != "on" && *tcp != "only" && *tcp != "off" {
		fmt.Fprintln(output, "[ERROR] -tcp must be auto, on, only or off.")
		return 2
	}
	if *timeout < time.Second || *timeout > 2*time.Minute {
		fmt.Fprintln(output, "[ERROR] -timeout must be between 1s and 2m.")
		return 2
	}
	list, err := resolverList(*resolvers)
	if err != nil {
		fmt.Fprintln(output, "[ERROR] "+err.Error())
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	var r result
	if *cottenFile != "" {
		r = result{Stage: "cotten tunnel"}
		info, e := os.Stat(*cottenFile)
		if e != nil || !info.Mode().IsRegular() || info.Size() > 4096 || os.PathSeparator != '\\' && info.Mode().Perm()&0077 != 0 {
			r.Error = "Cotten probe file must be private (0600)"
		} else {
			raw, e := os.ReadFile(*cottenFile)
			var cfg cottenprobe.Config
			if e != nil || json.Unmarshal(raw, &cfg) != nil {
				r.Error = "invalid Cotten probe file"
			} else {
				cfg.Resolvers, cfg.URL, cfg.Transport = list, *target, "auto"
				if *tcp == "off" {
					cfg.Transport = "udp"
				} else if *tcp == "only" {
					cfg.Transport = "tcp"
				}
				detailed, e := cottenprobe.CheckHTTPDetailed(ctx, cfg)
				r.HTTP, r.Handshake, r.Stage, r.ErrorClass = detailed.HTTPStatus, detailed.Handshake, detailed.Stage, detailed.ErrorClass
				if e != nil {
					r.Error = e.Error()
				} else {
					r.OK, r.Stage = detailed.OK, "complete"
				}
			}
		}
	} else {
		r = check(ctx, soar.Config{Zone: *zone, ServerKey: key, Resolvers: list, TCP: *tcp}, *target, nil)
	}
	if *asJSON {
		_ = json.NewEncoder(output).Encode(r)
	} else if r.OK {
		fmt.Fprintf(output, "[OK] Server authenticated. HTTPS through the tunnel succeeded (HTTP %d).\n", r.HTTP)
	} else {
		fmt.Fprintf(output, "[ERROR] Stage: %s. %s\n", r.Stage, r.Error)
	}
	if !r.OK {
		return 1
	}
	return 0
}

func main() { os.Exit(run(os.Args[1:], os.Stdout)) }
