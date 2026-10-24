package panel

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
	bolt "go.etcd.io/bbolt"
)

var subscriptions = []byte("subscriptions")
var subscriptionIndex = []byte("subscription_index")

// Subscription credentials are separate from tunnel keys. Renewing a key keeps
// the subscription URL useful; revoking the URL does not interrupt cached keys.
func subscriptionDigest(raw []byte) []byte { sum := sha256.Sum256(raw); return sum[:] }

func removeSubscription(tx *bolt.Tx, id string) error {
	b := tx.Bucket(subscriptions)
	if old := b.Get([]byte(id)); old != nil {
		if err := tx.Bucket(subscriptionIndex).Delete(subscriptionDigest(old)); err != nil {
			return err
		}
	}
	return b.Delete([]byte(id))
}

func (s *Store) SubscriptionToken(id string, rotate bool) (encoded string, err error) {
	return s.subscriptionToken(id, rotate, nil)
}
func (s *Store) subscriptionToken(id string, rotate bool, revision *uint64) (encoded string, err error) {
	err = s.db.Update(func(tx *bolt.Tx) error {
		c, e := getClient(tx, id)
		if e != nil {
			return e
		}
		if revision != nil && c.Revision != *revision {
			return ErrConflict
		}
		b := tx.Bucket(subscriptions)
		raw := b.Get([]byte(id))
		if raw == nil || rotate {
			if e := removeSubscription(tx, id); e != nil {
				return e
			}
			raw = make([]byte, 32)
			if _, e := rand.Read(raw); e != nil {
				return e
			}
			if e := b.Put([]byte(id), raw); e != nil {
				return e
			}
			if e := tx.Bucket(subscriptionIndex).Put(subscriptionDigest(raw), []byte(id)); e != nil {
				return e
			}
			if rotate {
				if e := addEvent(tx, "client.subscription_rotated", id); e != nil {
					return e
				}
			}
		}
		encoded = base64.RawURLEncoding.EncodeToString(raw)
		return nil
	})
	return
}

func (s *Store) Subscription(encoded string) (c Client, cfg Settings, err error) {
	raw, e := base64.RawURLEncoding.DecodeString(encoded)
	if e != nil || len(raw) != 32 || len(encoded) != 43 || base64.RawURLEncoding.EncodeToString(raw) != encoded {
		return c, cfg, ErrNotFound
	}
	err = s.db.View(func(tx *bolt.Tx) error {
		id := tx.Bucket(subscriptionIndex).Get(subscriptionDigest(raw))
		if id == nil || !hmac.Equal(tx.Bucket(subscriptions).Get(id), raw) {
			return ErrNotFound
		}
		var e error
		c, e = getClient(tx, string(id))
		if e != nil {
			return e
		}
		return json.Unmarshal(tx.Bucket(meta).Get([]byte("settings")), &cfg)
	})
	return
}

func (h *HTTP) subscriptionURL(r *http.Request, credential string) string {
	scheme, host := "http", r.Host
	if h.isSecure(r) {
		scheme = "https"
	}
	if h.access != nil {
		cfg := h.access.Config()
		if cfg.HTTPS {
			scheme, host = "https", cfg.Domain
			if cfg.hasDomain((&url.URL{Host: r.Host}).Hostname()) {
				host = (&url.URL{Host: r.Host}).Hostname()
			}
		}
	}
	return (&url.URL{Scheme: scheme, Host: host, Path: "/sub/" + credential}).String()
}

func (h *HTTP) subscriptionAdmin(w http.ResponseWriter, r *http.Request, id string) {
	rotate := r.Method == "POST"
	credential, err := h.store.SubscriptionToken(id, rotate)
	if err != nil {
		errorReply(w, 500, errors.New("cannot prepare subscription"))
		return
	}
	link := h.subscriptionURL(r, credential)
	if strings.HasSuffix(r.URL.Path, "/subscription-qr.png") {
		png, e := qrcode.Encode(link, qrcode.Medium, 512)
		if e != nil {
			errorReply(w, 500, errors.New("cannot render subscription QR"))
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
		return
	}
	urls := []string{link}
	if h.access != nil {
		for _, domain := range h.access.Config().domains() {
			alternative := (&url.URL{Scheme: "https", Host: domain, Path: "/sub/" + credential}).String()
			if alternative != link {
				urls = append(urls, alternative)
			}
		}
	}
	jsonReply(w, 200, map[string]any{"url": link, "urls": urls, "update_interval_hours": 12})
}

func (h *HTTP) serveSubscription(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(405)
		return
	}
	credential := strings.TrimPrefix(r.URL.Path, "/sub/")
	c, cfg, err := h.store.Subscription(credential)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	expires := int64(0)
	if c.ExpiresAt > 0 {
		expires = c.ExpiresAt / 1000
	}
	w.Header().Set("Subscription-Userinfo", fmt.Sprintf("upload=%d; download=%d; total=%d; expire=%d", c.UploadBytes, c.DownloadBytes, c.QuotaBytes, expires))
	w.Header().Set("Profile-Title", "base64:"+base64.StdEncoding.EncodeToString([]byte(c.Name)))
	w.Header().Set("Profile-Update-Interval", "12")
	w.Header().Set("Content-Disposition", `inline; filename="whitedns-subscription.txt"`)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if state := c.Status(time.Now()); state != "active" {
		w.Header().Set("X-WhiteDNS-Account-Status", state)
		if r.Method == "HEAD" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		jsonReply(w, http.StatusForbidden, map[string]string{"error": "subscription account is inactive", "status": state})
		return
	}
	body := ""
	for _, zone := range cfg.orderedZones() {
		links, err := h.store.profilesAt(c, cfg, zone)
		if err != nil {
			http.Error(w, "subscription unavailable", 500)
			return
		}
		for _, engine := range profileEngines {
			if link := links[engine]; link != "" {
				body += link + "\n"
			}
		}
	}
	switch r.URL.Query().Get("format") {
	case "", "base64":
		body = base64.StdEncoding.EncodeToString([]byte(body)) + "\n"
	case "plain":
	default:
		http.Error(w, "unsupported subscription format", 400)
		return
	}
	if r.Method != "HEAD" {
		_, _ = w.Write([]byte(body))
	}
}
