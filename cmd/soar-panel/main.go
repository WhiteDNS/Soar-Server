// soar-panel manages independent, unmodified Lantern Soar tunnel instances.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"soar-server-setup/internal/panel"
	"soar-server-setup/internal/panelops"
)

func main() {
	if err := execute(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}
func execute(args []string) error {
	if len(args) == 1 && args[0] == "version" {
		fmt.Println(panel.Version)
		return nil
	}
	if len(args) == 0 {
		return errors.New("usage: soar-panel init|run|admin-reset|backup [options]")
	}
	f := flag.NewFlagSet(args[0], flag.ContinueOnError)
	db := f.String("db", "/var/lib/soar-panel/panel.db", "Private database path")
	zone := f.String("zone", "", "Existing delegated DNS zone (init only)")
	nameserver := f.String("nameserver", "", "Existing NS target (init only)")
	publicIP := f.String("public-ip", "", "Server public IPv4 for DNS instructions (init only)")
	admin := f.String("admin", "admin", "Administrator name")
	passfile := f.String("password-file", "", "Read admin password from protected file (init/admin-reset)")
	country := f.String("country", "IR", "Discovery hint: IR or GLOBAL")
	resolvers := f.String("resolvers", "1.1.1.1,8.8.8.8", "Comma separated bootstrap resolvers")
	bind := f.String("listen", "127.0.0.1:2053", "Management HTTP listen address; use an SSH tunnel or HTTPS proxy")
	insecureHTTP := f.Bool("allow-insecure-http", false, "Explicitly permit unencrypted public HTTP for a test server")
	dns := f.String("dns-listen", ":53", "Authoritative DNS UDP/TCP listen address")
	cert := f.String("tls-cert", "", "HTTPS certificate PEM file")
	key := f.String("tls-key", "", "HTTPS private key PEM file")
	proxy := f.Bool("trust-loopback-proxy", false, "Trust HTTPS forwarding header only from loopback proxy")
	output := f.String("output", "", "New private backup file (backup only)")
	input := f.String("input", "", "Backup to inspect (isolated validation only)")
	accessFile := f.String("access-file", "", "Machine-local access configuration (defaults beside DB)")
	opsSocket := f.String("ops-socket", "/run/soar-panel-ops/control.sock", "Private maintenance service socket")
	domain := f.String("domain", "", "Panel HTTPS domain (configure-access only)")
	email := f.String("email", "", "Optional certificate account email")
	randomPath := f.Bool("random-path", false, "Generate a new private access path")
	probeID := f.String("probe-id", "", "Temporary installation account to remove (offline installer only)")
	updateResolvers := f.Bool("update-resolvers", false, "Save selected bootstrap resolvers when finishing a fresh installation")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	if *accessFile == "" {
		*accessFile = filepath.Join(filepath.Dir(*db), "access.json")
	}
	if args[0] == "inspect-backup" {
		v, e := panel.InspectBackupFile(*input)
		if e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(v)
	}
	if args[0] == "access-url" || args[0] == "health" {
		cfg, e := panel.ReadAccessConfig(*accessFile)
		if e != nil {
			return e
		}
		path := cfg.BasePath
		if path == "" {
			path = "/"
		}
		origin := "http://" + *bind
		if args[0] == "health" {
			client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
			r, e := client.Get(origin + path + "api/health")
			if e != nil {
				return e
			}
			r.Body.Close()
			if r.StatusCode != 204 {
				return fmt.Errorf("panel readiness HTTP %d", r.StatusCode)
			}
			return nil
		}
		if cfg.HTTPS {
			origin = "https://" + cfg.Domain
		}
		fmt.Println(origin + path)
		return nil
	}
	if args[0] == "access-reset" {
		return panel.WritePrivateJSON(*accessFile, panel.AccessConfig{})
	}
	if args[0] == "configure-access" {
		a, e := panel.NewAccessController(*accessFile)
		if e != nil {
			return e
		}
		defer a.Close()
		if e = a.Start(http.NotFoundHandler()); e != nil {
			return e
		}
		cfg := a.Config()
		host := *domain
		if host == "" {
			host = cfg.Domain
		}
		mail := *email
		if mail == "" {
			mail = cfg.Email
		}
		aliases := []string{}
		if host == cfg.Domain {
			aliases = append(aliases, cfg.Aliases...)
		}
		plan, e := a.Prepare("http://"+*bind, "terminal", panel.AccessInput{Domain: host, Aliases: aliases, HTTPS: host != "", Email: mail, GeneratePath: *randomPath, BasePath: cfg.BasePath}, panel.NewOpsClient(*opsSocket).AllowHTTPS)
		if e != nil {
			return e
		}
		deadline := time.NewTimer(5 * time.Minute)
		defer deadline.Stop()
		for {
			plan = a.Plan()
			if plan.State == "failed" {
				return errors.New(plan.Error)
			}
			if plan.State == "ready" {
				url, e := a.Apply(plan.ID, "terminal")
				if e == nil {
					fmt.Println(url)
				}
				return e
			}
			select {
			case <-deadline.C:
				return errors.New("certificate setup timed out; previous address retained")
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
	switch args[0] {
	case "init", "run", "admin-reset", "backup", "zone", "create-install-probe", "remove-install-probe":
	default:
		return errors.New("unknown command")
	}
	if args[0] == "run" {
		if err := validateManagementListen(*bind, *cert, *key, *insecureHTTP); err != nil {
			return err
		}
	}
	s, err := panel.OpenStore(*db)
	if err != nil {
		return err
	}
	defer s.Close()
	if args[0] == "init" || args[0] == "admin-reset" {
		if *passfile == "" {
			return errors.New("--password-file is required; passwords must not be passed on the command line")
		}
		stat, err := os.Stat(*passfile)
		if err != nil {
			return err
		}
		if !stat.Mode().IsRegular() || stat.Size() > 256 {
			return errors.New("invalid password file")
		}
		if stat.Mode().Perm()&0077 != 0 && os.PathSeparator != '\\' {
			return errors.New("password file must have mode 0600")
		}
		raw, err := os.ReadFile(*passfile)
		if err != nil {
			return err
		}
		password := strings.TrimRight(string(raw), "\r\n")
		if args[0] == "admin-reset" {
			if !s.Initialized() {
				return errors.New("database not initialized")
			}
			return s.ResetAdmin(*admin, password)
		}
		if *country == "GLOBAL" {
			*country = ""
		}
		rs := strings.Split(*resolvers, ",")
		for i := range rs {
			rs[i] = strings.TrimSpace(rs[i])
		}
		err = s.Init(panel.Settings{BaseZone: *zone, NameserverHost: *nameserver, PublicIP: *publicIP, Resolvers: rs, Country: *country, AllowPorts: []uint16{80, 443}}, *admin, password)
		if err == nil {
			fmt.Println("Panel initialized. Keep the database private; it contains account keys.")
		}
		return err
	}
	if !s.Initialized() {
		return errors.New("database not initialized; run init first")
	}
	if args[0] == "create-install-probe" {
		c, err := s.CreateInstallProbe()
		if err == nil && *output != "" {
			err = s.WriteInstallCottenProbe(c.ID, *output)
		}
		if err == nil {
			fmt.Printf("%s %s %s %d\n", c.ID, c.Zone, c.PublicKey, c.ExpiresAt/1000)
		}
		return err
	}
	if args[0] == "remove-install-probe" {
		if err := s.RemoveInstallProbe(*probeID); err != nil {
			return err
		}
		if *updateResolvers {
			cfg, err := s.Settings()
			if err != nil {
				return err
			}
			cfg.Resolvers = strings.Split(*resolvers, ",")
			return s.SaveSettings(cfg)
		}
		return nil
	}
	if args[0] == "zone" {
		cfg, e := s.Settings()
		if e == nil {
			fmt.Println(cfg.BaseZone)
		}
		return e
	}
	if args[0] == "backup" {
		if *output == "" {
			return errors.New("--output is required")
		}
		file, err := os.OpenFile(*output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		err = s.Backup(file)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		return closeErr
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Lantern's public-destination guard enumerates local interface addresses.
	// Fail with an actionable error instead of accepting handshakes that cannot
	// reach any website when systemd accidentally denies AF_NETLINK.
	if _, err = net.InterfaceAddrs(); err != nil {
		return fmt.Errorf("cannot enumerate local addresses; Linux service must permit AF_NETLINK: %w", err)
	}
	pc, tcpDNS, err := listenDNS(*dns)
	if err != nil {
		return fmt.Errorf("DNS listener: %w", err)
	}
	defer pc.Close()
	defer tcpDNS.Close()
	m := panel.NewManager(ctx, s, pc)
	if err = m.Reconcile(); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", *bind)
	if err != nil {
		return fmt.Errorf("panel listener: %w", err)
	}
	defer listener.Close()
	handler := panel.NewHTTP(s, m, *proxy)
	handler.SetMaintenance(panel.NewOpsClient(*opsSocket), func(ctx context.Context, path string) (panel.BackupSummary, error) {
		binary, e := os.Executable()
		if e != nil {
			return panel.BackupSummary{}, e
		}
		return panelops.InspectChild(ctx, binary, path)
	})
	access, e := panel.NewAccessController(*accessFile)
	if e != nil {
		return e
	}
	handler.SetAccess(access)
	if e = access.Start(handler); e != nil {
		return e
	}
	defer access.Close()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 90 * time.Second, WriteTimeout: 90 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	done := make(chan error, 3)
	go func() { done <- m.Run() }()
	go func() { done <- m.ServeTCP(tcpDNS) }()
	go func() {
		if *cert != "" {
			done <- server.ServeTLS(listener, *cert, *key)
		} else {
			done <- server.Serve(listener)
		}
	}()
	log.Printf("Soar panel beta: management=%s DNS=%s", listener.Addr(), pc.LocalAddr())
	remaining := 3
	select {
	case <-ctx.Done():
	case err = <-done:
		remaining--
	}
	stop()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
	for remaining > 0 {
		select {
		case next := <-done:
			remaining--
			if err == nil && !errors.Is(next, http.ErrServerClosed) {
				err = next
			}
		case <-shutdown.Done():
			return errors.New("shutdown timed out")
		}
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func validateManagementListen(bind, cert, key string, allowHTTP bool) error {
	host, _, err := net.SplitHostPort(bind)
	if err != nil {
		return err
	}
	if (cert == "") != (key == "") {
		return errors.New("provide both TLS certificate and key")
	}
	ip := net.ParseIP(host)
	if (ip == nil || !ip.IsLoopback()) && cert == "" && !allowHTTP {
		return errors.New("public management requires TLS or explicit --allow-insecure-http for a test server; otherwise use loopback with SSH or an HTTPS proxy")
	}
	return nil
}
