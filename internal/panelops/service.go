// Package panelops implements the fixed, local maintenance operations. It never
// accepts shell commands, arbitrary download hosts, destinations or service names.
package panelops

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"soar-server-setup/internal/panel"
)

type Service struct {
	DB, Binary, OpsBinary, SecurityScript, StateDir, HealthAddress string
	UID, GID                                                       int
	mu                                                             sync.Mutex
	job                                                            panel.MaintenanceJob
	// Dependency seams permit failure/rollback tests without touching systemd.
	Control func(context.Context, string) error
	Health  func(context.Context) error
	Inspect func(context.Context, string) (panel.BackupSummary, error)
	Fetch   func(context.Context, string, string) error
}

func (s *Service) statusPath() string { return filepath.Join(s.StateDir, "job.json") }
func (s *Service) Save(job panel.MaintenanceJob) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.job = job
	_ = panel.WritePrivateJSON(s.statusPath(), job)
}
func (s *Service) Status() panel.MaintenanceJob { s.mu.Lock(); defer s.mu.Unlock(); return s.job }
func (s *Service) Initialize() error {
	if e := os.MkdirAll(s.StateDir, 0700); e != nil {
		return e
	}
	if raw, e := os.ReadFile(s.statusPath()); e == nil {
		if e = json.Unmarshal(raw, &s.job); e != nil {
			return e
		}
	}
	if s.Control == nil {
		s.Control = systemControl
	}
	if s.Health == nil {
		s.Health = s.checkHealth
	}
	if s.Inspect == nil {
		s.Inspect = s.inspect
	}
	if s.Fetch == nil {
		s.Fetch = s.fetchBundle
	}
	if s.job.State == "running" {
		job := s.job
		if job.Backup != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			if e := s.rollback(ctx, job.Backup); e != nil {
				job.State = "failed"
				job.Message = "Interrupted maintenance; automatic recovery failed. Use the private backup."
			} else {
				job.State = "rolled_back"
				job.Message = "Interrupted maintenance was rolled back."
			}
		} else {
			job.State = "failed"
			job.Message = "Interrupted before activation; previous installation retained."
		}
		job.Finished = time.Now().UnixMilli()
		s.Save(job)
	}
	return nil
}
func systemControl(ctx context.Context, action string) error {
	switch action {
	case "stop", "start":
		return exec.CommandContext(ctx, "systemctl", action, "soar-panel.service").Run()
	case "restart-ops":
		return exec.CommandContext(ctx, "systemctl", "--no-block", "try-restart", "soar-panel-ops.service").Run()
	}
	return errors.New("invalid maintenance service action")
}
func (s *Service) checkHealth(ctx context.Context) error {
	cfg, e := panel.ReadAccessConfig(filepath.Join(filepath.Dir(s.DB), "access.json"))
	if e != nil {
		return e
	}
	prefix := cfg.BasePath
	if prefix == "" {
		prefix = "/"
	}
	request, e := http.NewRequestWithContext(ctx, "GET", "http://"+s.HealthAddress+prefix+"api/health", nil)
	if e != nil {
		return e
	}
	transport := &http.Transport{Proxy: nil}
	if cfg.HTTPS {
		host, _, e := net.SplitHostPort(s.HealthAddress)
		if e != nil {
			return e
		}
		request, e = http.NewRequestWithContext(ctx, "GET", "https://"+cfg.Domain+prefix+"api/health", nil)
		if e != nil {
			return e
		}
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(host, "443"))
		}
	}
	client := &http.Client{Timeout: 3 * time.Second, Transport: transport}
	r, e := client.Do(request)
	if e != nil {
		return e
	}
	defer r.Body.Close()
	if r.StatusCode != 204 {
		return fmt.Errorf("panel health returned HTTP %d", r.StatusCode)
	}
	return nil
}
func (s *Service) inspect(ctx context.Context, path string) (panel.BackupSummary, error) {
	return InspectChild(ctx, s.Binary, path)
}
func InspectChild(ctx context.Context, binary, path string) (summary panel.BackupSummary, e error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, e := exec.CommandContext(ctx, binary, "inspect-backup", "--input", path).Output()
	if e != nil {
		return summary, errors.New("backup validation failed")
	}
	if len(out) > 8192 {
		return summary, errors.New("invalid backup summary")
	}
	e = json.Unmarshal(out, &summary)
	return
}
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fail := func(code int, e error) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": e.Error()})
	}
	if r.URL.Path == "/status" && r.Method == "GET" {
		_ = json.NewEncoder(w).Encode(s.Status())
		return
	}
	if r.URL.Path == "/firewall" && r.Method == "POST" {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		if path, e := exec.LookPath("ufw"); e == nil {
			out, e := exec.CommandContext(ctx, path, "status").Output()
			if e == nil && strings.Contains(string(out), "Status: active") {
				for _, port := range []string{"80/tcp", "443/tcp"} {
					if e = exec.CommandContext(ctx, path, "allow", port, "comment", "soar-panel-https").Run(); e != nil {
						fail(500, errors.New("cannot open HTTPS firewall ports"))
						return
					}
				}
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		return
	}
	if r.URL.Path != "/start" || r.Method != "POST" {
		fail(404, errors.New("unknown maintenance operation"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 48<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request panel.MaintenanceRequest
	if e := decoder.Decode(&request); e != nil {
		fail(400, errors.New("invalid maintenance request"))
		return
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		fail(400, errors.New("invalid request framing"))
		return
	}
	job, e := s.Begin(request)
	if e != nil {
		fail(409, e)
		return
	}
	_ = json.NewEncoder(w).Encode(job)
}
func (s *Service) Begin(request panel.MaintenanceRequest) (panel.MaintenanceJob, error) {
	if request.Action != "restore" && request.Action != "update" {
		return panel.MaintenanceJob{}, errors.New("unsupported maintenance operation")
	}
	if request.Action == "update" && (!panel.ValidVersion(request.Version) || len(request.Backup) != 0) {
		return panel.MaintenanceJob{}, errors.New("invalid panel update")
	}
	if request.Action == "restore" && (len(request.Backup) < 8192 || len(request.Backup) > panel.MaxBackupBytes || len(request.SHA256) != 64) {
		return panel.MaintenanceJob{}, errors.New("invalid panel backup")
	}
	s.mu.Lock()
	if s.job.State == "running" {
		s.mu.Unlock()
		return panel.MaintenanceJob{}, errors.New("another maintenance operation is running")
	}
	raw := make([]byte, 16)
	if _, e := rand.Read(raw); e != nil {
		s.mu.Unlock()
		return panel.MaintenanceJob{}, e
	}
	job := panel.MaintenanceJob{ID: hex.EncodeToString(raw), Kind: request.Action, State: "running", Message: "Validating maintenance request", Version: request.Version, Started: time.Now().UnixMilli()}
	s.job = job
	e := panel.WritePrivateJSON(s.statusPath(), job)
	s.mu.Unlock()
	if e != nil {
		return job, e
	}
	go func() { time.Sleep(time.Second); s.perform(request, job) }()
	return job, nil
}
func (s *Service) perform(request panel.MaintenanceRequest, job panel.MaintenanceJob) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	work := filepath.Join(s.StateDir, job.ID)
	defer func() {
		for _, name := range []string{"restore.db", "release.tar.gz", "soar-panel", "soar-panel-ops", "soar-panel-security"} {
			_ = os.Remove(filepath.Join(work, name))
		}
		s.pruneJobs()
	}()
	var backup string
	stopped := false
	fail := func(e error) {
		job.Message = e.Error()
		job.State = "failed"
		if stopped {
			recovery, done := context.WithTimeout(context.Background(), 45*time.Second)
			defer done()
			if re := s.rollback(recovery, backup); re == nil {
				job.State = "rolled_back"
				job.Message += "; previous installation restored"
			} else {
				job.Message += "; automatic rollback failed: " + re.Error()
			}
		}
		job.Finished = time.Now().UnixMilli()
		s.Save(job)
	}
	if e := os.Mkdir(work, 0700); e != nil {
		fail(e)
		return
	}
	var candidate string
	if request.Action == "restore" {
		candidate = filepath.Join(work, "restore.db")
		if e := os.WriteFile(candidate, request.Backup, 0600); e != nil {
			fail(e)
			return
		}
		request.Backup = nil
		summary, e := s.Inspect(ctx, candidate)
		if e != nil {
			fail(e)
			return
		}
		if summary.SHA256 != request.SHA256 || summary.BaseZone != request.ExpectedZone {
			fail(errors.New("backup identity or zone mismatch"))
			return
		}
	} else {
		out, e := exec.CommandContext(ctx, s.Binary, "version").Output()
		if e != nil || !panel.NewerVersion(request.Version, strings.TrimSpace(string(out))) {
			fail(errors.New("update must be newer than the installed panel"))
			return
		}
		if e = s.Fetch(ctx, request.Version, work); e != nil {
			fail(e)
			return
		}
		for _, name := range []string{"soar-panel", "soar-panel-ops"} {
			out, e = exec.CommandContext(ctx, filepath.Join(work, name), "version").Output()
			if e != nil || strings.TrimSpace(string(out)) != request.Version {
				fail(errors.New("downloaded binary version mismatch"))
				return
			}
		}
	}
	backup = filepath.Join(work, "before")
	if e := os.Mkdir(backup, 0700); e != nil {
		fail(e)
		return
	}
	job.Backup = backup
	s.Save(job)
	// Stop only after the entire candidate has been downloaded and validated.
	if e := s.Control(ctx, "stop"); e != nil {
		fail(errors.New("cannot stop panel for maintenance"))
		return
	}
	stopped = true
	for _, pair := range [][2]string{{s.DB, "panel.db"}, {s.Binary, "soar-panel"}, {s.OpsBinary, "soar-panel-ops"}} {
		if e := copyAtomic(pair[0], filepath.Join(backup, pair[1]), 0600, -1, -1); e != nil {
			fail(e)
			return
		}
	}
	if s.SecurityScript != "" {
		if e := copyAtomic(s.SecurityScript, filepath.Join(backup, "soar-panel-security"), 0600, -1, -1); e != nil {
			fail(e)
			return
		}
	}
	job.Backup = backup
	job.Message = "Activating validated maintenance candidate"
	s.Save(job)
	if request.Action == "restore" {
		old, e := s.Inspect(ctx, s.DB)
		if e != nil {
			fail(e)
			return
		}
		if old.BaseZone != request.ExpectedZone {
			fail(errors.New("current server zone changed; restore cancelled"))
			return
		}
		if e = panel.PreserveRestoreAdmin(candidate, s.DB); e != nil {
			fail(e)
			return
		}
		if e = copyAtomic(candidate, s.DB, 0600, s.UID, s.GID); e != nil {
			fail(e)
			return
		}
	} else {
		for _, pair := range [][2]string{{"soar-panel", s.Binary}, {"soar-panel-ops", s.OpsBinary}} {
			if e := copyAtomic(filepath.Join(work, pair[0]), pair[1], 0755, -1, -1); e != nil {
				fail(e)
				return
			}
		}
		if s.SecurityScript != "" {
			if e := copyAtomic(filepath.Join(work, "soar-panel-security"), s.SecurityScript, 0755, -1, -1); e != nil {
				fail(e)
				return
			}
		}
	}
	if e := s.Control(ctx, "start"); e != nil {
		fail(errors.New("new panel did not start"))
		return
	}
	if e := s.waitHealthy(ctx); e != nil {
		fail(errors.New("new panel failed readiness check"))
		return
	}
	job.State = "succeeded"
	job.Message = "Maintenance completed; administrator credentials and access settings retained"
	job.Finished = time.Now().UnixMilli()
	s.Save(job)
	if request.Action == "update" {
		_ = s.Control(context.Background(), "restart-ops")
	}
}
func (s *Service) waitHealthy(ctx context.Context) error {
	for i := 0; i < 15; i++ {
		if e := s.Health(ctx); e == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return errors.New("readiness timeout")
}
func (s *Service) rollback(ctx context.Context, backup string) error {
	_ = s.Control(ctx, "stop")
	if backup != "" {
		for _, pair := range [][2]string{{"panel.db", s.DB}, {"soar-panel", s.Binary}, {"soar-panel-ops", s.OpsBinary}} {
			mode := os.FileMode(0755)
			uid, gid := -1, -1
			if pair[0] == "panel.db" {
				mode = 0600
				uid, gid = s.UID, s.GID
			}
			if _, e := os.Stat(filepath.Join(backup, pair[0])); e == nil {
				if e = copyAtomic(filepath.Join(backup, pair[0]), pair[1], mode, uid, gid); e != nil {
					return e
				}
			}
		}
	}
	if backup != "" && s.SecurityScript != "" {
		path := filepath.Join(backup, "soar-panel-security")
		if _, e := os.Stat(path); e == nil {
			if e = copyAtomic(path, s.SecurityScript, 0755, -1, -1); e != nil {
				return e
			}
		}
	}
	if e := s.Control(ctx, "start"); e != nil {
		return e
	}
	return s.waitHealthy(ctx)
}
func copyAtomic(source, target string, mode os.FileMode, uid, gid int) error {
	src, e := os.Open(source)
	if e != nil {
		return e
	}
	defer src.Close()
	dst, e := os.CreateTemp(filepath.Dir(target), ".soar-install-*")
	if e != nil {
		return e
	}
	path := dst.Name()
	defer os.Remove(path)
	if e = dst.Chmod(mode); e == nil && uid >= 0 {
		e = dst.Chown(uid, gid)
	}
	if e == nil {
		_, e = io.Copy(dst, src)
	}
	if e == nil {
		e = dst.Sync()
	}
	ce := dst.Close()
	if e == nil {
		e = ce
	}
	if e == nil {
		e = os.Rename(path, target)
	}
	return e
}
func (s *Service) fetchBundle(ctx context.Context, version, dir string) error {
	release, e := panel.ReleaseForVersion(ctx, version)
	if e != nil {
		return e
	}
	archive := "soar-panel-" + version + "-linux-amd64.tar.gz"
	sumName := archive + ".sha256"
	var sums []byte
	var archivePath string
	var archiveDigest string
	for _, name := range []string{sumName, archive} {
		for _, asset := range release.Assets {
			if asset.Name != name {
				continue
			}
			req, e := http.NewRequestWithContext(ctx, "GET", asset.URL, nil)
			if e != nil {
				return e
			}
			response, e := panel.ReleaseClient().Do(req)
			if e != nil {
				return e
			}
			if response.StatusCode != 200 {
				response.Body.Close()
				return errors.New("cannot download official panel bundle")
			}
			if name == sumName {
				sums, e = io.ReadAll(io.LimitReader(response.Body, 2049))
				response.Body.Close()
				if e != nil || len(sums) > 2048 {
					return errors.New("invalid release checksum file")
				}
			} else {
				archivePath = filepath.Join(dir, "release.tar.gz")
				f, e := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
				if e != nil {
					response.Body.Close()
					return e
				}
				hash := sha256.New()
				n, e := io.Copy(io.MultiWriter(f, hash), io.LimitReader(response.Body, (96<<20)+1))
				response.Body.Close()
				ce := f.Close()
				if e != nil {
					return e
				}
				if ce != nil {
					return ce
				}
				if n != asset.Size || n > 96<<20 {
					return errors.New("release bundle size mismatch")
				}
				archiveDigest = hex.EncodeToString(hash.Sum(nil))
				if asset.Digest != "" && asset.Digest != "sha256:"+archiveDigest {
					return errors.New("GitHub release digest mismatch")
				}
			}
		}
	}
	fields := strings.Fields(string(sums))
	if len(fields) != 2 || fields[0] != archiveDigest || fields[1] != archive {
		return errors.New("release SHA-256 verification failed")
	}
	return ExtractBinaries(archivePath, dir, version)
}
func ExtractBinaries(archive, destination, version string) error {
	f, e := os.Open(archive)
	if e != nil {
		return e
	}
	defer f.Close()
	z, e := gzip.NewReader(f)
	if e != nil {
		return e
	}
	defer z.Close()
	r := tar.NewReader(io.LimitReader(z, 256<<20))
	found := map[string]bool{}
	for {
		header, e := r.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		for _, name := range []string{"soar-panel", "soar-panel-ops", "soar-panel-security"} {
			want := "soar-panel-" + version + "-linux-amd64/bin/linux-amd64/" + name
			if name == "soar-panel-security" {
				want = "soar-panel-" + version + "-linux-amd64/panel-security.sh"
			}
			if header.Name != want {
				continue
			}
			if found[name] || header.Typeflag != tar.TypeReg || header.Size < 1 || header.Size > 96<<20 {
				return errors.New("invalid release binary entry")
			}
			out, e := os.OpenFile(filepath.Join(destination, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0755)
			if e != nil {
				return e
			}
			_, e = io.CopyN(out, r, header.Size)
			ce := out.Close()
			if e != nil {
				return e
			}
			if ce != nil {
				return ce
			}
			found[name] = true
		}
	}
	if len(found) != 3 {
		return errors.New("release is missing maintenance binaries")
	}
	return nil
}

// Parse IDs once at startup; request bodies cannot select an operating-system user.
func ParseID(value string) (int, error) { return strconv.Atoi(value) }
func (s *Service) pruneJobs() {
	entries, e := os.ReadDir(s.StateDir)
	if e != nil {
		return
	}
	type saved struct {
		name     string
		modified time.Time
	}
	var jobs []saved
	for _, entry := range entries {
		if !entry.IsDir() || len(entry.Name()) != 32 {
			continue
		}
		if _, e := hex.DecodeString(entry.Name()); e != nil {
			continue
		}
		info, e := entry.Info()
		if e == nil {
			jobs = append(jobs, saved{entry.Name(), info.ModTime()})
		}
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].modified.Before(jobs[j].modified) })
	active := s.Status().ID
	for len(jobs) > 5 {
		old := jobs[0]
		jobs = jobs[1:]
		if old.name == active {
			continue
		}
		target := filepath.Join(s.StateDir, old.name)
		relative, e := filepath.Rel(s.StateDir, target)
		if e == nil && relative == old.name {
			_ = os.RemoveAll(target)
		}
	}
}
