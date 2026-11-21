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
	Administrators []telegramAdministrator `json:"administrators,omitempty"`
	FreeAccess     bool                    `json:"free_access,omitempty"`
	AutoBackup     bool                    `json:"auto_backup"`
	BackupTime     string                  `json:"backup_time"`
	BackupKey      string                  `json:"backup_key,omitempty"` // Private encryption key; never included in HTTP views or DB backups.
	Token          string                  `json:"token"`
	ChatID         int64                   `json:"chat_id"`
	Enabled        bool                    `json:"enabled"`
	Language       string                  `json:"language"`
	Alerts         bool                    `json:"alerts"`
	DailyReport    bool                    `json:"daily_report"`
	ReportTime     string                  `json:"report_time"`
	Timezone       string                  `json:"timezone"`
	Origin         string                  `json:"origin"`
	Username       string                  `json:"username"`
	PairToken      string                  `json:"pair_token,omitempty"`
	PairExpires    int64                   `json:"pair_expires,omitempty"`
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
	PendingChat             int64             `json:"pending_chat,omitempty"`
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
	telegramDialogue
	adminDialogues           map[int64]telegramDialogue // Guarded by opMu.
	activeAdminLanguage      string                     // Guarded by mu.
	mu                       sync.Mutex                 // Configuration and public connection status; never held during I/O.
	opMu                     sync.Mutex                 // Serialize reconfiguration with dispatch and persistent state.
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
	nextBotSend              time.Time
	chatSends                map[int64]time.Time
	retryUntil               time.Time
	sendInterval             time.Duration
	freeMu                   sync.Mutex
	freeChallenges           map[int64]telegramFreeChallenge // Guarded by freeMu.
	freeAttempts             map[int64][]time.Time
	freeReplyPending         map[int64]int64 // Guarded by freeMu.
	freeRate                 [2]freePublicRate
	freeQueues               [4]chan telegramFreeJob // Guarded by opMu.
	freeReplies              chan telegramFreeJob    // Guarded by opMu.
	freeBusy                 chan telegramFreeJob
	freeMenus                map[int64]telegramFreeMenuSlot // Guarded by freeMu.
	freeReserved             [4]map[int64]struct{}          // Guarded by freeMu.
	freeRevision             uint64                         // Writes hold opMu and mu.
	freeTraffic              telegramFreeTraffic
	freeSend                 [7]telegramFreeSend          // Guarded by mu.
	freeJoinCache            map[int64]freeJoinCacheEntry // Guarded by freeMu.
	freeJoinBotToken         string                       // Guarded by freeMu.
	freeJoinLostAccess       map[int64]bool               // Guarded by freeMu.
	freeJoinNextLookup       time.Time                    // Guarded by freeMu.
	freeJoinHealth           freeJoinHealth               // Guarded by freeMu.
	freeEpoch                string
	freeNotices              chan telegramFreeNotice
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
	ID       int64  `json:"id"`
	Type     string `json:"type"`
	Username string `json:"username"`
	Title    string `json:"title"`
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
	ID            int64             `json:"update_id"`
	Message       *telegramMessage  `json:"message"`
	Callback      *telegramCallback `json:"callback_query"`
	BotMembership *struct {
		Chat   telegramChat       `json:"chat"`
		Member telegramMembership `json:"new_chat_member"`
	} `json:"my_chat_member"`
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
	t.freeEpoch = token()[:8]
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
	if !validTelegramAdministrators(c) {
		return errors.New("invalid Telegram administrator list")
	}
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
	raw, e := json.Marshal(body)
	if e != nil {
		return errors.New("cannot encode Telegram request")
	}
	sending := oneOf(method, "sendMessage", "editMessageText", "sendDocument")
	lane, _ := ctx.Value(freeLaneKey{}).(freeLane)
	for {
		if e = t.waitAPI(ctx, sending, c.ChatID); e != nil {
			return e
		}
		attempt := ctx
		cancel := func() {}
		if lane == freeMenuLane {
			attempt, cancel = context.WithTimeout(ctx, 5*time.Second)
		}
		req, e := http.NewRequestWithContext(attempt, "POST", t.baseURL+"/bot"+c.Token+"/"+method, bytes.NewReader(raw))
		if e != nil {
			cancel()
			return errors.New("cannot prepare Telegram request")
		}
		req.Header.Set("Content-Type", "application/json")
		e = t.request(attempt, req, out)
		cancel()
		var api *telegramAPIError
		if lane != freeMenuLane || !sending || !errors.As(e, &api) || api.Code != http.StatusTooManyRequests {
			return e
		}
	}
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
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Code        int             `json:"error_code"`
		Description string          `json:"description"`
		Parameters  struct {
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
		if code == http.StatusTooManyRequests {
			t.deferAPI(req.Context(), max(1, envelope.Parameters.Retry))
			if lane, _ := req.Context().Value(freeLaneKey{}).(freeLane); lane != 0 {
				t.freeTraffic.limited.Add(1)
			}
		}
		if code == http.StatusBadRequest && strings.Contains(envelope.Description, "message is not modified") {
			return errTelegramMenuUnchanged
		}
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
	c.ChatID = telegramRecipient(ctx, c.ChatID)
	var lastID int64
	parts := telegramTextParts(text)
	for i, part := range parts {
		var markup any
		if i == len(parts)-1 {
			markup = keyboard
		}
		id, e := t.sendText(ctx, c, part, markup)
		if telegramActor(ctx).Role == telegramOwner {
			t.connection(e)
		}
		if e != nil {
			return lastID, e
		}
		lastID = id
	}
	return lastID, nil
}

func (t *Telegram) sendText(ctx context.Context, c TelegramConfig, text string, markup any) (int64, error) {
	body := map[string]any{"chat_id": c.ChatID, "text": text, "link_preview_options": map[string]bool{"is_disabled": true}}
	if keyboard, ok := markup.(telegramKeyboard); ok {
		body["reply_markup"] = styledTelegramKeyboard(keyboard)
		menu, _ := ctx.Value(telegramMenuKey{}).(*telegramMenuMessage)
		if menu != nil && menu.chat == c.ChatID && !menu.used {
			menu.used = true
			body["message_id"] = menu.message
			e := t.call(ctx, c, "editMessageText", body, nil)
			if e == nil || errors.Is(e, errTelegramMenuUnchanged) {
				return menu.message, nil
			}
			var api *telegramAPIError
			if !errors.As(e, &api) || api.Code != http.StatusBadRequest {
				return 0, e
			}
			delete(body, "message_id")
		}
		markup = body["reply_markup"]
	}
	// Telegram rejects explicit null reply_markup; omit it for plain messages.
	if markup != nil {
		body["reply_markup"] = markup
	}
	var msg telegramMessage
	e := t.call(ctx, c, "sendMessage", body, &msg)
	return msg.ID, e
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
func (t *Telegram) deferAPI(ctx context.Context, seconds int) {
	if seconds <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	at := time.Now().Add(time.Duration(seconds) * time.Second)
	if lane, _ := ctx.Value(freeLaneKey{}).(freeLane); lane != 0 {
		send := &t.freeSend[lane-1]
		send.retry = maxTime(send.retry, at)
		return
	}
	t.retryUntil = maxTime(t.retryUntil, at)
}

func (t *Telegram) waitAPI(ctx context.Context, sending bool, recipient ...int64) error {
	lane, _ := ctx.Value(freeLaneKey{}).(freeLane)
	revision, guarded := ctx.Value(freePublicConfigKey{}).(uint64)
	for ctx.Err() == nil {
		now := time.Now()
		t.mu.Lock()
		if guarded && (!t.cfg.Enabled || !t.cfg.FreeAccess || t.freeRevision != revision) {
			t.mu.Unlock()
			return context.Canceled
		}
		at, next := t.retryUntil, &t.nextSend
		if lane != 0 {
			send := &t.freeSend[lane-1]
			at, next = send.retry, &send.next
		}
		var chat int64
		if sending && t.sendInterval > 0 {
			at = maxTime(at, maxTime(*next, t.nextBotSend))
			if len(recipient) != 0 {
				chat = recipient[0]
				at = maxTime(at, t.chatSends[chat])
			}
		}
		if !at.After(now) {
			if sending && t.sendInterval > 0 {
				interval := t.sendInterval
				if lane == freeMenuLane {
					interval = 50 * time.Millisecond
				}
				*next = now.Add(interval)
				t.nextBotSend = now.Add(40 * time.Millisecond)
				if chat != 0 {
					if t.chatSends == nil {
						t.chatSends = make(map[int64]time.Time)
					}
					for id, until := range t.chatSends {
						if !until.After(now) {
							delete(t.chatSends, id)
						}
					}
					t.chatSends[chat] = now.Add(time.Second)
				}
			}
			t.mu.Unlock()
			return ctx.Err()
		}
		t.mu.Unlock()
		if deadline, ok := ctx.Deadline(); ok && !at.Before(deadline) {
			return context.DeadlineExceeded
		}
		delay := time.Until(at)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
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
		c.FreeAccess = false
	}
	c.Administrators = nil
	if old.Token == c.Token && old.ChatID == c.ChatID {
		c.Administrators = old.Administrators
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
	if old.Enabled != c.Enabled || old.FreeAccess != c.FreeAccess || old.Token != c.Token || old.ChatID != c.ChatID {
		t.freeRevision++
	}
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
	if !c.Enabled || old.Token != c.Token || old.ChatID != c.ChatID {
		t.clearFreeChallenge(0)
		t.freeEpoch = token()[:8]
	}
	t.telegramDialogue = telegramDialogue{}
	t.adminDialogues = nil
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
	t.freeRevision++
	t.status = "disconnected"
	t.lastError = ""
	t.pollError = ""
	t.lastContact = 0
	t.lastReport, t.lastBackup = "", ""
	t.reportError, t.backupError = "", ""
	t.mu.Unlock()
	t.state = telegramState{}
	t.clearFreeChallenge(0)
	t.freeEpoch = token()[:8]
	t.telegramDialogue = telegramDialogue{}
	t.adminDialogues = nil
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
	defer t.runFreeWorkers(ctx)()
	scheduleCtx, stopSchedule := context.WithCancel(ctx)
	scheduleDone := make(chan struct{})
	go func() { defer close(scheduleDone); t.runSchedule(scheduleCtx) }()
	defer func() { stopSchedule(); <-scheduleDone }()
	backoff := time.Second
	var registered uint64 = ^uint64(0)
	var commandsPublic bool
	var commandsLanguage string
	var nextRegistration time.Time
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
		if c.ChatID > 0 && (registered != g || commandsPublic != c.FreeAccess || commandsLanguage != c.Language) && !time.Now().Before(nextRegistration) {
			job, cancel := context.WithTimeout(ctx, 5*time.Second)
			e := t.syncTelegramCommands(job, c)
			cancel()
			if e == nil {
				registered, commandsPublic, commandsLanguage = g, c.FreeAccess, c.Language
			} else {
				nextRegistration = time.Now().Add(30 * time.Second)
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
		e := t.call(poll, c, "getUpdates", map[string]any{"offset": offset, "timeout": 25, "limit": 50, "allowed_updates": []string{"message", "callback_query", "my_chat_member"}}, &updates)
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
	c := t.config()
	if u.BotMembership != nil {
		t.state.Offset = u.ID + 1
		change := u.BotMembership
		if change.Chat.ID < 0 && change.Member.User.IsBot {
			catalog, err := t.h.store.freeCatalog()
			if err != nil {
				return err
			}
			for _, chat := range catalog.JoinChats {
				if chat.ID == change.Chat.ID {
					t.noteFreeBotAccess(c.Token, chat.ID, oneOf(change.Member.Status, "administrator", "creator"))
					break
				}
			}
		}
		return nil
	}
	var from telegramUser
	var chat telegramChat
	if u.Message != nil && u.Callback == nil {
		from = u.Message.From
		chat = u.Message.Chat
	} else if u.Message == nil && u.Callback != nil && u.Callback.Message != nil {
		from = u.Callback.From
		chat = u.Callback.Message.Chat
	}
	private := chat.Type == "private" && chat.ID > 0 && from.ID == chat.ID && !from.IsBot
	role := c.administratorRole(chat.ID)
	pairing := false
	if private && c.ChatID == 0 && u.Message != nil && c.PairToken != "" && t.now().UnixMilli() < c.PairExpires {
		parts := strings.Fields(u.Message.Text)
		pairing = len(parts) == 2 && parts[0] == "/start" && hmac.Equal([]byte(parts[1]), []byte(c.PairToken))
	}
	if !c.Enabled || !private || (!pairing && role == "") {
		// Public requests have their own one-use challenge and transactional state.
		// Floods of ignored updates must not force a disk write for each update.
		t.state.Offset = u.ID + 1
		if c.Enabled && c.FreeAccess && c.ChatID > 0 && private {
			t.dispatchFree(ctx, c, from, u)
		}
		return nil
	}
	// Acknowledge admin commands durably before dispatch.
	offset := t.state.Offset
	t.state.Offset = u.ID + 1
	if e := t.save(); e != nil {
		t.state.Offset = offset
		return errTelegramUpdatePersistence
	}
	if pairing {
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
		_, e := t.notify(ctx, c, t.tr("✅ Telegram connected. This private chat is the owner. To delegate access, open Bot administrators from the free configuration dashboard.", "✅ تلگرام متصل شد. این گفت‌وگوی خصوصی متعلق به مالک ربات است. برای واگذاری دسترسی، از داشبورد کانفیگ رایگان وارد «مدیران ربات» شوید."), t.homeKeyboard())
		return e
	}
	if role == telegramOwner && c.Language == "auto" {
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
	ctx = context.WithValue(ctx, telegramActorKey{}, telegramAdministrator{ID: chat.ID, Role: role})
	if role != telegramOwner {
		defer t.enterTelegramDialogue(c, from)()
	}
	if t.conversation != nil && !t.now().Before(t.conversation.Expires) {
		t.conversation = nil
	}
	var e error
	if u.Callback != nil {
		ctx = withTelegramMenu(ctx, chat.ID, u.Callback.Message.ID)
		e = t.call(ctx, c, "answerCallbackQuery", map[string]any{"callback_query_id": u.Callback.ID}, nil)
		if e == nil {
			e = t.callback(ctx, u.Callback.Data)
		}
	} else {
		e = t.message(ctx, *u.Message)
	}
	if e != nil {
		keyboard := t.homeKeyboard()
		if role == telegramFreeAdmin {
			keyboard = telegramKeyboard{Rows: [][]telegramButton{{t.button("Open the free configuration dashboard", "بازگشت به داشبورد کانفیگ رایگان", "free:manage")}}}
		}
		_, sendErr := t.notify(ctx, t.config(), t.tr("❌ Action failed: ", "❌ عملیات ناموفق: ")+t.errorText(e), keyboard)
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
