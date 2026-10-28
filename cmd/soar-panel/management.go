package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"soar-server-setup/internal/panel"
)

// Interactive management stays separate from the daemon and its public API.
func managementCommand(args []string) (bool, error) {
	if len(args) == 0 || (len(args) == 1 && args[0] == "menu") {
		if runtime.GOOS != "linux" {
			return true, errors.New("the server menu requires Linux; use init, run, admin-reset or backup")
		}
		cmd := exec.Command("/usr/local/bin/soar-panel-menu")
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return true, cmd.Run()
	}
	switch args[0] {
	case "maintenance", "maintenance-status", "latest-release", "server-info", "configure-tunnels", "configure-network":
	default:
		return false, nil
	}
	f := flag.NewFlagSet(args[0], flag.ContinueOnError)
	db := f.String("db", "/var/lib/soar-panel/panel.db", "Stopped panel database")
	socket := f.String("ops-socket", "/run/soar-panel-ops/control.sock", "Private maintenance socket")
	action := f.String("action", "", "update, restore or restart")
	version := f.String("version", "", "Official update version")
	input := f.String("input", "", "Validated .db backup for restore")
	zones := f.String("zones", "", "Comma separated tunnel domains")
	primary := f.String("primary", "", "Preferred tunnel domain")
	nameservers := f.String("nameservers", "", "Comma separated zone=nameserver pairs")
	resolvers := f.String("resolvers", "", "Comma separated resolver addresses")
	ports := f.String("ports", "", "Comma separated allowed destination ports")
	country := f.String("country", "", "IR or GLOBAL")
	publicIP := f.String("public-ip", "", "Public IPv4 for DNS instructions")
	if err := f.Parse(args[1:]); err != nil {
		return true, err
	}
	if f.NArg() != 0 {
		return true, errors.New("unexpected arguments")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	encode := func(v any) (bool, error) { return true, json.NewEncoder(os.Stdout).Encode(v) }
	if args[0] == "latest-release" {
		r, err := panel.LatestPanelRelease(ctx)
		if err != nil {
			return true, err
		}
		fmt.Println(strings.TrimPrefix(r.Tag, "panel-v"))
		return true, nil
	}
	if args[0] == "maintenance-status" {
		job, err := panel.NewOpsClient(*socket).Status(ctx)
		if err != nil {
			return true, err
		}
		return encode(job)
	}
	if args[0] == "maintenance" {
		r := panel.MaintenanceRequest{Action: *action, Version: *version}
		if *action == "restore" {
			summary, err := panel.InspectBackupFile(*input)
			if err != nil {
				return true, err
			}
			r.Backup, err = os.ReadFile(*input)
			if err != nil {
				return true, err
			}
			r.SHA256, r.ExpectedZone = summary.SHA256, summary.BaseZone
		}
		ops := panel.NewOpsClient(*socket)
		job, err := ops.Start(ctx, r)
		if err != nil {
			return true, err
		}
		fmt.Printf("Maintenance job: %s\n", job.ID)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return true, errors.New("timed out waiting; inspect maintenance-status before retrying")
			case <-ticker.C:
				status, err := ops.Status(ctx)
				if err != nil { // The helper restarts after an update.
					continue
				}
				if status.ID != job.ID {
					return true, errors.New("maintenance job changed; inspect maintenance-status")
				}
				if status.State == "running" {
					continue
				}
				_, _ = encode(status)
				if status.State != "succeeded" {
					return true, errors.New(status.Message)
				}
				return true, nil
			}
		}
	}
	// Offline commands never create a database by accident.
	if info, err := os.Stat(*db); err != nil || !info.Mode().IsRegular() {
		return true, errors.New("existing panel database required; stop the service before using this command")
	}
	s, err := panel.OpenStore(*db)
	if err != nil {
		return true, err
	}
	defer s.Close()
	if !s.Initialized() {
		return true, errors.New("database not initialized")
	}
	if args[0] == "server-info" {
		info, err := s.ServerManagementInfo()
		if err != nil {
			return true, err
		}
		return encode(info)
	}
	err = s.ConfigureServer(panel.ServerConfiguration{TunnelDomains: *zones, Primary: *primary, Nameservers: *nameservers, Resolvers: *resolvers, Ports: *ports, Country: *country, PublicIP: *publicIP}, args[0] == "configure-tunnels")
	return true, err
}

func normalizedDomains(raw string) []string {
	domains := strings.Split(raw, ",")
	for i := range domains {
		domains[i] = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domains[i]), "."))
	}
	return domains
}
