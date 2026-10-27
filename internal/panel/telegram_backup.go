package panel

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Cloud Bot API getFile accepts at most 20 MiB. Bound exports to the same size
// so every backup produced here can make the return trip through this bot.
const telegramFileLimit = 20 << 20
const telegramBackupMagic = "WhiteDNS-backup-v1\n"

type telegramDocument struct {
	ID   string `json:"file_id"`
	Name string `json:"file_name"`
	Size int64  `json:"file_size"`
}

type telegramBoundedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *telegramBoundedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, errors.New("backup exceeds supported size")
	}
	return b.Buffer.Write(p)
}

func telegramBackupCipher(key string) (cipher.AEAD, error) {
	raw, e := base64.RawURLEncoding.DecodeString(key)
	if e != nil || len(raw) != 32 {
		return nil, errors.New("backup recovery key must be a 43-character base64url key")
	}
	b, e := aes.NewCipher(raw)
	if e != nil {
		return nil, e
	}
	return cipher.NewGCM(b)
}

func sealTelegramBackup(raw []byte, key string) ([]byte, error) {
	if len(raw) > MaxBackupBytes {
		return nil, errors.New("backup exceeds 32 MiB")
	}
	aead, e := telegramBackupCipher(key)
	if e != nil {
		return nil, e
	}
	compressed := &telegramBoundedBuffer{limit: telegramFileLimit - aead.Overhead() - aead.NonceSize() - len(telegramBackupMagic)}
	z := gzip.NewWriter(compressed)
	if _, e = z.Write(raw); e != nil {
		_ = z.Close()
		return nil, e
	}
	if e = z.Close(); e != nil {
		return nil, e
	}
	nonce := make([]byte, aead.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return nil, e
	}
	out := append([]byte(telegramBackupMagic), nonce...)
	return aead.Seal(out, nonce, compressed.Bytes(), []byte(telegramBackupMagic)), nil
}

func openTelegramBackup(data []byte, key string) ([]byte, error) {
	if len(data) > telegramFileLimit {
		return nil, errors.New("Telegram backup exceeds 20 MiB")
	}
	// Also accept existing panel .db backups; the same full inspector validates them.
	if !bytes.HasPrefix(data, []byte(telegramBackupMagic)) {
		return data, nil
	}
	aead, e := telegramBackupCipher(key)
	if e != nil {
		return nil, errors.New("set the backup recovery key in Backups before restoring this file")
	}
	payload := data[len(telegramBackupMagic):]
	if len(payload) < aead.NonceSize()+aead.Overhead() {
		return nil, errors.New("invalid encrypted backup")
	}
	plain, e := aead.Open(nil, payload[:aead.NonceSize()], payload[aead.NonceSize():], []byte(telegramBackupMagic))
	if e != nil {
		return nil, errors.New("backup authentication failed; check the recovery key and file")
	}
	z, e := gzip.NewReader(bytes.NewReader(plain))
	if e != nil {
		return nil, errors.New("invalid compressed backup")
	}
	defer z.Close()
	raw, e := io.ReadAll(io.LimitReader(z, MaxBackupBytes+1))
	if e != nil || len(raw) > MaxBackupBytes {
		return nil, errors.New("invalid backup or expanded size exceeds 32 MiB")
	}
	return raw, nil
}

func (t *Telegram) ensureBackupKey() error {
	c := t.config()
	if c.BackupKey != "" {
		return nil
	}
	c.BackupKey = token()
	return t.savePreferences(c)
}

func (t *Telegram) sendBackup(ctx context.Context) error {
	if e := t.ensureBackupKey(); e != nil {
		return e
	}
	c := t.config()
	snapshot := &telegramBoundedBuffer{limit: MaxBackupBytes}
	if e := t.h.store.BackupContext(ctx, snapshot); e != nil {
		return errors.New("cannot create panel backup")
	}
	sealed, e := sealTelegramBackup(snapshot.Bytes(), c.BackupKey)
	if e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("chat_id", strconv.FormatInt(c.ChatID, 10))
	_ = w.WriteField("caption", fmt.Sprintf(t.tr("💾 Encrypted WhiteDNS backup · %s\nSHA256: %x\nSave your recovery key separately via Backups → Recovery key. Restore by replying with this file to /restore.", "💾 پشتیبان رمزگذاری‌شدهٔ WhiteDNS · %s\nSHA256: %x\nکلید بازیابی را از پشتیبان ← کلید بازیابی جداگانه نگه دارید. برای بازیابی، فایل را در پاسخ به /restore بفرستید."), t.now().Format(time.RFC3339), sha256.Sum256(sealed)))
	part, e := w.CreateFormFile("document", "whitedns-backup-"+t.now().UTC().Format("20060102-150405")+".wdb")
	if e != nil {
		return errors.New("cannot prepare backup upload")
	}
	if _, e = part.Write(sealed); e != nil {
		return e
	}
	if e = w.Close(); e != nil {
		return e
	}
	if e = t.waitAPI(ctx, true); e != nil {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", t.baseURL+"/bot"+c.Token+"/sendDocument", &body)
	if e != nil {
		return errors.New("cannot prepare backup upload")
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	e = t.request(ctx, req, nil)
	t.connection(e)
	if e == nil {
		_ = t.h.store.Audit("telegram.backup_exported", "")
	}
	return e
}

var telegramFilePath = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)

func (t *Telegram) downloadBackup(ctx context.Context, doc telegramDocument) ([]byte, error) {
	if doc.ID == "" || len(doc.ID) > 512 || doc.Size < 0 || doc.Size > telegramFileLimit {
		return nil, errors.New("backup must be at most 20 MiB")
	}
	var file struct {
		Path string `json:"file_path"`
		Size int64  `json:"file_size"`
	}
	c := t.config()
	if e := t.call(ctx, c, "getFile", map[string]string{"file_id": doc.ID}, &file); e != nil {
		return nil, e
	}
	if file.Size < 0 || file.Size > telegramFileLimit || len(file.Path) > 1024 || !telegramFilePath.MatchString(file.Path) {
		return nil, errors.New("invalid Telegram backup file")
	}
	for _, segment := range strings.Split(file.Path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return nil, errors.New("invalid Telegram file path")
		}
	}
	req, e := http.NewRequestWithContext(ctx, "GET", t.baseURL+"/file/bot"+c.Token+"/"+file.Path, nil)
	if e != nil {
		return nil, errors.New("cannot prepare backup download")
	}
	client := *t.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("Telegram redirect rejected") }
	r, e := client.Do(req)
	if e != nil {
		return nil, errors.New("cannot download Telegram backup")
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK || r.ContentLength > telegramFileLimit {
		return nil, errors.New("Telegram backup download rejected")
	}
	raw, e := io.ReadAll(io.LimitReader(r.Body, telegramFileLimit+1))
	if e != nil || len(raw) > telegramFileLimit {
		return nil, errors.New("backup download failed or exceeds 20 MiB")
	}
	return raw, nil
}

func (t *Telegram) restorePrompt(ctx context.Context) error {
	if t.h.ops == nil {
		return errors.New("maintenance service unavailable; rerun the current panel installer")
	}
	t.confirmation = nil
	t.conversation = &telegramConversation{Field: "restore", Expires: t.now().Add(10 * time.Minute)}
	id, e := t.notify(ctx, t.config(), t.tr("Reply to this message with a WhiteDNS .wdb or panel .db backup (up to 20 MiB). I will validate it and show a preview before requesting confirmation. Restoring replaces accounts, settings and usage; current administrator, panel access and Telegram pairing are preserved.", "فایل پشتیبان WhiteDNS با پسوند .wdb یا .db را در پاسخ به همین پیام بفرستید (تا ۲۰ MiB). ابتدا فایل بررسی و خلاصه نمایش داده می‌شود؛ بازیابی فقط پس از تأیید انجام می‌شود. حساب‌ها، تنظیمات و مصرف جایگزین می‌شوند؛ مدیر فعلی، دسترسی پنل و اتصال تلگرام حفظ می‌شوند."), map[string]any{"force_reply": true, "selective": true})
	if e == nil {
		t.conversation.Prompt = id
	}
	return e
}

func (t *Telegram) receiveBackup(ctx context.Context, msg telegramMessage) error {
	d := t.conversation
	if d == nil || d.Field != "restore" || !t.now().Before(d.Expires) || msg.Reply == nil || msg.Reply.ID != d.Prompt {
		return errors.New("use /restore and reply to its upload prompt with the backup")
	}
	job, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	data, e := t.downloadBackup(job, *msg.Document)
	if e != nil {
		return e
	}
	raw, e := openTelegramBackup(data, t.config().BackupKey)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(t.h.store.db.Path()), ".telegram-restore-*.db")
	if e != nil {
		return errors.New("cannot stage backup")
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(raw)
	}
	ce := f.Close()
	if e != nil || ce != nil {
		return errors.New("cannot stage backup")
	}
	var summary BackupSummary
	inspectCtx, stop := context.WithTimeout(job, 15*time.Second)
	defer stop()
	if t.h.inspectBackup != nil {
		summary, e = t.h.inspectBackup(inspectCtx, f.Name())
	} else {
		summary, e = InspectBackupFile(f.Name())
	}
	if e != nil {
		return errors.New("invalid or unsupported panel backup")
	}
	cfg, e := t.h.store.Settings()
	if e != nil {
		return e
	}
	if summary.BaseZone != cfg.BaseZone {
		return BackupMismatch(summary.BaseZone, cfg.BaseZone)
	}
	t.conversation = nil
	return t.confirm(ctx, telegramConfirmation{Action: "restore", Backup: raw, Summary: summary}, fmt.Sprintf(t.tr("Restore backup?\nZone: %s\nAccounts: %d\nBytes: %d\nSHA256: %s\nThis replaces accounts, tunnel keys, settings and usage with the backup snapshot. Current administrator, access settings and Telegram pairing remain. Connections will briefly drop; the maintenance service creates a rollback backup.", "پشتیبان بازیابی شود؟\nدامنه: %s\nحساب‌ها: %d\nبایت: %d\nSHA256: %s\nحساب‌ها، کلیدهای تونل، تنظیمات و مصرف با وضعیت فایل جایگزین می‌شوند. مدیر، تنظیمات دسترسی و اتصال تلگرام فعلی حفظ می‌شوند. اتصال‌ها موقتاً قطع می‌شوند؛ سرویس نگهداری پشتیبان بازگشت می‌سازد."), summary.BaseZone, summary.Accounts, summary.Bytes, summary.SHA256))
}
