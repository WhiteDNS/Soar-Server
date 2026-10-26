package panel

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"
)

type TelegramConfig struct {
	AutoBackup  bool   `json:"auto_backup"`
	BackupTime  string `json:"backup_time"`
	BackupKey   string `json:"backup_key,omitempty"` // Private encryption key; never included in HTTP views or DB backups.
	Token       string `json:"token"`
	ChatID      int64  `json:"chat_id"`
	Enabled     bool   `json:"enabled"`
	Language    string `json:"language"`
	Alerts      bool   `json:"alerts"`
	DailyReport bool   `json:"daily_report"`
	ReportTime  string `json:"report_time"`
	Timezone    string `json:"timezone"`
	Origin      string `json:"origin"`
	Username    string `json:"username"`
	PairToken   string `json:"pair_token,omitempty"`
	PairExpires int64  `json:"pair_expires,omitempty"`
}
type telegramState struct {
	ReportError             string            `json:"report_error,omitempty"`
	BackupError             string            `json:"backup_error,omitempty"`
	LastBackup              string            `json:"last_backup,omitempty"`
	BackupStarted           int64             `json:"backup_started,omitempty"`
	MasterDNSIssues         uint64            `json:"masterdns_issues,omitempty"`
	MasterDNSRecoveryStarts uint64            `json:"masterdns_recovery_starts,omitempty"`
	MasterDNSFailed         bool              `json:"masterdns_failed,omitempty"`
	Offset                  int64             `json:"offset"`
	Booted                  bool              `json:"booted"`
	Accounts                map[string]string `json:"accounts,omitempty"`
	RuntimeKnown            bool              `json:"runtime_known"`
	RuntimeFailed           bool              `json:"runtime_failed"`
	CottenIssues            uint64            `json:"cotten_issues,omitempty"`
	CottenRecoveryStarts    uint64            `json:"cotten_recovery_starts,omitempty"`
	CottenFailed            bool              `json:"cotten_failed,omitempty"`
	LastReport              string            `json:"last_report,omitempty"`
	ReportStarted           int64             `json:"report_started,omitempty"`
	LastRelease             string            `json:"last_release,omitempty"`
	LastReleaseCheck        int64             `json:"last_release_check,omitempty"`
	PendingAction           string            `json:"pending_action,omitempty"`
	PendingSince            int64             `json:"pending_since,omitempty"`
	PendingJob              string            `json:"pending_job,omitempty"`
}
type telegramDisk struct {
	Config TelegramConfig `json:"config"`
	State  telegramState  `json:"state"`
}
type TelegramView struct {
	ReportError string `json:"report_error"`
	BackupError string `json:"backup_error"`
	AutoBackup  bool   `json:"auto_backup"`
	BackupTime  string `json:"backup_time"`
	LastReport  string `json:"last_report"`
	LastBackup  string `json:"last_backup"`
	Configured  bool   `json:"configured"`
	Enabled     bool   `json:"enabled"`
	ChatID      int64  `json:"chat_id"`
	Username    string `json:"username"`
	Language    string `json:"language"`
	Alerts      bool   `json:"alerts"`
	DailyReport bool   `json:"daily_report"`
	ReportTime  string `json:"report_time"`
	Timezone    string `json:"timezone"`
	Status      string `json:"status"`
	LastContact int64  `json:"last_contact"`
	Error       string `json:"error"`
	Paired      bool   `json:"paired"`
	PairURL     string `json:"pair_url,omitempty"`
	PairExpires int64  `json:"pair_expires,omitempty"`
}
type Telegram struct {
	mu                       sync.Mutex // Configuration and public connection status; never held during I/O.
	opMu                     sync.Mutex // Serialize reconfiguration with dispatch and persistent state.
	cfg                      TelegramConfig
	state                    telegramState
	file                     string
	h                        *HTTP
	client                   *http.Client
	baseURL                  string
	now                      func() time.Time
	latest                   func(context.Context) (PanelRelease, error)
	releaseVersion           func(context.Context, string) (PanelRelease, error)
	wake                     chan struct{}
	pollCancel               context.CancelFunc
	generation               uint64
	status, lastError        string
	pollError                string // An outgoing success must not hide broken command polling.
	lastContact              int64
	nextSend                 time.Time
	retryUntil               time.Time
	sendInterval             time.Duration
	conversation             *telegramConversation
	confirmation             *telegramConfirmation
	recovered                bool
	lastReport, lastBackup   string // Public delivery state, guarded by mu.
	reportError, backupError string
	reportRetry, backupRetry time.Time // Scheduler retry deadlines, guarded by opMu.
}
type telegramUser struct {
	ID       int64  `json:"id"`
	Language string `json:"language_code"`
	Username string `json:"username"`
	IsBot    bool   `json:"is_bot"`
}
type telegramChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}
type telegramMessage struct {
	Document *telegramDocument `json:"document,omitempty"`
	ID       int64             `json:"message_id"`
	From     telegramUser      `json:"from"`
	Chat     telegramChat      `json:"chat"`
	Text     string            `json:"text"`
	Reply    *struct {
		ID int64 `json:"message_id"`
	} `json:"reply_to_message"`
}
type telegramCallback struct {
	ID      string           `json:"id"`
	From    telegramUser     `json:"from"`
	Data    string           `json:"data"`
	Message *telegramMessage `json:"message"`
}
type telegramUpdate struct {
	ID       int64             `json:"update_id"`
	Message  *telegramMessage  `json:"message"`
	Callback *telegramCallback `json:"callback_query"`
}
type telegramAPIError struct{ Code, Retry int }

var errTelegramUpdatePersistence = errors.New("cannot persist Telegram update; action not executed")

func (e *telegramAPIError) Error() string {
	switch e.Code {
	case 401:
		return "Telegram token rejected; replace the bot token"
	case 403:
		return "Telegram access denied; unblock the bot and send /start"
	case 409:
		return "Telegram polling conflict; use a dedicated bot on only one server"
	case 429:
		return "Telegram rate limit; retrying later"
	}
	return fmt.Sprintf("Telegram request failed (HTTP %d); check chat ID and send /start", e.Code)
}
func defaultTelegramConfig() TelegramConfig {
	return TelegramConfig{Language: "auto", Alerts: true, DailyReport: true, ReportTime: "09:00", BackupTime: "03:00", Timezone: "Asia/Tehran"}
}
func NewTelegram(h *HTTP, file string) (*Telegram, error) {
	t := &Telegram{h: h, file: file, cfg: defaultTelegramConfig(), baseURL: "https://api.telegram.org", client: &http.Client{Timeout: 40 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("Telegram redirect rejected") }}, now: time.Now, latest: LatestStablePanelRelease, releaseVersion: ReleaseForVersion, wake: make(chan struct{}, 1), status: "disconnected", sendInterval: 1100 * time.Millisecond}
	raw, e := os.ReadFile(file)
	if e == nil {
		var disk telegramDisk
		if e = json.Unmarshal(raw, &disk); e != nil {
			return nil, errors.New("cannot read Telegram configuration")
		}
		t.cfg, t.state = disk.Config, disk.State
		if t.cfg.BackupTime == "" {
			t.cfg.BackupTime = "03:00"
		}
		if e = validateTelegramConfig(t.cfg, false); e != nil {
			return nil, e
		}
	} else if !os.IsNotExist(e) {
		return nil, errors.New("cannot read Telegram configuration")
	}
	t.recovered = t.cfg.Enabled && t.cfg.ChatID > 0 && t.state.Booted
	if t.cfg.Enabled && t.cfg.ChatID > 0 && t.cfg.DailyReport && t.state.ReportStarted == 0 {
		t.state.ReportStarted = t.now().UnixMilli()
		t.state.Booted = false // Persist the reporting baseline before polling.
	}
	if t.cfg.Enabled && t.cfg.ChatID > 0 && t.cfg.AutoBackup && t.state.BackupStarted == 0 {
		t.state.BackupStarted = t.now().UnixMilli()
		t.state.Booted = false
	}
	// Runtime counters restart with the process; persisted account/report state does not.
	t.state.RuntimeKnown = false
	t.state.CottenFailed = false
	t.state.CottenIssues = 0
	t.state.CottenRecoveryStarts = 0
	t.state.MasterDNSFailed = false
	t.state.MasterDNSIssues = 0
	t.state.MasterDNSRecoveryStarts = 0
	t.lastReport, t.lastBackup = t.state.LastReport, t.state.LastBackup
	t.reportError, t.backupError = t.state.ReportError, t.state.BackupError
	if t.cfg.Enabled {
		t.status = "connecting"
	}
	h.telegram = t
	return t, nil
}

var telegramTokenPattern = regexp.MustCompile(`^[0-9]{5,20}:[A-Za-z0-9_-]{20,100}$`)

func validateTelegramConfig(c TelegramConfig, required bool) error {
	if c.BackupTime != "" {
		if _, e := time.Parse("15:04", c.BackupTime); e != nil || len(c.BackupTime) != 5 {
			return errors.New("backup time must use HH:MM")
		}
	}
	if c.BackupKey != "" {
		if _, e := telegramBackupCipher(c.BackupKey); e != nil {
			return e
		}
	}
	if required || c.Token != "" {
		if !telegramTokenPattern.MatchString(c.Token) || c.ChatID < 0 {
			return errors.New("provide a valid Telegram bot token")
		}
	}
	if c.Language != "auto" && c.Language != "fa" && c.Language != "en" {
		return errors.New("choose auto, fa or en for Telegram language")
	}
	if _, e := time.Parse("15:04", c.ReportTime); e != nil || len(c.ReportTime) != 5 {
		return errors.New("report time must use HH:MM")
	}
	if _, e := time.LoadLocation(c.Timezone); e != nil {
		return errors.New("choose a valid IANA timezone")
	}
	return nil
}
func (t *Telegram) config() TelegramConfig { t.mu.Lock(); defer t.mu.Unlock(); return t.cfg }
func (t *Telegram) View() TelegramView {
	t.mu.Lock()
	defer t.mu.Unlock()
	c := t.cfg
	status, pairURL := t.status, ""
	if c.Enabled && c.ChatID == 0 && status != "error" {
		status = "awaiting_pairing"
	}
	if c.Enabled && c.ChatID == 0 && c.PairToken != "" && t.now().UnixMilli() < c.PairExpires {
		pairURL = "https://t.me/" + c.Username + "?start=" + c.PairToken
	}
	return TelegramView{ReportError: t.reportError, BackupError: t.backupError, AutoBackup: c.AutoBackup, BackupTime: c.BackupTime, LastReport: t.lastReport, LastBackup: t.lastBackup, Configured: c.Token != "", Enabled: c.Enabled, ChatID: c.ChatID, Username: c.Username, Language: c.Language, Alerts: c.Alerts, DailyReport: c.DailyReport, ReportTime: c.ReportTime, Timezone: c.Timezone, Status: status, LastContact: t.lastContact, Error: t.lastError, Paired: c.ChatID > 0, PairURL: pairURL, PairExpires: c.PairExpires}
}
func (t *Telegram) save() error {
	if e := WritePrivateJSON(t.file, telegramDisk{Config: t.config(), State: t.state}); e != nil {
		return e
	}
	t.mu.Lock()
	t.lastReport, t.lastBackup = t.state.LastReport, t.state.LastBackup
	t.reportError, t.backupError = t.state.ReportError, t.state.BackupError
	t.mu.Unlock()
	return nil
}
func (t *Telegram) connection(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err != nil {
		t.status = "error"
		t.lastError = err.Error()
	} else {
		t.status = "connected"
		t.lastError = ""
		t.lastContact = t.now().UnixMilli()
		if t.pollError != "" {
			t.status, t.lastError = "error", t.pollError
		}
	}
}

func (t *Telegram) pollingConnection(generation uint64, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if generation != t.generation {
		return
	}
	if err != nil {
		t.pollError = err.Error()
		t.status, t.lastError = "error", t.pollError
	} else {
		t.pollError, t.lastError, t.status = "", "", "connected"
		t.lastContact = t.now().UnixMilli()
	}
}
func (t *Telegram) call(ctx context.Context, c TelegramConfig, method string, body any, out any) error {
	if e := t.waitAPI(ctx, method == "sendMessage" || method == "sendDocument"); e != nil {
		return e
	}
	raw, e := json.Marshal(body)
	if e != nil {
		return errors.New("cannot encode Telegram request")
	}
	req, e := http.NewRequestWithContext(ctx, "POST", t.baseURL+"/bot"+c.Token+"/"+method, bytes.NewReader(raw))
	if e != nil {
		return errors.New("cannot prepare Telegram request")
	}
	req.Header.Set("Content-Type", "application/json")
	return t.request(ctx, req, out)
}
func (t *Telegram) request(ctx context.Context, req *http.Request, out any) error {
	resp, e := t.client.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("Telegram unreachable; check outbound HTTPS access")
	}
	defer resp.Body.Close()
	var envelope struct {
		OK         bool            `json:"ok"`
		Result     json.RawMessage `json:"result"`
		Code       int             `json:"error_code"`
		Parameters struct {
			Retry int `json:"retry_after"`
		} `json:"parameters"`
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&envelope); e != nil {
		return errors.New("invalid Telegram response")
	}
	if !envelope.OK || resp.StatusCode != 200 {
		code := envelope.Code
		if code == 0 {
			code = resp.StatusCode
		}
		t.deferAPI(envelope.Parameters.Retry)
		return &telegramAPIError{code, envelope.Parameters.Retry}
	}
	if out != nil {
		if e = json.Unmarshal(envelope.Result, out); e != nil {
			return errors.New("invalid Telegram result")
		}
	}
	return nil
}
func (t *Telegram) notify(ctx context.Context, c TelegramConfig, text string, keyboard any) (int64, error) {
	var msg telegramMessage
	parts := telegramTextParts(text)
	for i, part := range parts {
		var markup any
		if i == len(parts)-1 {
			markup = keyboard
		}
		body := map[string]any{"chat_id": c.ChatID, "text": part, "link_preview_options": map[string]bool{"is_disabled": true}}
		// Telegram rejects explicit null reply_markup; omit it for plain messages.
		if markup != nil {
			body["reply_markup"] = markup
		}
		e := t.call(ctx, c, "sendMessage", body, &msg)
		t.connection(e)
		if e != nil {
			return msg.ID, e
		}
	}
	return msg.ID, nil
}

// Telegram counts UTF-16 code units. Keep room below its 4096-unit text limit.
func telegramTextParts(text string) []string {
	parts, start, units := []string{}, 0, 0
	for i, r := range text {
		n := 1
		if r > 0xffff {
			n = 2
		}
		if units+n > 3900 {
			parts = append(parts, text[start:i])
			start, units = i, 0
		}
		units += n
	}
	return append(parts, text[start:])
}
func (t *Telegram) deferAPI(seconds int) {
	if seconds <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.retryUntil = time.Now().Add(time.Duration(seconds) * time.Second)
}

func (t *Telegram) waitAPI(ctx context.Context, sending bool) error {
	t.mu.Lock()
	at := t.retryUntil
	if sending && t.nextSend.After(at) {
		at = t.nextSend
	}
	if sending {
		t.nextSend = maxTime(at, time.Now()).Add(t.sendInterval)
	}
	t.mu.Unlock()
	if delay := time.Until(at); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	return ctx.Err()
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
func (t *Telegram) configure(ctx context.Context, c TelegramConfig) error {
	t.opMu.Lock()
	defer t.opMu.Unlock()
	return t.configureLocked(ctx, c)
}
func (t *Telegram) configureLocked(ctx context.Context, c TelegramConfig) error {
	old := t.config()
	if c.BackupKey == "" {
		c.BackupKey = old.BackupKey
	}
	if c.BackupTime == "" {
		c.BackupTime = "03:00"
	}
	if c.AutoBackup && c.BackupKey == "" {
		c.BackupKey = token()
	}
	if c.Token == "" {
		c.Token = old.Token
	}
	if old.Token != "" && old.Token != c.Token {
		c.ChatID = 0
	}
	if e := validateTelegramConfig(c, true); e != nil {
		return e
	}
	if c.Enabled {
		var me telegramUser
		if e := t.call(ctx, c, "getMe", map[string]any{}, &me); e != nil {
			return e
		}
		if !me.IsBot {
			return errors.New("Telegram token does not identify a bot")
		}
		c.Username = me.Username
		var info struct {
			URL string `json:"url"`
		}
		if e := t.call(ctx, c, "getWebhookInfo", map[string]any{}, &info); e != nil {
			return e
		}
		if info.URL != "" {
			return errors.New("this bot has a webhook; use a dedicated bot without a webhook")
		}
		if c.ChatID > 0 {
			var chat telegramChat
			if e := t.call(ctx, c, "getChat", map[string]any{"chat_id": c.ChatID}, &chat); e != nil {
				return e
			}
			if chat.ID != c.ChatID || chat.Type != "private" {
				return errors.New("only a private administrator chat is allowed; reconnect Telegram")
			}
		}
	}
	c.PairToken, c.PairExpires = "", 0
	if c.Enabled && c.ChatID == 0 {
		c.PairToken = token()
		c.PairExpires = t.now().Add(5 * time.Minute).UnixMilli()
	}
	state := t.state
	if old.Token != c.Token || old.ChatID != c.ChatID {
		state = telegramState{}
		t.recovered = false
	}
	if c.Enabled && c.ChatID > 0 && c.DailyReport && state.ReportStarted == 0 {
		state.ReportStarted = t.now().UnixMilli()
	}
	if c.Enabled && c.ChatID > 0 && c.AutoBackup && state.BackupStarted == 0 {
		state.BackupStarted = t.now().UnixMilli()
	}
	if e := WritePrivateJSON(t.file, telegramDisk{c, state}); e != nil {
		return errors.New("cannot save Telegram configuration")
	}
	t.mu.Lock()
	if t.pollCancel != nil {
		t.pollCancel()
	}
	t.cfg = c
	t.generation++
	t.lastError = ""
	t.pollError = ""
	t.lastContact = 0
	t.lastReport, t.lastBackup = state.LastReport, state.LastBackup
	t.reportError, t.backupError = state.ReportError, state.BackupError
	t.status = "disabled"
	if c.Enabled {
		t.status = "connecting"
	}
	t.mu.Unlock()
	t.state = state
	t.conversation = nil
	t.confirmation = nil
	t.reportRetry, t.backupRetry = time.Time{}, time.Time{}
	_ = t.h.store.Audit("telegram.configured", "")
	select {
	case t.wake <- struct{}{}:
	default:
	}
	return nil
}
func (t *Telegram) disconnect() error {
	t.opMu.Lock()
	defer t.opMu.Unlock()
	// Persist an empty configuration atomically before revoking the live worker.
	c := defaultTelegramConfig()
	// Disconnect revokes bot access, but must never destroy the only key capable
	// of decrypting backups already sent to the administrator.
	c.BackupKey = t.config().BackupKey
	if e := WritePrivateJSON(t.file, telegramDisk{Config: c}); e != nil {
		return errors.New("cannot disconnect Telegram")
	}
	t.mu.Lock()
	if t.pollCancel != nil {
		t.pollCancel()
	}
	t.cfg = c
	t.generation++
	t.status = "disconnected"
	t.lastError = ""
	t.pollError = ""
	t.lastContact = 0
	t.lastReport, t.lastBackup = "", ""
	t.reportError, t.backupError = "", ""
	t.mu.Unlock()
	t.state = telegramState{}
	t.conversation = nil
	t.confirmation = nil
	t.recovered = false
	_ = t.h.store.Audit("telegram.disconnected", "")
	select {
	case t.wake <- struct{}{}:
	default:
	}
	return nil
}
func (h *HTTP) telegramAPI(w http.ResponseWriter, r *http.Request) {
	t := h.telegram
	if t == nil {
		errorReply(w, 503, errors.New("Telegram integration unavailable"))
		return
	}
	if r.URL.Path == "/api/telegram/pair" {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		if e := t.renewPairing(); e != nil {
			errorReply(w, 400, e)
			return
		}
		jsonReply(w, 200, t.View())
		return
	}
	if r.URL.Path == "/api/telegram/test" {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		t.opMu.Lock()
		defer t.opMu.Unlock()
		c := t.config()
		if !c.Enabled || c.Token == "" || c.ChatID == 0 {
			errorReply(w, 400, errors.New("save the bot token and connect your private Telegram chat first"))
			return
		}
		_, e := t.notify(r.Context(), c, t.tr("✅ Telegram connection verified. Use /start to open management.", "✅ اتصال تلگرام تأیید شد. برای مدیریت /start را بفرستید."), nil)
		if e != nil {
			errorReply(w, 502, e)
			return
		}
		jsonReply(w, 200, t.View())
		return
	}
	switch r.Method {
	case "GET":
		jsonReply(w, 200, t.View())
	case "PUT":
		var in struct {
			AutoBackup  *bool   `json:"auto_backup"`
			BackupTime  *string `json:"backup_time"`
			Token       *string `json:"token"`
			Enabled     *bool   `json:"enabled"`
			Language    *string `json:"language"`
			Alerts      *bool   `json:"alerts"`
			DailyReport *bool   `json:"daily_report"`
			ReportTime  *string `json:"report_time"`
			Timezone    *string `json:"timezone"`
		}
		if e := decodeJSON(w, r, &in); e != nil {
			errorReply(w, 400, e)
			return
		}
		t.opMu.Lock()
		defer t.opMu.Unlock()
		c := t.config()
		if in.AutoBackup != nil {
			c.AutoBackup = *in.AutoBackup
		}
		if in.BackupTime != nil {
			c.BackupTime = *in.BackupTime
		}
		if in.Token != nil {
			c.Token = *in.Token
		}
		if in.Enabled != nil {
			c.Enabled = *in.Enabled
		}
		if in.Language != nil {
			c.Language = *in.Language
		}
		if in.Alerts != nil {
			c.Alerts = *in.Alerts
		}
		if in.DailyReport != nil {
			c.DailyReport = *in.DailyReport
		}
		if in.ReportTime != nil {
			c.ReportTime = *in.ReportTime
		}
		if in.Timezone != nil {
			c.Timezone = *in.Timezone
		}
		scheme := "http"
		if h.isSecure(r) {
			scheme = "https"
		}
		c.Origin = (&url.URL{Scheme: scheme, Host: r.Host}).String()
		if e := t.configureLocked(r.Context(), c); e != nil {
			errorReply(w, 400, e)
			return
		}
		jsonReply(w, 200, t.View())
	case "DELETE":
		if e := t.disconnect(); e != nil {
			errorReply(w, 500, e)
			return
		}
		jsonReply(w, 200, t.View())
	default:
		w.WriteHeader(405)
	}
}
func (t *Telegram) Run(ctx context.Context) {
	scheduleCtx, stopSchedule := context.WithCancel(ctx)
	scheduleDone := make(chan struct{})
	go func() { defer close(scheduleDone); t.runSchedule(scheduleCtx) }()
	defer func() { stopSchedule(); <-scheduleDone }()
	backoff := time.Second
	var registered uint64 = ^uint64(0)
	for ctx.Err() == nil {
		t.mu.Lock()
		c, g := t.cfg, t.generation
		t.mu.Unlock()
		if !c.Enabled || c.Token == "" {
			select {
			case <-ctx.Done():
				return
			case <-t.wake:
			}
			continue
		}
		if registered != g && c.ChatID > 0 {
			commands := []map[string]string{{"command": "start", "description": "Open management / مدیریت"}, {"command": "stats", "description": "Current server stats / آمار سرور"}, {"command": "help", "description": "Help / راهنما"}, {"command": "menu", "description": "Main menu / منو"}, {"command": "settings", "description": "Server settings / تنظیمات سرور"}, {"command": "backup", "description": "Backups / پشتیبان"}, {"command": "restore", "description": "Restore backup / بازیابی"}, {"command": "diagnostics", "description": "Diagnostics / عیب‌یابی"}, {"command": "access", "description": "Panel address / آدرس پنل"}, {"command": "cancel", "description": "Cancel / لغو"}}
			if e := t.call(ctx, c, "setMyCommands", map[string]any{"commands": commands, "scope": map[string]any{"type": "chat", "chat_id": c.ChatID}}, nil); e != nil {
				t.connection(e)
			} else {
				registered = g
			}
		}
		t.opMu.Lock()
		if g == t.generation && !t.state.Booted {
			t.state.Booted = true
			if e := t.save(); e != nil {
				t.state.Booted = false
				t.connection(errors.New("cannot persist Telegram state"))
				t.opMu.Unlock()
				timer := time.NewTimer(5 * time.Second)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-t.wake:
					timer.Stop()
				case <-timer.C:
				}
				continue
			}
		}
		offset := t.state.Offset
		t.opMu.Unlock()
		poll, cancel := context.WithCancel(ctx)
		t.mu.Lock()
		if g != t.generation {
			t.mu.Unlock()
			cancel()
			continue
		}
		t.pollCancel = cancel
		t.mu.Unlock()
		var updates []telegramUpdate
		e := t.call(poll, c, "getUpdates", map[string]any{"offset": offset, "timeout": 25, "limit": 50, "allowed_updates": []string{"message", "callback_query"}}, &updates)
		cancel()
		t.mu.Lock()
		current := g == t.generation
		t.mu.Unlock()
		if !current {
			continue
		}
		if e != nil {
			if ctx.Err() != nil {
				return
			}
			t.pollingConnection(g, e)
			delay := backoff
			var apiErr *telegramAPIError
			if errors.As(e, &apiErr) && apiErr.Retry > 0 {
				delay = time.Duration(apiErr.Retry) * time.Second
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-t.wake:
				timer.Stop()
			case <-timer.C:
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		backoff = time.Second
		t.pollingConnection(g, nil)
		persistFailed := false
		for _, u := range updates {
			t.opMu.Lock()
			if g == t.generation {
				if e = t.handleUpdate(ctx, u); e != nil {
					t.connection(e)
					persistFailed = errors.Is(e, errTelegramUpdatePersistence)
				}
			}
			t.opMu.Unlock()
			if persistFailed {
				break
			}
		}
		if persistFailed {
			timer := time.NewTimer(5 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-t.wake:
				timer.Stop()
			case <-timer.C:
			}
			continue
		}
	}
}
func (t *Telegram) handleUpdate(ctx context.Context, u telegramUpdate) error {
	if u.ID < t.state.Offset {
		return nil
	}
	// Acknowledge durably before dispatch: interrupted actions are never replayed.
	offset := t.state.Offset
	t.state.Offset = u.ID + 1
	if e := t.save(); e != nil {
		t.state.Offset = offset
		return errTelegramUpdatePersistence
	}
	c := t.config()
	var from telegramUser
	var chat telegramChat
	if u.Message != nil {
		from = u.Message.From
		chat = u.Message.Chat
	} else if u.Callback != nil && u.Callback.Message != nil {
		from = u.Callback.From
		chat = u.Callback.Message.Chat
	} else {
		return nil
	}
	if c.Enabled && c.ChatID == 0 {
		if u.Message == nil || chat.Type != "private" || chat.ID <= 0 || from.ID != chat.ID || from.IsBot {
			return nil
		}
		parts := strings.Fields(u.Message.Text)
		if len(parts) != 2 || parts[0] != "/start" || c.PairToken == "" || t.now().UnixMilli() >= c.PairExpires || !hmac.Equal([]byte(parts[1]), []byte(c.PairToken)) {
			return nil
		}
		c.ChatID = chat.ID
		c.PairToken = ""
		c.PairExpires = 0
		if c.Language == "auto" {
			c.Language = "en"
			if strings.HasPrefix(from.Language, "fa") {
				c.Language = "fa"
			}
		}
		state := t.state
		if c.DailyReport {
			state.ReportStarted = t.now().UnixMilli()
		}
		if c.AutoBackup {
			state.BackupStarted = t.now().UnixMilli()
		}
		if e := WritePrivateJSON(t.file, telegramDisk{Config: c, State: state}); e != nil {
			return errors.New("cannot persist Telegram pairing; no access was granted")
		}
		t.mu.Lock()
		t.cfg = c
		t.generation++
		t.status = "connected"
		t.lastError = ""
		t.lastContact = t.now().UnixMilli()
		t.mu.Unlock()
		t.state = state
		t.conversation = nil
		t.confirmation = nil
		t.recovered = false
		_ = t.h.store.Audit("telegram.paired", "")
		_, e := t.notify(ctx, c, t.tr("✅ Telegram connected. Only this private chat can manage the server.", "✅ تلگرام متصل شد. فقط همین گفت‌وگوی خصوصی می‌تواند سرور را مدیریت کند."), t.homeKeyboard())
		return e
	}
	if !c.Enabled || chat.Type != "private" || chat.ID != c.ChatID || from.ID != c.ChatID || from.IsBot {
		return nil
	}
	if c.Language == "auto" {
		c.Language = "en"
		if strings.HasPrefix(from.Language, "fa") {
			c.Language = "fa"
		}
		t.mu.Lock()
		t.cfg.Language = c.Language
		t.mu.Unlock()
		if e := t.save(); e != nil {
			return errors.New("cannot save Telegram language")
		}
	}
	if t.conversation != nil && !t.now().Before(t.conversation.Expires) {
		t.conversation = nil
	}
	var e error
	if u.Callback != nil {
		e = t.call(ctx, c, "answerCallbackQuery", map[string]any{"callback_query_id": u.Callback.ID}, nil)
		if e == nil {
			e = t.callback(ctx, u.Callback.Data)
		}
	} else {
		e = t.message(ctx, *u.Message)
	}
	if e != nil {
		_, sendErr := t.notify(ctx, t.config(), t.tr("❌ Action failed: ", "❌ عملیات ناموفق: ")+t.errorText(e), t.homeKeyboard())
		if sendErr != nil {
			return sendErr
		}
	}
	return nil
}
func telegramPath(s *Store) string { return filepath.Join(filepath.Dir(s.db.Path()), "telegram.json") }

func (t *Telegram) renewPairing() error {
	t.opMu.Lock()
	defer t.opMu.Unlock()
	c := t.config()
	if !c.Enabled || c.Token == "" || c.ChatID > 0 {
		return errors.New("save and enable an unpaired bot first")
	}
	c.PairToken = token()
	c.PairExpires = t.now().Add(5 * time.Minute).UnixMilli()
	if e := WritePrivateJSON(t.file, telegramDisk{Config: c, State: t.state}); e != nil {
		return errors.New("cannot create Telegram connection link")
	}
	t.mu.Lock()
	t.cfg = c
	t.mu.Unlock()
	return nil
}
