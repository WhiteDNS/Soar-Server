package panel

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const outboundSegmentBytes = 1 << 20
const outboundQueryMax = 100000

func parseOutboundPort(raw string) (uint16, error) {
	p, err := strconv.ParseUint(raw, 10, 16)
	if err != nil || p == 0 {
		return 0, errDestinationPort
	}
	return uint16(p), nil
}

type OutboundAttempt struct {
	IP     string `json:"ip"`
	Result string `json:"result"`
}
type OutboundRecord struct {
	ID            string            `json:"id"`
	Run           string            `json:"run"`
	Engine        string            `json:"engine"`
	AccountID     string            `json:"account_id"`
	Host          string            `json:"host"`
	Port          uint16            `json:"port"`
	Network       string            `json:"network"`
	Purpose       string            `json:"purpose"`
	Route         string            `json:"route"`
	Proxy         string            `json:"proxy,omitempty"`
	DestinationIP string            `json:"destination_ip,omitempty"`
	Attempts      []OutboundAttempt `json:"attempts,omitempty"`
	StartedAt     int64             `json:"started_at"`
	EndedAt       int64             `json:"ended_at,omitempty"`
	UpdatedAt     int64             `json:"updated_at"`
	BytesKnown    bool              `json:"bytes_known"`
	UploadBytes   uint64            `json:"upload_bytes"`
	DownloadBytes uint64            `json:"download_bytes"`
	State         string            `json:"state"`
	Reason        string            `json:"reason,omitempty"`
}

type outboundFlow struct {
	mu       sync.Mutex
	journal  *outboundJournal
	record   OutboundRecord
	up, down atomic.Uint64
	ended    bool
}

type outboundJournal struct {
	mu            sync.RWMutex
	diskMu        sync.RWMutex
	cfg           OutboundLogConfig
	dir, run      string
	live          map[string]*outboundFlow
	queue         chan outboundJournalEvent
	queries       chan struct{}
	dropped       atomic.Uint64
	storageErrors atomic.Uint64
	start         sync.Once
	stop          chan struct{}
	done          chan struct{}
	closed        bool
	started       bool
	rotate        atomic.Bool
}

type outboundJournalEvent struct {
	record  *OutboundRecord
	barrier chan struct{}
}

func newOutboundJournal(dir string) *outboundJournal {
	var id [12]byte
	rand.Read(id[:])
	return &outboundJournal{dir: dir, run: hex.EncodeToString(id[:]), cfg: defaultEgressConfig().Logs, live: map[string]*outboundFlow{}, queue: make(chan outboundJournalEvent, 2048), queries: make(chan struct{}, 2), stop: make(chan struct{}), done: make(chan struct{})}
}
func (j *outboundJournal) configure(cfg OutboundLogConfig) {
	if j == nil {
		return
	}
	j.mu.Lock()
	changed := j.cfg != cfg
	j.cfg = cfg
	closed := j.closed
	j.mu.Unlock()
	if changed {
		j.rotate.Store(true)
	}
	_, err := os.Stat(j.dir)
	if !closed && (cfg.Enabled || err == nil) {
		j.start.Do(func() { j.mu.Lock(); j.started = true; j.mu.Unlock(); go j.writer() })
	}
}
func (j *outboundJournal) config() OutboundLogConfig {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.cfg
}
func (j *outboundJournal) enqueue(record OutboundRecord) {
	j.mu.RLock()
	defer j.mu.RUnlock()
	if j.closed || !j.cfg.Enabled {
		return
	}
	select {
	case j.queue <- outboundJournalEvent{record: &record}:
	default:
		j.dropped.Add(1)
	}
}

func (j *outboundJournal) flush(ctx context.Context) error {
	j.mu.RLock()
	ready := j.started && !j.closed
	j.mu.RUnlock()
	if !ready {
		return nil
	}
	barrier := make(chan struct{})
	select {
	case j.queue <- outboundJournalEvent{barrier: barrier}:
	case <-j.stop:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-barrier:
		return nil
	case <-j.stop:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (j *outboundJournal) begin(account string, engine upstreamEngine, network, address string, route EngineEgress, resolver bool) *outboundFlow {
	if j == nil || !j.config().Enabled {
		return nil
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || len(host) > 253 {
		return nil
	}
	n, err := parseOutboundPort(port)
	if err != nil {
		return nil
	}
	var id [12]byte
	rand.Read(id[:])
	mode := route.Mode
	if mode == "" {
		mode = "direct"
	}
	purpose := "traffic"
	if resolver {
		purpose = "dns"
	}
	f := &outboundFlow{journal: j, record: OutboundRecord{ID: j.run + "-" + hex.EncodeToString(id[:]), Run: j.run, Engine: engineLogSource(engine), AccountID: account, Host: host, Port: n, Network: network, Purpose: purpose, Route: mode, StartedAt: time.Now().UnixMilli(), State: "pending", BytesKnown: true}}
	if route.proxy() {
		f.record.Proxy = route.Address
	}
	return f
}
func (f *outboundFlow) attempt(ip, result string) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record.DestinationIP = ip
	if len(f.record.Attempts) < 4 {
		f.record.Attempts = append(f.record.Attempts, OutboundAttempt{ip, result})
	}
}
func (f *outboundFlow) snapshot() OutboundRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	record := f.record
	record.Attempts = append([]OutboundAttempt(nil), record.Attempts...)
	record.UploadBytes = f.up.Load()
	record.DownloadBytes = f.down.Load()
	record.UpdatedAt = time.Now().UnixMilli()
	if f.ended {
		record.UpdatedAt = record.EndedAt
	}
	return record
}
func (f *outboundFlow) opened() {
	if f == nil {
		return
	}
	f.mu.Lock()
	f.record.State = "active"
	f.mu.Unlock()
	f.journal.mu.Lock()
	f.journal.live[f.record.ID] = f
	f.journal.mu.Unlock()
	f.journal.enqueue(f.snapshot())
}
func (f *outboundFlow) finish(err error) {
	if f == nil {
		return
	}
	f.mu.Lock()
	if f.ended {
		f.mu.Unlock()
		return
	}
	f.ended = true
	f.record.EndedAt = time.Now().UnixMilli()
	if reason := diagnosticReason(err); reason != "" {
		f.record.Reason = reason
	}
	if f.record.State == "active" {
		f.record.State = "completed"
	} else {
		f.record.State = "failed"
		if errors.Is(err, errDestinationPort) || errors.Is(err, ErrDenied) || errors.Is(err, errAntiFlood) || errors.Is(err, errProxyUDP) {
			f.record.State = "denied"
		}
	}
	id := f.record.ID
	f.mu.Unlock()
	f.journal.enqueue(f.snapshot())
	f.journal.mu.Lock()
	delete(f.journal.live, id)
	f.journal.mu.Unlock()
}

func (f *outboundFlow) ioError(err error) {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled) {
		return
	}
	f.mu.Lock()
	if !f.ended {
		f.record.Reason = diagnosticReason(err)
	}
	f.mu.Unlock()
}

// Payload accounting occurs around the actual stream/UDP payload I/O, before
// application quota decisions. Closing unblocks I/O and waits for byte counters.
type outboundObservedConn struct {
	net.Conn
	flow   *outboundFlow
	mu     sync.Mutex
	closed bool
	io     sync.WaitGroup
	once   sync.Once
}

func (c *outboundObservedConn) begin() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	c.io.Add(1)
	return true
}
func (c *outboundObservedConn) Read(p []byte) (int, error) {
	if !c.begin() {
		return 0, net.ErrClosed
	}
	defer c.io.Done()
	n, err := c.Conn.Read(p)
	c.flow.down.Add(uint64(n))
	c.flow.ioError(err)
	return n, err
}
func (c *outboundObservedConn) Write(p []byte) (int, error) {
	if !c.begin() {
		return 0, net.ErrClosed
	}
	defer c.io.Done()
	n, err := c.Conn.Write(p)
	c.flow.up.Add(uint64(n))
	c.flow.ioError(err)
	return n, err
}
func (c *outboundObservedConn) Close() error {
	var err error
	c.once.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()
		err = c.Conn.Close()
		c.io.Wait()
		c.flow.finish(err)
	})
	return err
}
func (c *outboundObservedConn) CloseWrite() error {
	if v, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return v.CloseWrite()
	}
	return nil
}

func (j *outboundJournal) segments() ([]os.DirEntry, error) {
	entries, err := os.ReadDir(j.dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := entries[:0]
	for _, entry := range entries {
		if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 && strings.HasPrefix(entry.Name(), "outbound-") && strings.HasSuffix(entry.Name(), ".jsonl") {
			out = append(out, entry)
		}
	}
	sort.Slice(out, func(i, k int) bool { return out[i].Name() < out[k].Name() })
	return out, nil
}
func (j *outboundJournal) prune(current string) error {
	entries, err := j.segments()
	if err != nil {
		return err
	}
	cfg := j.config()
	cutoff := time.Now().Add(-time.Duration(cfg.RetentionHours) * time.Hour)
	var total int64
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
	}
	// Reserve a segment before writing, so disk usage cannot exceed the cap.
	maxBytes := int64(cfg.MaxMiB) << 20
	for _, entry := range entries {
		if entry.Name() == current {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if total+outboundSegmentBytes <= maxBytes && !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(j.dir, entry.Name())); err != nil {
			return err
		}
		total -= info.Size()
	}
	return nil
}
func (j *outboundJournal) writer() {
	defer close(j.done)
	var file *os.File
	var size int64
	var name string
	var created time.Time
	defer func() {
		if file != nil {
			file.Sync()
			file.Close()
		}
	}()
	write := func(record OutboundRecord) {
		j.diskMu.Lock()
		defer j.diskMu.Unlock()
		if !j.config().Enabled {
			return
		}
		raw, err := json.Marshal(record)
		if err != nil || len(raw) > 8192 {
			j.storageErrors.Add(1)
			return
		}
		raw = append(raw, '\n')
		rotate := j.rotate.Swap(false)
		if file == nil || rotate || size+int64(len(raw)) > outboundSegmentBytes || time.Since(created) >= time.Hour {
			if file != nil {
				file.Sync()
				file.Close()
				file = nil
			}
			if err := os.MkdirAll(j.dir, 0700); err != nil {
				j.storageErrors.Add(1)
				return
			}
			if err := j.prune(""); err != nil {
				j.storageErrors.Add(1)
				return
			}
			name = "outbound-" + time.Now().UTC().Format("20060102T150405.000000000") + "-" + j.run + ".jsonl"
			file, err = os.OpenFile(filepath.Join(j.dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			size = 0
			created = time.Now()
			if err != nil {
				j.storageErrors.Add(1)
				return
			}
		}
		n, err := file.Write(raw)
		size += int64(n)
		if err != nil || n != len(raw) {
			j.storageErrors.Add(1)
			file.Close()
			file = nil
		}
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastPrune := time.Time{}
	lastCheckpoint := time.Now()
	for {
		select {
		case event := <-j.queue:
			if event.record != nil {
				write(*event.record)
			}
			if event.barrier != nil {
				if file != nil {
					if err := file.Sync(); err != nil {
						j.storageErrors.Add(1)
					}
				}
				close(event.barrier)
			}
		case <-ticker.C:
			if file != nil {
				if err := file.Sync(); err != nil {
					j.storageErrors.Add(1)
				}
			}
			if time.Since(lastCheckpoint) >= 10*time.Second && j.config().Enabled {
				lastCheckpoint = time.Now()
				j.mu.RLock()
				live := make([]*outboundFlow, 0, len(j.live))
				for _, flow := range j.live {
					live = append(live, flow)
				}
				j.mu.RUnlock()
				for _, flow := range live {
					record := flow.snapshot()
					if record.State == "active" {
						write(record)
					}
				}
			}
			rotate := j.rotate.Swap(false)
			if time.Since(lastPrune) > time.Minute || rotate {
				j.diskMu.Lock()
				lastPrune = time.Now()
				if file != nil {
					info, err := file.Stat()
					if err == nil && (rotate || time.Since(info.ModTime()) > time.Duration(j.config().RetentionHours)*time.Hour || time.Since(created) >= time.Hour) {
						file.Close()
						file = nil
						name = ""
					}
				}
				if err := j.prune(name); err != nil {
					j.storageErrors.Add(1)
				}
				j.diskMu.Unlock()
			}
		case <-j.stop:
			for {
				select {
				case event := <-j.queue:
					if event.record != nil {
						write(*event.record)
					}
					if event.barrier != nil {
						close(event.barrier)
					}
				default:
					return
				}
			}
		}
	}
}
func (j *outboundJournal) close() {
	if j == nil {
		return
	}
	j.mu.Lock()
	if j.closed {
		j.mu.Unlock()
		return
	}
	j.closed = true
	j.mu.Unlock()
	j.start.Do(func() { close(j.done) })
	close(j.stop)
	<-j.done
}

type outboundQuery struct {
	From, Until                   int64
	Engine, Account, State, Route string
}

func (q outboundQuery) matches(r OutboundRecord) bool {
	return r.StartedAt >= q.From && r.StartedAt <= q.Until && (q.Engine == "" || r.Engine == q.Engine) && (q.Account == "" || r.AccountID == q.Account) && (q.State == "" || r.State == q.State) && (q.Route == "" || r.Route == q.Route)
}
func (j *outboundJournal) query(ctx context.Context, q outboundQuery) ([]OutboundRecord, error) {
	select {
	case j.queries <- struct{}{}:
		defer func() { <-j.queries }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := j.flush(ctx); err != nil {
		return nil, err
	}
	entries, err := j.segments()
	if err != nil {
		return nil, err
	}
	cutoff := time.Now().Add(-time.Duration(j.config().RetentionHours) * time.Hour).UnixMilli()
	q.From = max(q.From, cutoff)
	latest := map[string]OutboundRecord{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		j.diskMu.RLock()
		f, err := os.Open(filepath.Join(j.dir, entry.Name()))
		var size int64
		if err == nil {
			var info os.FileInfo
			info, err = f.Stat()
			if err == nil {
				size = info.Size()
			}
			if err != nil {
				f.Close()
			}
		}
		j.diskMu.RUnlock()
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if size > outboundSegmentBytes {
			f.Close()
			return nil, errors.New("outbound journal segment exceeds storage bounds")
		}
		scanner := bufio.NewScanner(io.LimitReader(f, size))
		scanner.Buffer(make([]byte, 8192), 16384)
		for scanner.Scan() {
			if err := ctx.Err(); err != nil {
				f.Close()
				return nil, err
			}
			var r OutboundRecord
			if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
				j.storageErrors.Add(1)
				continue
			}
			if r.StartedAt < q.From || r.StartedAt > q.Until || q.Engine != "" && r.Engine != q.Engine || q.Account != "" && r.AccountID != q.Account {
				continue
			}
			latest[r.ID] = r
			if len(latest) > outboundQueryMax {
				f.Close()
				return nil, errors.New("too many outbound records; select a shorter time range")
			}
		}
		err = scanner.Err()
		f.Close()
		if err != nil {
			j.storageErrors.Add(1)
			continue
		}
	}
	j.mu.RLock()
	live := make([]*outboundFlow, 0, len(j.live))
	for _, f := range j.live {
		live = append(live, f)
	}
	j.mu.RUnlock()
	liveIDs := map[string]bool{}
	for _, f := range live {
		r := f.snapshot()
		liveIDs[r.ID] = true
		if r.StartedAt >= q.From && r.StartedAt <= q.Until {
			latest[r.ID] = r
		}
	}
	out := make([]OutboundRecord, 0, len(latest))
	for _, r := range latest {
		if r.State == "active" && !liveIDs[r.ID] {
			r.State = "interrupted"
			r.BytesKnown = false
			r.Reason = "service_interrupted_or_record_missing"
		}
		if q.matches(r) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, k int) bool {
		if out[i].StartedAt == out[k].StartedAt {
			return out[i].ID > out[k].ID
		}
		return out[i].StartedAt > out[k].StartedAt
	})
	return out, nil
}
