// soar-panel manages independent, unmodified Lantern Soar tunnel instances.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"soar-server-setup/internal/panel"
)

func main() {
	if err := execute(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}
func execute(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: soar-panel init|run|admin-reset|backup [options]")
	}
	f := flag.NewFlagSet(args[0], flag.ContinueOnError)
	db := f.String("db", "/var/lib/soar-panel/panel.db", "Private database path")
	zone := f.String("zone", "", "Existing delegated DNS zone (init only)")
	admin := f.String("admin", "admin", "Administrator name")
	passfile := f.String("password-file", "", "Read admin password from protected file (init/admin-reset)")
	country := f.String("country", "IR", "Discovery hint: IR or GLOBAL")
	resolvers := f.String("resolvers", "1.1.1.1,8.8.8.8", "Comma separated bootstrap resolvers")
	bind := f.String("listen", "127.0.0.1:2053", "Management HTTP listen address; use an SSH tunnel or HTTPS proxy")
	dns := f.String("dns-listen", ":53", "Authoritative DNS UDP listen address")
	cert := f.String("tls-cert", "", "HTTPS certificate PEM file")
	key := f.String("tls-key", "", "HTTPS private key PEM file")
	proxy := f.Bool("trust-loopback-proxy", false, "Trust HTTPS forwarding header only from loopback proxy")
	output := f.String("output", "", "New private backup file (backup only)")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	switch args[0] {
	case "init", "run", "admin-reset", "backup", "zone":
	default:
		return errors.New("unknown command")
	}
	if args[0] == "run" {
		host, _, err := net.SplitHostPort(*bind)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if (ip == nil || !ip.IsLoopback()) && (*cert == "" || *key == "") {
			return errors.New("public management access requires --tls-cert and --tls-key; otherwise bind to 127.0.0.1 and use an SSH tunnel or HTTPS proxy")
		}
		if (*cert == "") != (*key == "") {
			return errors.New("provide both TLS certificate and key")
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
		err = s.Init(panel.Settings{BaseZone: *zone, Resolvers: rs, Country: *country, AllowPorts: []uint16{80, 443}}, *admin, password)
		if err == nil {
			fmt.Println("Panel initialized. Keep the database private; it contains account keys.")
		}
		return err
	}
	if !s.Initialized() {
		return errors.New("database not initialized; run init first")
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
	pc, err := net.ListenPacket("udp", *dns)
	if err != nil {
		return fmt.Errorf("DNS listener: %w", err)
	}
	defer pc.Close()
	m := panel.NewManager(ctx, s, pc)
	if err = m.Reconcile(); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", *bind)
	if err != nil {
		return fmt.Errorf("panel listener: %w", err)
	}
	defer listener.Close()
	server := &http.Server{Handler: panel.NewHTTP(s, m, *proxy), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	done := make(chan error, 2)
	go func() { done <- m.Run() }()
	go func() {
		if *cert != "" {
			done <- server.ServeTLS(listener, *cert, *key)
		} else {
			done <- server.Serve(listener)
		}
	}()
	log.Printf("Soar panel beta: management=%s DNS=%s", listener.Addr(), pc.LocalAddr())
	remaining := 2
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
