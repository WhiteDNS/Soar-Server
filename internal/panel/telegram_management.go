package panel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"time"
)

func (t *Telegram) savePreferences(c TelegramConfig) error {
	if e := validateTelegramConfig(c, true); e != nil {
		return e
	}
	old, state := t.config(), t.state
	if c.DailyReport && t.state.ReportStarted == 0 {
		t.state.ReportStarted = t.now().UnixMilli()
	}
	if c.AutoBackup && t.state.BackupStarted == 0 {
		t.state.BackupStarted = t.now().UnixMilli()
	}
	t.mu.Lock()
	t.cfg = c
	t.mu.Unlock()
	if e := t.save(); e != nil {
		t.state = state
		t.mu.Lock()
		t.cfg = old
		t.mu.Unlock()
		return errors.New("cannot save Telegram preferences")
	}
	t.reportRetry, t.backupRetry = time.Time{}, time.Time{}
	return nil
}

func telegramJSON(v any) string { raw, _ := json.MarshalIndent(v, "", "  "); return string(raw) }

func decodeTelegramJSON(text string, out any) error {
	d := json.NewDecoder(strings.NewReader(text))
	d.DisallowUnknownFields()
	if e := d.Decode(out); e != nil {
		return errors.New("invalid JSON or unsupported field")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("send one JSON value")
	}
	return nil
}

func (t *Telegram) serverSettings(ctx context.Context) error {
	c, e := t.h.store.Settings()
	if e != nil {
		return e
	}
	t.conversation, t.confirmation = nil, nil
	k := telegramKeyboard{Rows: [][]telegramButton{
		{t.button("Resolvers", "ریزالورها", "server:resolvers"), t.button("Allowed ports", "پورت‌های مجاز", "server:allow_ports")},
		{t.button("Country", "کشور", "server:country"), t.button("Server streams", "سقف جریان سرور", "server:server_stream_limit")},
		{t.button("Tunnel domains", "دامنه‌های تونل", "server:tunnel_zones"), t.button("Primary domain", "دامنهٔ اصلی تونل", "server:primary_zone")},
		{t.button("Public IPv4", "IPv4 عمومی", "server:public_ip"), t.button("Nameserver", "نام‌سرور", "server:nameserver_host")},
		{t.button("Domain nameservers", "نام‌سرور دامنه‌ها", "server:tunnel_nameservers")},
		{t.button("Soar tuning (JSON)", "تنظیم Soar (JSON)", "server:soarSettings"), t.button("Anti-flood (JSON)", "ضدفلود (JSON)", "server:antiflood")},
		{t.button("🔄 Restart engines", "🔄 راه‌اندازی مجدد موتورها", "engines"), t.button("🛡 Anti-flood status", "🛡 وضعیت ضدفلود", "flood")},
		{t.button("↩️ Menu", "↩️ منو", "home")},
	}}
	view := map[string]any{"base_zone": c.BaseZone, "resolvers": c.Resolvers, "allow_ports": c.AllowPorts, "country": c.Country, "server_stream_limit": c.ServerStreamLimit, "tunnel_zones": c.tunnelZones(), "primary_zone": c.primaryZone(), "public_ip": c.PublicIP, "nameserver_host": c.NameserverHost}
	_, e = t.notify(ctx, t.config(), t.tr("⚙️ Server settings\nChanges show a preview and require confirmation. Saving restarts active engines; identities, keys, subscription URLs and usage remain. The base zone is fixed.\n", "⚙️ تنظیمات سرور\nتغییرات پیش‌نمایش و تأیید دارند. ذخیره، موتورهای فعال را دوباره راه‌اندازی می‌کند؛ شناسه، کلید، لینک ساب و مصرف حفظ می‌شوند. دامنهٔ پایه ثابت است.\n")+telegramJSON(view), k)
	return e
}

func (t *Telegram) managementPrompt(ctx context.Context, setting, text string) error {
	before, e := t.h.store.Settings()
	if e != nil {
		return e
	}
	t.confirmation = nil
	t.conversation = &telegramConversation{Field: "management", Setting: setting, Before: before, Expires: t.now().Add(10 * time.Minute)}
	id, e := t.notify(ctx, t.config(), text, map[string]any{"force_reply": true, "selective": true})
	if e == nil {
		t.conversation.Prompt = id
	}
	return e
}

func (t *Telegram) managementCallback(ctx context.Context, data string) (bool, error) {
	switch {
	case data == "server":
		return true, t.serverSettings(ctx)
	case data == "diagnostics":
		return true, t.diagnostics(ctx)
	case data == "flood":
		return true, t.floodStatus(ctx)
	case data == "backups":
		return true, t.backups(ctx)
	case data == "restore":
		return true, t.restorePrompt(ctx)
	case data == "backup-now":
		return true, t.confirm(ctx, telegramConfirmation{Action: "backup"}, t.tr("Send an encrypted snapshot of accounts, keys, settings and usage to this private chat? Keep the recovery key outside the server.", "نسخهٔ رمزگذاری‌شدهٔ حساب‌ها، کلیدها، تنظیمات و مصرف به همین گفت‌وگوی خصوصی ارسال شود؟ کلید بازیابی را خارج از سرور نگه دارید."))
	case data == "backup-key":
		return true, t.confirm(ctx, telegramConfirmation{Action: "backup-key"}, t.tr("Show the backup recovery key in this private chat? Keep it separately; anyone holding this key and a backup can read its tunnel credentials.", "کلید بازیابی پشتیبان در همین گفت‌وگوی خصوصی نمایش داده شود؟ آن را جداگانه نگه دارید؛ دارندهٔ کلید و فایل پشتیبان می‌تواند کلیدهای تونل را بخواند."))
	case data == "engines":
		return true, t.confirm(ctx, telegramConfirmation{Action: "engines"}, t.tr("Restart all active tunnel engines? Connections briefly drop; keys and usage remain.", "همهٔ موتورهای فعال دوباره راه‌اندازی شوند؟ اتصال‌ها موقتاً قطع می‌شوند؛ کلیدها و مصرف حفظ می‌شوند."))
	case strings.HasPrefix(data, "release:"):
		id := strings.TrimPrefix(data, "release:")
		c, e := t.h.store.Get(id)
		if e != nil {
			return true, e
		}
		return true, t.confirm(ctx, telegramConfirmation{Action: "release", ID: id, Revision: c.Revision}, t.tr("Release anti-flood quarantine for ", "رفع مسدودی ضدفلود برای ")+c.Name)
	case strings.HasPrefix(data, "server:"):
		field := strings.TrimPrefix(data, "server:")
		prompts := map[string]string{
			"resolvers":           t.tr("Reply with 1–8 resolver IPs (optional :port), separated by spaces.", "۱ تا ۸ IP ریزالور با پورت اختیاری را با فاصله جدا کنید و در پاسخ بفرستید."),
			"allow_ports":         t.tr("Reply with 1–32 destination TCP ports separated by spaces.", "۱ تا ۳۲ پورت TCP مقصد را با فاصله جدا کنید و در پاسخ بفرستید."),
			"country":             t.tr("Reply with IR or GLOBAL.", "در پاسخ IR یا GLOBAL بفرستید."),
			"server_stream_limit": t.tr("Reply with the server TCP stream limit; 0 means unlimited.", "سقف جریان TCP سرور را بفرستید؛ ۰ یعنی نامحدود."),
			"tunnel_zones":        t.tr("Reply with the complete tunnel domain list separated by spaces, including the fixed base zone. Create DNS NS records first.", "فهرست کامل دامنه‌های تونل را با فاصله بفرستید؛ دامنهٔ پایه باید موجود باشد. ابتدا رکوردهای NS را بسازید."),
			"primary_zone":        t.tr("Reply with a domain already in the tunnel domain list.", "یکی از دامنه‌های موجود در فهرست دامنه‌های تونل را در پاسخ بفرستید."),
			"public_ip":           t.tr("Reply with the public IPv4 used by your DNS records.", "IPv4 عمومی استفاده‌شده در رکوردهای DNS را در پاسخ بفرستید."),
			"nameserver_host":     t.tr("Reply with the nameserver hostname, e.g. ns1.example.com.", "نام میزبان نام‌سرور مانند ns1.example.com را در پاسخ بفرستید."),
			"tunnel_nameservers":  t.tr("Reply with a JSON domain-to-nameserver map, e.g. {\"t.example.com\":\"ns1.example.com\"}.", "نگاشت دامنه به نام‌سرور را به‌صورت JSON بفرستید؛ مانند {\"t.example.com\":\"ns1.example.com\"}."),
		}
		text, ok := prompts[field]
		cfg, e := t.h.store.Settings()
		if e != nil {
			return true, e
		}
		if field == "soarSettings" {
			text, ok = t.tr("Reply with the updated Soar JSON object (existing values below):\n", "JSON تنظیمات Soar را با تغییر موردنظر در پاسخ بفرستید (مقادیر فعلی):\n")+telegramJSON(cfg.profileSettings()), true
		}
		if field == "antiflood" {
			text, ok = t.tr("Reply with the updated anti-flood JSON object (existing values below):\n", "JSON ضدفلود را با تغییر موردنظر در پاسخ بفرستید (مقادیر فعلی):\n")+telegramJSON(cfg.antiFloodPolicy()), true
		}
		if !ok {
			return true, errors.New("invalid server setting")
		}
		return true, t.managementPrompt(ctx, "server:"+field, text)
	case strings.HasPrefix(data, "schedule:"):
		field := strings.TrimPrefix(data, "schedule:")
		prompts := map[string]string{
			"report_time": t.tr("Reply with report time, HH:MM (24-hour).", "ساعت گزارش را به‌صورت HH:MM با قالب ۲۴ ساعته بفرستید."),
			"backup_time": t.tr("Reply with automatic backup time, HH:MM (24-hour).", "ساعت پشتیبان خودکار را به‌صورت HH:MM با قالب ۲۴ ساعته بفرستید."),
			"timezone":    t.tr("Reply with an IANA timezone, e.g. Asia/Tehran or UTC.", "منطقهٔ زمانی IANA مانند Asia/Tehran یا UTC را بفرستید."),
			"backup_key":  t.tr("Reply with the 43-character recovery key saved with your backup. Importing it replaces this server's current backup key; save the old key first.", "کلید بازیابی ۴۳ کاراکتریِ ذخیره‌شده همراه پشتیبان را بفرستید. این کار کلید فعلی سرور را جایگزین می‌کند؛ ابتدا کلید قبلی را نگه دارید."),
		}
		if field == "auto_backup" {
			c := t.config()
			c.AutoBackup = !c.AutoBackup
			if c.AutoBackup && c.BackupKey == "" {
				c.BackupKey = token()
			}
			return true, t.confirm(ctx, telegramConfirmation{Action: "preferences", Config: c}, fmt.Sprintf(t.tr("Automatic encrypted backup: %t at %s (%s). Files are sent to this private chat. Save the recovery key separately from Backups → Recovery key.", "پشتیبان رمزگذاری‌شدهٔ خودکار: %t ساعت %s (%s). فایل‌ها به همین گفت‌وگوی خصوصی ارسال می‌شوند. کلید را از پشتیبان ← کلید بازیابی جداگانه نگه دارید."), c.AutoBackup, c.BackupTime, c.Timezone))
		}
		if prompt, ok := prompts[field]; ok {
			return true, t.managementPrompt(ctx, "schedule:"+field, prompt)
		}
		return true, errors.New("invalid schedule setting")
	case data == "access":
		return true, t.accessMenu(ctx)
	case data == "access-edit":
		if t.h.access == nil {
			return true, errors.New("access settings unavailable in this installation")
		}
		c := t.h.access.Config()
		return true, t.managementPrompt(ctx, "access", t.tr("Reply with panel access JSON. Create domain A/AAAA records pointing to this server first. Certificate preparation keeps the previous address active. Keep old domains in aliases while migrating.\n", "JSON دسترسی پنل را در پاسخ بفرستید. ابتدا رکوردهای A/AAAA دامنه را به همین سرور وصل کنید. هنگام آماده‌سازی گواهی، آدرس قبلی فعال می‌ماند. برای انتقال، دامنه‌های قبلی را در aliases نگه دارید.\n")+telegramJSON(AccessInput{Domain: c.Domain, Aliases: c.Aliases, HTTPS: true, Email: c.Email, BasePath: c.BasePath}))
	case strings.HasPrefix(data, "access-apply:"):
		if t.h.access == nil {
			return true, errors.New("access settings unavailable")
		}
		id := strings.TrimPrefix(data, "access-apply:")
		p := t.h.access.Plan()
		if p == nil || p.ID != id || p.owner != t.accessOwner() || p.State != "ready" {
			return true, errors.New("access preview expired or is not ready")
		}
		return true, t.confirm(ctx, telegramConfirmation{Action: "access-apply", ID: id}, t.tr("Activate this panel address and sign out current dashboard sessions?\n", "این آدرس پنل فعال و نشست‌های داشبورد خارج شوند؟\n")+p.URL)
	}
	return false, nil
}

func (t *Telegram) managementReply(ctx context.Context, d *telegramConversation, text string) error {
	if !t.now().Before(d.Expires) {
		return errors.New("form expired; reopen the menu")
	}
	if len(text) > 8192 {
		return errors.New("input must be at most 8192 bytes")
	}
	if strings.HasPrefix(d.Setting, "schedule:") {
		c := t.config()
		field := strings.TrimPrefix(d.Setting, "schedule:")
		value := telegramDigits(text)
		switch field {
		case "report_time":
			c.ReportTime = value
		case "backup_time":
			c.BackupTime = value
		case "timezone":
			c.Timezone = text
		case "backup_key":
			c.BackupKey = text
		default:
			return errors.New("invalid preference")
		}
		if e := validateTelegramConfig(c, true); e != nil {
			return e
		}
		preview := field + ": " + value
		if field == "backup_key" {
			preview = t.tr("Replace the backup recovery key? Old backups still need their original key.", "کلید بازیابی جایگزین شود؟ پشتیبان‌های قبلی همچنان به کلید اصلی خود نیاز دارند.")
		}
		return t.confirm(ctx, telegramConfirmation{Action: "preferences", Config: c}, preview)
	}
	if d.Setting == "access" {
		var in AccessInput
		if e := decodeTelegramJSON(text, &in); e != nil {
			return e
		}
		if in.Password != "" {
			return errors.New("do not send a dashboard password; remove the password field")
		}
		if e := ValidateAccess(AccessConfig{Domain: in.Domain, Aliases: in.Aliases, HTTPS: in.HTTPS, Email: in.Email, BasePath: in.BasePath}); e != nil {
			return e
		}
		return t.confirm(ctx, telegramConfirmation{Action: "access-prepare", Access: in}, t.tr("Prepare DNS and HTTPS for these panel access settings? Activation requires a separate confirmation.\n", "DNS و HTTPS با این تنظیمات آماده شوند؟ فعال‌سازی تأیید جداگانه دارد.\n")+telegramJSON(in))
	}
	field := strings.TrimPrefix(d.Setting, "server:")
	// Deep copy avoids mutating pointer/map fields in the comparison snapshot.
	var after Settings
	_ = json.Unmarshal([]byte(telegramJSON(d.Before)), &after)
	words := strings.Fields(strings.NewReplacer(",", " ", "،", " ", "\n", " ").Replace(telegramDigits(text)))
	switch field {
	case "resolvers":
		after.Resolvers = words
	case "allow_ports":
		after.AllowPorts = nil
		for _, word := range words {
			n, e := strconv.ParseUint(word, 10, 16)
			if e != nil || n == 0 {
				return errors.New("invalid port")
			}
			after.AllowPorts = append(after.AllowPorts, uint16(n))
		}
	case "country":
		after.Country = strings.ToUpper(strings.TrimSpace(text))
		if after.Country == "GLOBAL" {
			after.Country = ""
		}
	case "server_stream_limit":
		n, e := strconv.ParseInt(telegramDigits(text), 10, 32)
		if e != nil {
			return errors.New("invalid stream limit")
		}
		after.ServerStreamLimit = int(n)
	case "tunnel_zones":
		after.TunnelZones = words
	case "primary_zone":
		after.PrimaryZone = strings.TrimSpace(text)
	case "public_ip":
		after.PublicIP = strings.TrimSpace(text)
	case "nameserver_host":
		after.NameserverHost = strings.ToLower(strings.TrimSpace(text))
	case "tunnel_nameservers":
		if e := decodeTelegramJSON(text, &after.TunnelNameservers); e != nil {
			return e
		}
	case "soarSettings":
		p := after.profileSettings()
		if e := decodeTelegramJSON(text, &p); e != nil {
			return e
		}
		after.SoarSettings = &p
	case "antiflood":
		p := after.antiFloodPolicy()
		if e := decodeTelegramJSON(text, &p); e != nil {
			return e
		}
		after.AntiFlood = &p
	default:
		return errors.New("invalid server setting")
	}
	if e := after.Validate(); e != nil {
		return e
	}
	oldFields, newFields := map[string]any{}, map[string]any{}
	_ = json.Unmarshal([]byte(telegramJSON(d.Before)), &oldFields)
	_ = json.Unmarshal([]byte(telegramJSON(after)), &newFields)
	preview := fmt.Sprintf("%s\n%s\n→\n%s", field, telegramJSON(oldFields[field]), telegramJSON(newFields[field]))
	return t.confirm(ctx, telegramConfirmation{Action: "settings", Before: d.Before, After: after}, preview+"\n"+t.tr("Active engines restart; keys, usage and subscription URLs remain.", "موتورهای فعال دوباره راه‌اندازی می‌شوند؛ کلیدها، مصرف و لینک ساب حفظ می‌شوند."))
}

func (t *Telegram) executeManagement(ctx context.Context, c *telegramConfirmation) (bool, error) {
	switch c.Action {
	case "backup":
		return true, t.sendBackup(ctx)
	case "backup-key":
		if e := t.ensureBackupKey(); e != nil {
			return true, e
		}
		_, e := t.notify(ctx, t.config(), t.tr("🔑 Backup recovery key — store it outside this server:\n", "🔑 کلید بازیابی پشتیبان — خارج از این سرور نگه دارید:\n")+t.config().BackupKey, nil)
		return true, e
	case "preferences":
		// A delayed confirmation must not reinstate a revoked pairing or stale settings.
		current := t.config()
		if !reflect.DeepEqual(current, c.ConfigBefore) {
			return true, ErrConflict
		}
		return true, t.savePreferences(c.Config)
	case "settings":
		if e := t.h.store.saveAssistantSettings(c.Before, c.After, "telegram.settings_updated"); e != nil {
			return true, e
		}
		if e := t.h.manager.RestartEngines(); e != nil {
			return true, errors.New("settings saved; some engines failed to restart, check diagnostics")
		}
		return true, nil
	case "engines":
		return true, t.h.manager.RestartEngines()
	case "release":
		account, e := t.h.store.Get(c.ID)
		if e != nil {
			return true, e
		}
		if account.Revision != c.Revision {
			return true, ErrConflict
		}
		if e = t.h.manager.flood.reset(c.ID); e == nil {
			e = t.h.manager.Reconcile()
		}
		return true, e
	case "access-prepare":
		if t.h.access == nil {
			return true, errors.New("access settings unavailable")
		}
		var firewall func(context.Context) error
		if t.h.ops != nil {
			firewall = t.h.ops.AllowHTTPS
		}
		p, e := t.h.access.Prepare(t.config().Origin, t.accessOwner(), c.Access, firewall)
		if e != nil {
			return true, e
		}
		_, e = t.notify(ctx, t.config(), t.tr("⏳ Access preparation started. Open Panel access to check readiness and activate.\n", "⏳ آماده‌سازی دسترسی آغاز شد. از منوی دسترسی پنل، وضعیت را بررسی و فعال کنید.\n")+p.URL, t.backKeyboard())
		return true, e
	case "access-apply":
		if t.h.access == nil {
			return true, errors.New("access settings unavailable")
		}
		next, e := t.h.access.Apply(c.ID, t.accessOwner())
		if e != nil {
			return true, e
		}
		t.h.mu.Lock()
		clear(t.h.sessions)
		t.h.mu.Unlock()
		_ = t.h.store.Audit("telegram.access_updated", "")
		_, e = t.notify(ctx, t.config(), t.tr("✅ Panel address:\n", "✅ آدرس پنل:\n")+next, nil)
		return true, e
	}
	return false, nil
}

func (t *Telegram) backups(ctx context.Context) error {
	c := t.config()
	t.conversation, t.confirmation = nil, nil
	text := fmt.Sprintf(t.tr("💾 Encrypted backups\nAutomatic: %t · %s (%s)\nLast automatic backup: %s\nLast daily report: %s\nBackups contain accounts, keys, settings and usage. Current Telegram pairing and panel access are machine-local and are preserved on restore. Save the recovery key separately before relying on backups.", "💾 پشتیبان رمزگذاری‌شده\nخودکار: %t · %s (%s)\nآخرین پشتیبان خودکار: %s\nآخرین گزارش روزانه: %s\nپشتیبان شامل حساب‌ها، کلیدها، تنظیمات و مصرف است. اتصال تلگرام و دسترسی پنل محلی‌اند و هنگام بازیابی حفظ می‌شوند. پیش از اتکا به پشتیبان، کلید بازیابی را جداگانه نگه دارید."), c.AutoBackup, c.BackupTime, c.Timezone, t.state.LastBackup, t.state.LastReport)
	if t.state.BackupError != "" {
		text += "\n" + t.tr("Backup error: ", "خطای پشتیبان: ") + t.state.BackupError
	}
	if t.state.ReportError != "" {
		text += "\n" + t.tr("Report error: ", "خطای گزارش: ") + t.state.ReportError
	}
	_, e := t.notify(ctx, c, text, telegramKeyboard{Rows: [][]telegramButton{
		{t.button("💾 Backup now", "💾 پشتیبان همین حالا", "backup-now"), t.button("📥 Restore", "📥 بازیابی", "restore")},
		{t.button("Toggle automatic backup", "فعال/غیرفعال خودکار", "schedule:auto_backup"), t.button("Backup time", "ساعت پشتیبان", "schedule:backup_time")},
		{t.button("🔑 Recovery key", "🔑 کلید بازیابی", "backup-key"), t.button("Import recovery key", "ورود کلید بازیابی", "schedule:backup_key")},
		{t.button("⚙️ Report schedule", "⚙️ زمان‌بندی گزارش", "prefs"), t.button("↩️ Menu", "↩️ منو", "home")},
	}})
	return e
}

func (t *Telegram) diagnostics(ctx context.Context) error {
	d := t.h.manager.Diagnostics()
	logs, _, _ := t.h.manager.logs.snapshot(logQuery{Limit: 8})
	text := fmt.Sprintf(t.tr("🩺 Tunnel diagnostics\nRunning Soar/Cotten/MasterDNS: %d/%d/%d\nUnexpected Soar exits: %d\nCotten/MasterDNS start errors: %d/%d\nOutbound attempts/failures: %d/%d\nUDP write errors: %d\nRecent diagnostic events:", "🩺 عیب‌یابی تونل\nموتورهای درحال اجرا Soar/Cotten/MasterDNS: %d/%d/%d\nخروج غیرمنتظرهٔ Soar: %d\nخطای شروع Cotten/MasterDNS: %d/%d\nتلاش/خطای اتصال خروجی: %d/%d\nخطای ارسال UDP: %d\nآخرین رویدادهای عیب‌یابی:"), d.SoarRunning, d.CottenRunning, d.MasterDNSRunning, d.SoarUnexpectedExits, d.CottenStartErrors, d.MasterDNSStartErrors, d.DialAttempts, d.DialFailures, d.UDPWriteErrors)
	for _, log := range logs {
		text += fmt.Sprintf("\n%s · %s · %s · %s (%d)", time.UnixMilli(log.LastAt).UTC().Format(time.RFC3339), log.Level, log.Code, log.Reason, log.Count)
	}
	if t.h.ops != nil {
		job, e := t.h.ops.Status(ctx)
		if e == nil {
			text += fmt.Sprintf("\nMaintenance: %s · %s\n%s", job.Kind, job.State, job.Message)
		}
	}
	_, e := t.notify(ctx, t.config(), text, telegramKeyboard{Rows: [][]telegramButton{{t.button("Refresh", "تازه‌سازی", "diagnostics"), t.button("Restart engines", "راه‌اندازی موتورها", "engines")}, {t.button("Anti-flood", "ضدفلود", "flood"), t.button("Menu", "منو", "home")}}})
	return e
}

func (t *Telegram) floodStatus(ctx context.Context) error {
	s := t.h.manager.flood.snapshot()
	text := fmt.Sprintf(t.tr("🛡 Anti-flood\nMode: %s\nDenied/observed: %d/%d\nActive streams: %d\nQuarantines: %d\nStorage errors: %d\nOpen a user to release its quarantine.", "🛡 ضدفلود\nحالت: %s\nمسدود/مشاهده‌شده: %d/%d\nجریان فعال: %d\nقرنطینه‌ها: %d\nخطای ذخیره: %d\nبرای رفع مسدودی، حساب کاربر را باز کنید."), s.Policy.Mode, s.Denied, s.Observed, s.Active, s.Quarantines, s.PersistenceErrors)
	_, e := t.notify(ctx, t.config(), text, telegramKeyboard{Rows: [][]telegramButton{{t.button("Users", "کاربران", "users:0"), t.button("Policy", "تنظیم ضدفلود", "server:antiflood")}, {t.button("Menu", "منو", "home")}}})
	return e
}

func (t *Telegram) accessOwner() string { return fmt.Sprintf("telegram:%d", t.config().ChatID) }

func (t *Telegram) accessMenu(ctx context.Context) error {
	if t.h.access == nil {
		return errors.New("access settings unavailable in this installation")
	}
	t.conversation, t.confirmation = nil, nil
	c := t.h.access.Config()
	text := t.tr("🌐 Panel access\n", "🌐 دسترسی پنل\n") + telegramJSON(c)
	k := telegramKeyboard{Rows: [][]telegramButton{{t.button("Edit domain / HTTPS / path", "تغییر دامنه / HTTPS / مسیر", "access-edit"), t.button("Refresh", "تازه‌سازی", "access")}}}
	if p := t.h.access.Plan(); p != nil && p.owner == t.accessOwner() {
		text += "\n" + p.State + "\n" + p.URL + "\n" + p.Error
		if p.State == "ready" {
			k.Rows = append(k.Rows, []telegramButton{t.button("Activate prepared address", "فعال‌سازی آدرس آماده‌شده", "access-apply:"+p.ID)})
		}
	}
	k.Rows = append(k.Rows, []telegramButton{t.button("Menu", "منو", "home")})
	_, e := t.notify(ctx, t.config(), text, k)
	return e
}
