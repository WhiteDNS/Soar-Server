package panel

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
	"golang.org/x/crypto/bcrypt"
)

var ErrDenied = errors.New("account disabled, expired or out of traffic")
var ErrNotFound = errors.New("account not found")
var ErrConflict = errors.New("account changed; refresh and retry")

const MaxClients = 512

type Settings struct {
	BaseZone     string           `json:"base_zone"`
	Resolvers    []string         `json:"resolvers"`
	Country      string           `json:"country"`
	AllowPorts   []uint16         `json:"allow_ports"`
	SoarSettings *ProfileSettings `json:"soarSettings,omitempty"`
}

func (s Settings) Validate() error {
	if len(s.BaseZone) < 3 || len(s.BaseZone) > 24 || !strings.Contains(s.BaseZone, ".") {
		return errors.New("base zone must be an ASCII domain, at most 24 characters")
	}
	for _, label := range strings.Split(s.BaseZone, ".") {
		if label == "" || label[0] == '-' || label[len(label)-1] == '-' {
			return errors.New("invalid base zone")
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return errors.New("invalid base zone")
			}
		}
	}
	if s.Country != "IR" && s.Country != "" {
		return errors.New("country must be IR or GLOBAL (empty)")
	}
	if len(s.Resolvers) < 1 || len(s.Resolvers) > 8 || len(s.AllowPorts) == 0 || len(s.AllowPorts) > 32 {
		return errors.New("provide 1–32 allowed ports and 1–8 bootstrap resolvers")
	}
	for _, r := range s.Resolvers {
		if _, e := netip.ParseAddr(r); e != nil {
			if a, e := netip.ParseAddrPort(r); e != nil || a.Port() == 0 {
				return errors.New("invalid resolver")
			}
		}
	}
	for _, p := range s.AllowPorts {
		if p == 0 {
			return errors.New("invalid destination port")
		}
	}
	return s.profileSettings().Validate()
}

type Client struct {
	InstallerProbe bool   `json:"installer_probe,omitempty"`
	ID             string `json:"id"`
	Name           string `json:"name"`
	Note           string `json:"note"`
	Zone           string `json:"zone"`
	PublicKey      string `json:"public_key"`
	Enabled        bool   `json:"enabled"`
	QuotaBytes     int64  `json:"quota_bytes"`
	UploadBytes    int64  `json:"upload_bytes"`
	DownloadBytes  int64  `json:"download_bytes"`
	ExpiresAt      int64  `json:"expires_at"`
	RateBytes      int64  `json:"rate_bytes"`
	StreamLimit    int    `json:"stream_limit"`
	Revision       uint64 `json:"revision"`
	UsageEpoch     uint64 `json:"usage_epoch"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
	LastSeen       int64  `json:"last_seen"`
}

type ClientInput struct {
	Name        string `json:"name"`
	Note        string `json:"note"`
	Enabled     bool   `json:"enabled"`
	QuotaBytes  int64  `json:"quota_bytes"`
	ExpiresAt   int64  `json:"expires_at"`
	RateBytes   int64  `json:"rate_bytes"`
	StreamLimit int    `json:"stream_limit"`
	Revision    uint64 `json:"revision"`
}

func (c Client) Used() int64 { return c.UploadBytes + c.DownloadBytes }
func (c Client) Status(now time.Time) string {
	if !c.Enabled {
		return "disabled"
	}
	if c.ExpiresAt > 0 && now.UnixMilli() >= c.ExpiresAt {
		return "expired"
	}
	if c.QuotaBytes > 0 && c.Used() >= c.QuotaBytes {
		return "exhausted"
	}
	return "active"
}
func (c ClientInput) validate() error {
	if strings.TrimSpace(c.Name) == "" || len(c.Name) > 160 || len(c.Note) > 2048 {
		return errors.New("name required (max 160 bytes); note max 2048 bytes")
	}
	if c.QuotaBytes < 0 || c.QuotaBytes > 1<<60 || c.ExpiresAt < 0 || c.RateBytes < 0 || c.RateBytes > 1<<30 || c.StreamLimit < 1 || c.StreamLimit > 128 {
		return errors.New("invalid quota, expiry, rate or stream limit")
	}
	return nil
}

type Admin struct {
	Name string `json:"name"`
	Hash []byte `json:"hash"`
}
type Event struct {
	ID       uint64 `json:"id"`
	At       int64  `json:"at"`
	Action   string `json:"action"`
	ClientID string `json:"client_id,omitempty"`
}
type Store struct{ db *bolt.DB }

var meta = []byte("meta")
var clients = []byte("clients")
var seeds = []byte("seeds")
var events = []byte("events")

func OpenStore(path string) (*Store, error) {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return nil, e
	}
	// A single writer/process owns the file. Never silently share a live database.
	db, e := bolt.Open(path, 0600, &bolt.Options{Timeout: time.Second})
	if e != nil {
		return nil, e
	}
	s := &Store{db: db}
	e = db.Update(func(tx *bolt.Tx) error {
		for _, b := range [][]byte{meta, clients, seeds, events, subscriptions, subscriptionIndex} {
			if _, e := tx.CreateBucketIfNotExists(b); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		db.Close()
		return nil, e
	}
	return s, nil
}
func (s *Store) Close() error { return s.db.Close() }
func putJSON(b *bolt.Bucket, k []byte, v any) error {
	raw, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return b.Put(k, raw)
}
func (s *Store) Initialized() bool {
	var yes bool
	s.db.View(func(tx *bolt.Tx) error { yes = tx.Bucket(meta).Get([]byte("admin")) != nil; return nil })
	return yes
}
func (s *Store) Init(cfg Settings, name, password string) error {
	cfg.BaseZone = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(cfg.BaseZone), "."))
	if e := cfg.Validate(); e != nil {
		return e
	}
	if strings.TrimSpace(name) == "" || len(name) > 64 || len(password) < 12 || len(password) > 72 {
		return errors.New("admin name required; password must be 12–72 bytes")
	}
	hash, e := bcrypt.GenerateFromPassword([]byte(password), 12)
	if e != nil {
		return e
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(meta)
		if b.Get([]byte("admin")) != nil {
			return errors.New("database already initialized")
		}
		if e := putJSON(b, []byte("settings"), cfg); e != nil {
			return e
		}
		return putJSON(b, []byte("admin"), Admin{name, hash})
	})
}
func (s *Store) CheckAdmin(name, password string) bool {
	var a Admin
	s.db.View(func(tx *bolt.Tx) error { return json.Unmarshal(tx.Bucket(meta).Get([]byte("admin")), &a) })
	// Always perform the password hash comparison, even for an unknown name.
	ok := bcrypt.CompareHashAndPassword(a.Hash, []byte(password)) == nil
	return ok && name == a.Name
}
func (s *Store) ResetAdmin(name, password string) error {
	if name == "" || len(name) > 64 || len(password) < 12 || len(password) > 72 {
		return errors.New("admin name required; password must be 12–72 bytes")
	}
	hash, e := bcrypt.GenerateFromPassword([]byte(password), 12)
	if e != nil {
		return e
	}
	return s.db.Update(func(tx *bolt.Tx) error { return putJSON(tx.Bucket(meta), []byte("admin"), Admin{name, hash}) })
}
func (s *Store) Settings() (v Settings, e error) {
	e = s.db.View(func(tx *bolt.Tx) error { return json.Unmarshal(tx.Bucket(meta).Get([]byte("settings")), &v) })
	if e == nil && v.SoarSettings == nil {
		p := DefaultProfileSettings()
		v.SoarSettings = &p
	}
	return
}
func (s *Store) SaveSettings(v Settings) error {
	if e := v.Validate(); e != nil {
		return e
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(meta)
		var old Settings
		if e := json.Unmarshal(b.Get([]byte("settings")), &old); e != nil {
			return e
		}
		if old.BaseZone != v.BaseZone {
			return errors.New("base zone is fixed; changing it invalidates existing profiles")
		}
		// Older API callers and databases omit this field. Preserve saved tuning
		// instead of silently reverting it when they change resolver/port settings.
		if v.SoarSettings == nil {
			p := old.profileSettings()
			v.SoarSettings = &p
		}
		if e := v.Validate(); e != nil {
			return e
		}
		if e := putJSON(b, []byte("settings"), v); e != nil {
			return e
		}
		return addEvent(tx, "settings.updated", "")
	})
}
func randomIdentity(base string) (id, zone, public string, seed []byte, e error) {
	raw := make([]byte, 16)
	if _, e = rand.Read(raw); e != nil {
		return
	}
	id = hex.EncodeToString(raw)
	label := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw))
	label = label[:min(26, 44-len(base))]
	zone = label + "." + base
	seed = make([]byte, ed25519.SeedSize)
	if _, e = rand.Read(seed); e != nil {
		return
	}
	public = base64.StdEncoding.EncodeToString(ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey))
	return
}
func (s *Store) Create(in ClientInput) (c Client, e error) {
	return s.create(in, false)
}
func (s *Store) create(in ClientInput, installerProbe bool) (c Client, e error) {
	capacity := MaxClients
	if installerProbe {
		capacity++
	}
	if e = in.validate(); e != nil {
		return
	}
	cfg, e := s.Settings()
	if e != nil {
		return
	}
	id, zone, pub, seed, e := randomIdentity(cfg.BaseZone)
	if e != nil {
		return
	}
	now := time.Now().UnixMilli()
	c = Client{ID: id, Zone: zone, PublicKey: pub, CreatedAt: now, UpdatedAt: now, Revision: 1, UsageEpoch: 1, InstallerProbe: installerProbe}
	applyInput(&c, in)
	e = s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(clients)
		if b.Stats().KeyN >= capacity {
			return errors.New("account capacity reached")
		}
		if e := putJSON(b, []byte(id), c); e != nil {
			return e
		}
		if e := tx.Bucket(seeds).Put([]byte(id), seed); e != nil {
			return e
		}
		return addEvent(tx, "client.created", id)
	})
	return
}
func applyInput(c *Client, in ClientInput) {
	c.Name = strings.TrimSpace(in.Name)
	c.Note = in.Note
	c.Enabled = in.Enabled
	c.QuotaBytes = in.QuotaBytes
	c.ExpiresAt = in.ExpiresAt
	c.RateBytes = in.RateBytes
	c.StreamLimit = in.StreamLimit
}
func getClient(tx *bolt.Tx, id string) (c Client, e error) {
	raw := tx.Bucket(clients).Get([]byte(id))
	if raw == nil {
		return c, ErrNotFound
	}
	e = json.Unmarshal(raw, &c)
	return
}
func (s *Store) Get(id string) (c Client, e error) {
	e = s.db.View(func(tx *bolt.Tx) error { c, e = getClient(tx, id); return e })
	return
}
func (s *Store) List() (out []Client, e error) {
	out = []Client{}
	e = s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(clients).ForEach(func(k, v []byte) error {
			var c Client
			if e := json.Unmarshal(v, &c); e != nil {
				return e
			}
			out = append(out, c)
			return nil
		})
	})
	return
}
func (s *Store) Seed(id string) (out []byte, e error) {
	e = s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket(seeds).Get([]byte(id))
		if len(raw) != ed25519.SeedSize {
			return ErrNotFound
		}
		out = append([]byte{}, raw...)
		return nil
	})
	return
}
func (s *Store) Edit(id string, in ClientInput) (c Client, e error) {
	if e = in.validate(); e != nil {
		return
	}
	e = s.db.Update(func(tx *bolt.Tx) error {
		c, e = getClient(tx, id)
		if e != nil {
			return e
		}
		if in.Revision != c.Revision {
			return ErrConflict
		}
		applyInput(&c, in)
		c.Revision++
		c.UpdatedAt = time.Now().UnixMilli()
		if e = putJSON(tx.Bucket(clients), []byte(id), c); e != nil {
			return e
		}
		return addEvent(tx, "client.updated", id)
	})
	return
}
func (s *Store) ResetUsage(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		c, e := getClient(tx, id)
		if e != nil {
			return e
		}
		c.UploadBytes = 0
		c.DownloadBytes = 0
		c.UsageEpoch++
		c.Revision++
		c.UpdatedAt = time.Now().UnixMilli()
		if e := putJSON(tx.Bucket(clients), []byte(id), c); e != nil {
			return e
		}
		return addEvent(tx, "client.usage_reset", id)
	})
}
func (s *Store) Rotate(id string) (c Client, e error) {
	cfg, e := s.Settings()
	if e != nil {
		return
	}
	_, zone, pub, seed, e := randomIdentity(cfg.BaseZone)
	if e != nil {
		return
	}
	e = s.db.Update(func(tx *bolt.Tx) error {
		c, e = getClient(tx, id)
		if e != nil {
			return e
		}
		c.Zone = zone
		c.PublicKey = pub
		c.Revision++
		c.UpdatedAt = time.Now().UnixMilli()
		if e := tx.Bucket(seeds).Put([]byte(id), seed); e != nil {
			return e
		}
		if e := putJSON(tx.Bucket(clients), []byte(id), c); e != nil {
			return e
		}
		return addEvent(tx, "client.rotated", id)
	})
	return
}
func (s *Store) Delete(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if _, e := getClient(tx, id); e != nil {
			return e
		}
		if e := removeSubscription(tx, id); e != nil {
			return e
		}
		if e := tx.Bucket(clients).Delete([]byte(id)); e != nil {
			return e
		}
		if e := tx.Bucket(seeds).Delete([]byte(id)); e != nil {
			return e
		}
		return addEvent(tx, "client.deleted", id)
	})
}

// reserve commits quota BEFORE forwarding payload. Parallel streams cannot overspend.
// A crash may conservatively charge an unfinished reservation (at most one
// bounded chunk per direction per active stream); it can never create free traffic.
func (s *Store) reserve(id string, n int, up bool) (allowed int, revision uint64, e error) {
	return s.reserveEpoch(id, n, up, 0)
}
func (s *Store) reserveEpoch(id string, n int, up bool, expected uint64) (allowed int, revision uint64, e error) {
	if n <= 0 {
		return
	}
	e = s.db.Update(func(tx *bolt.Tx) error {
		c, e := getClient(tx, id)
		if e != nil {
			return e
		}
		if c.Status(time.Now()) != "active" || expected != 0 && c.UsageEpoch != expected {
			return ErrDenied
		}
		allowed = min(n, 32*1024)
		if c.QuotaBytes > 0 {
			allowed = min(allowed, int(c.QuotaBytes-c.Used()))
		}
		if allowed <= 0 {
			return ErrDenied
		}
		if c.Used() > 1<<60 {
			return errors.New("usage counter capacity reached")
		}
		if up {
			c.UploadBytes += int64(allowed)
		} else {
			c.DownloadBytes += int64(allowed)
		}
		revision = c.UsageEpoch
		c.LastSeen = time.Now().UnixMilli()
		return putJSON(tx.Bucket(clients), []byte(id), c)
	})
	return
}
func (s *Store) refund(id string, n int, up bool, revision uint64) error {
	if n <= 0 {
		return nil
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		c, e := getClient(tx, id)
		if e != nil {
			return e
		}
		if c.UsageEpoch != revision {
			return nil
		}
		if up {
			c.UploadBytes = max(0, c.UploadBytes-int64(n))
		} else {
			c.DownloadBytes = max(0, c.DownloadBytes-int64(n))
		}
		return putJSON(tx.Bucket(clients), []byte(id), c)
	})
}
func addEvent(tx *bolt.Tx, action, id string) error {
	b := tx.Bucket(events)
	seq, e := b.NextSequence()
	if e != nil {
		return e
	}
	key := []byte(fmt.Sprintf("%020d", seq))
	if e := putJSON(b, key, Event{seq, time.Now().UnixMilli(), action, id}); e != nil {
		return e
	}
	if b.Stats().KeyN > 2000 {
		k, _ := b.Cursor().First()
		return b.Delete(k)
	}
	return nil
}
func (s *Store) Audit(action, id string) error {
	return s.db.Update(func(tx *bolt.Tx) error { return addEvent(tx, action, id) })
}
func (s *Store) Events() (out []Event, e error) {
	out = []Event{}
	e = s.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(events).Cursor()
		for k, v := c.Last(); k != nil && len(out) < 200; k, v = c.Prev() {
			var ev Event
			if e := json.Unmarshal(v, &ev); e != nil {
				return e
			}
			out = append(out, ev)
		}
		return nil
	})
	return
}
func (s *Store) Backup(w io.Writer) error {
	return s.db.View(func(tx *bolt.Tx) error { _, e := tx.WriteTo(w); return e })
}

func (s *Store) Profile(id string) (link string, e error) {
	c, e := s.Get(id)
	if e != nil {
		return
	}
	cfg, e := s.Settings()
	if e != nil {
		return
	}
	return profileFor(c, cfg)
}

func profileFor(c Client, cfg Settings) (string, error) {
	settings := struct {
		ProfileSettings
		Country string `json:"country"`
	}{cfg.profileSettings(), cfg.Country}
	body := map[string]any{"schema": "whitedns.profile", "version": 1, "profile": map[string]any{"id": c.ID, "name": c.Name, "server": map[string]string{"domain": c.Zone, "public_key": c.PublicKey}, "resolvers": cfg.Resolvers, "soarSettings": settings}}
	raw, e := json.Marshal(body)
	if e != nil {
		return "", e
	}
	return "soar://" + base64.RawURLEncoding.EncodeToString(raw), nil
}
