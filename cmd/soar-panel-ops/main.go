package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"soar-server-setup/internal/panel"
	"soar-server-setup/internal/panelops"
	"strings"
	"time"
)

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "firewall-dns" || strings.HasPrefix(os.Args[1], "firewall-dns:") || os.Args[1] == "firewall-https") {
		if runtime.GOOS != "linux" || os.Geteuid() != 0 {
			log.Fatal("firewall preparation requires Linux root")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		kind := "dns"
		if strings.HasPrefix(os.Args[1], "firewall-dns:") {
			kind = strings.TrimPrefix(os.Args[1], "firewall-")
		}
		if os.Args[1] == "firewall-https" {
			kind = "https"
		}
		if err := panelops.RunFirewallAction(ctx, kind); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "version" {
		fmt.Println(panel.Version)
		return
	}
	if e := run(); e != nil {
		log.Fatal(e)
	}
}
func run() error {
	socket := flag.String("socket", "/run/soar-panel-ops/control.sock", "Private Unix socket")
	db := flag.String("db", "/var/lib/soar-panel/panel.db", "Fixed panel database")
	binary := flag.String("binary", "/usr/local/bin/soar-panel", "Fixed panel binary")
	opsBinary := flag.String("ops-binary", "/usr/local/bin/soar-panel-ops", "Fixed maintenance binary")
	securityScript := flag.String("security-script", "/usr/local/bin/soar-panel-security", "Fixed terminal security wizard")
	state := flag.String("state", "/var/lib/soar-panel-ops", "Private job/rollback directory")
	health := flag.String("health-address", "127.0.0.1:2053", "Fixed local management listener")
	dnsPort := flag.Int("dns-port", 53, "DNS firewall port from the root-owned service configuration")
	flag.Parse()
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return fmt.Errorf("maintenance service requires Linux root; it exposes only a private group-restricted Unix socket")
	}
	if err := installManagementMenu("/usr/local/bin/soar-panel-menu", "/usr/local/bin/whitedns"); err != nil {
		return fmt.Errorf("install server menu: %w", err)
	}
	owner, e := user.Lookup("soar-panel")
	if e != nil {
		return e
	}
	uid, e := panelops.ParseID(owner.Uid)
	if e != nil {
		return e
	}
	gid, e := panelops.ParseID(owner.Gid)
	if e != nil {
		return e
	}
	service := &panelops.Service{DB: *db, Binary: *binary, OpsBinary: *opsBinary, SecurityScript: *securityScript, MenuScript: "/usr/local/bin/soar-panel-menu", LockFile: filepath.Join(*state, "maintenance.lock"), StateDir: *state, HealthAddress: *health, UID: uid, GID: gid}
	firewallCtx, cancelFirewall := context.WithTimeout(context.Background(), 15*time.Second)
	e = panelops.PrepareFirewall(firewallCtx, *opsBinary, fmt.Sprintf("dns:%d", *dnsPort))
	cancelFirewall()
	if e != nil {
		return e
	}
	if e = service.Initialize(); e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(*socket), 0750); e != nil {
		return e
	}
	if e = os.Chown(filepath.Dir(*socket), 0, gid); e != nil {
		return e
	}
	_ = os.Remove(*socket)
	listener, e := net.Listen("unix", *socket)
	if e != nil {
		return e
	}
	defer listener.Close()
	if e = os.Chown(*socket, 0, gid); e != nil {
		return e
	}
	if e = os.Chmod(*socket, 0660); e != nil {
		return e
	}
	return (&http.Server{Handler: service, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, MaxHeaderBytes: 8192}).Serve(listener)
}
