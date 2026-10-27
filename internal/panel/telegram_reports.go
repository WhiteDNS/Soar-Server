package panel

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Telegram always offers stable releases even when the installed panel is a beta.
func LatestStablePanelRelease(ctx context.Context) (PanelRelease, error) {
	var releases []PanelRelease
	if e := getReleaseJSON(ctx, "releases?per_page=50", &releases); e != nil {
		return PanelRelease{}, e
	}
	return selectPanelRelease(releases, "0.0.0")
}
func (t *Telegram) report() (string, error) {
	list, e := t.h.store.List()
	if e != nil {
		return "", e
	}
	states := map[string]int{}
	var up, down int64
	counts := t.h.manager.ActiveCounts()
	online, streams := 0, 0
	users := []Client{}
	for _, c := range list {
		if c.InstallerProbe {
			continue
		}
		users = append(users, c)
		streams += counts[c.ID]
		states[c.Status(t.now())]++
		up += c.UploadBytes
		down += c.DownloadBytes
		if counts[c.ID] > 0 {
			online++
		}
	}
	h := t.h.metrics.sample()
	metric := func(key string) string {
		v := h[key]
		if v == nil {
			return "—"
		}
		return fmt.Sprint(v)
	}
	text := fmt.Sprintf(t.tr("📊 WhiteDNS · current state snapshot\nVersion: %s\n⏱️ Uptime: %s\nAccounts: %d · with active streams: %d\nActive TCP streams: %d\nActive / Disabled / Expired / Exhausted: %d / %d / %d / %d\n📡 Current account totals (not daily traffic)\nUpload: %.2f GiB · Download: %.2f GiB\n🖥️ CPU: %s%%\n🧠 Memory used/total (bytes): %s / %s\n💾 Disk used/total (bytes): %s / %s\nTop accounts by current usage:", "📊 WhiteDNS · گزارش وضعیت فعلی\nنسخه: %s\n⏱️ زمان اجرای سرویس: %s\nحساب‌ها: %d · دارای جریان فعال: %d\nجریان TCP فعال: %d\nفعال / غیرفعال / منقضی / حجم تمام‌شده: %d / %d / %d / %d\n📡 مجموع مصرف فعلی حساب‌ها (نه مصرف روزانه)\nآپلود: %.2f GiB · دانلود: %.2f GiB\n🖥️ پردازنده: %s%%\n🧠 حافظهٔ مصرفی/کل (بایت): %s / %s\n💾 دیسک مصرفی/کل (بایت): %s / %s\nحساب‌های دارای بیشترین مصرف فعلی:"), Version, t.now().Sub(t.h.manager.started).Round(time.Second), len(users), online, streams, states["active"], states["disabled"], states["expired"], states["exhausted"], float64(up)/(1<<30), float64(down)/(1<<30), metric("cpu_percent"), metric("memory_used"), metric("memory_total"), metric("disk_used"), metric("disk_total"))
	sort.Slice(users, func(i, j int) bool { return users[i].Used() > users[j].Used() })
	for _, c := range users[:min(5, len(users))] {
		text += fmt.Sprintf("\n%s · %.2f GiB", c.Name, float64(c.Used())/(1<<30))
	}
	return text, nil
}
func telegramReportDue(now time.Time, c TelegramConfig, last string, started int64) (string, bool) {
	loc, e := time.LoadLocation(c.Timezone)
	if e != nil {
		return "", false
	}
	local := now.In(loc)
	if last == "" && started > 0 {
		last = time.UnixMilli(started).In(loc).AddDate(0, 0, -1).Format("2006-01-02")
	}
	day := local.Format("2006-01-02")
	at, e := time.ParseInLocation("2006-01-02 15:04", day+" "+c.ReportTime, loc)
	if e != nil {
		return "", false
	}
	if !now.Before(at) && last != day {
		return day, true
	}
	// Before today's delivery time, recover only yesterday's missed report.
	yesterday := local.AddDate(0, 0, -1).Format("2006-01-02")
	if last != "" && last < yesterday {
		return yesterday, true
	}
	return day, false
}
func (t *Telegram) tick(ctx context.Context) {
	c := t.config()
	if !c.Enabled || c.ChatID == 0 {
		return
	}
	t.scheduled(ctx)
	if t.recovered {
		if _, e := t.notify(ctx, c, t.tr("✅ The WhiteDNS panel is running again.", "✅ پنل WhiteDNS دوباره در حال اجراست."), nil); e != nil {
			return
		}
		t.recovered = false
	}
	if t.state.PendingAction != "" {
		action := t.state.PendingAction
		if action == "update" || action == "restart" || action == "restore" {
			if t.h.ops != nil {
				job, e := t.h.ops.Status(ctx)
				if e == nil {
					match := job.ID == t.state.PendingJob || (t.state.PendingJob == "" && job.Kind == action && job.Started >= t.state.PendingSince)
					if match && job.State != "running" {
						text := t.tr("🛠️ Maintenance result: ", "🛠️ نتیجهٔ نگهداری: ") + job.State + "\n" + job.Message
						if _, e = t.notify(ctx, c, text, nil); e != nil {
							return
						}
						if e = t.finishAction(nil); e != nil {
							t.connection(e)
							return
						}
					} else if !match && t.now().UnixMilli()-t.state.PendingSince > 5*60*1000 {
						if _, e = t.notify(ctx, c, t.tr("⚠️ Maintenance result is uncertain. Check server status before retrying.", "⚠️ نتیجهٔ نگهداری مشخص نیست. پیش از تلاش دوباره وضعیت سرور را بررسی کنید."), nil); e != nil {
							return
						}
						if e = t.finishAction(nil); e != nil {
							t.connection(e)
							return
						}
					}
				}
			}
		} else {
			if _, e := t.notify(ctx, c, t.tr("⚠️ An account action was interrupted. Check current account state before retrying; it will not be replayed.", "⚠️ یک عملیات حساب قطع شده است. پیش از تلاش دوباره وضعیت فعلی حساب را بررسی کنید؛ عملیات تکرار نمی‌شود."), nil); e != nil {
				return
			}
			if e := t.finishAction(nil); e != nil {
				t.connection(e)
				return
			}
		}
	}
	list, e := t.h.store.List()
	if e != nil {
		return
	}
	baseline := t.state.Accounts == nil
	if baseline {
		t.state.Accounts = map[string]string{}
	}
	current := map[string]string{}
	for _, account := range list {
		if account.InstallerProbe {
			continue
		}
		status := account.Status(t.now())
		current[account.ID] = status
		old, known := t.state.Accounts[account.ID]
		if c.Alerts && !baseline && known && old != status && (status == "expired" || status == "exhausted") {
			if _, e = t.notify(ctx, c, t.tr("⚠️ Account alert: ", "⚠️ هشدار حساب: ")+account.Name+" · "+t.accountStatus(account), nil); e != nil {
				return
			}
		}
		// Persist per transition so a later failed notification does not repeat earlier ones.
		t.state.Accounts[account.ID] = status
		if known && old != status {
			if e = t.save(); e != nil {
				t.connection(e)
				return
			}
		}
	}
	t.state.Accounts = current
	d := t.h.manager.Diagnostics()
	var expectedSoar, expectedCotten, expectedMaster int64
	for _, account := range list {
		if account.Status(t.now()) != "active" {
			continue
		}
		if account.hasEngine("soar") {
			expectedSoar++
		}
		if account.hasEngine("masterdns") {
			expectedMaster++
		}
		if account.hasEngine("cotten") {
			expectedCotten++
		}
	}
	// Cotten starts on first traffic. Never treat an unused lazy engine as down.
	newCottenFailure := t.state.RuntimeKnown && d.CottenStartErrors > t.state.CottenIssues
	if newCottenFailure {
		t.state.CottenFailed = true
		t.state.CottenRecoveryStarts = d.CottenStarts
	}
	if expectedCotten == 0 || (!newCottenFailure && d.CottenStarts > t.state.CottenRecoveryStarts) {
		t.state.CottenFailed = false
	}
	newMasterFailure := t.state.RuntimeKnown && d.MasterDNSStartErrors > t.state.MasterDNSIssues
	if newMasterFailure {
		t.state.MasterDNSFailed = true
		t.state.MasterDNSRecoveryStarts = d.MasterDNSStarts
	}
	if expectedMaster == 0 || (!newMasterFailure && d.MasterDNSStarts > t.state.MasterDNSRecoveryStarts) {
		t.state.MasterDNSFailed = false
	}
	t.state.MasterDNSIssues = d.MasterDNSStartErrors
	failed := (d.SoarUnexpectedExits > 0 && d.SoarRunning < expectedSoar) || t.state.CottenFailed || t.state.MasterDNSFailed
	t.state.CottenIssues = d.CottenStartErrors
	if c.Alerts && t.state.RuntimeKnown && failed != t.state.RuntimeFailed {
		text := t.tr("✅ Tunnel runtimes recovered.", "✅ موتورهای تونل بازیابی شدند.")
		if failed {
			text = t.tr("🚨 Tunnel runtime failure detected. Check dashboard diagnostics.", "🚨 اختلال موتور تونل شناسایی شد. عیب‌یابی داشبورد را بررسی کنید.")
		}
		if _, e = t.notify(ctx, c, text, nil); e != nil {
			return
		}
	}
	t.state.RuntimeKnown = true
	t.state.RuntimeFailed = failed
	if c.Alerts && t.now().Unix()-t.state.LastReleaseCheck >= int64((6*time.Hour)/time.Second) {
		r, e := t.latest(ctx)
		if e == nil {
			v := strings.TrimPrefix(r.Tag, "panel-v")
			if !r.Prerelease && !strings.Contains(v, "-") && NewerVersion(v, Version) && validatePanelRelease(r, v) == nil && t.state.LastRelease != v {
				if _, e = t.notify(ctx, c, t.tr("🆕 New official stable panel release: ", "🆕 نسخهٔ پایدار رسمی جدید پنل: ")+v+"\n"+r.URL, nil); e != nil {
					return
				}
				t.state.LastRelease = v
			}
			t.state.LastReleaseCheck = t.now().Unix()
		} else {
			t.state.LastReleaseCheck = t.now().Add(-6*time.Hour + 15*time.Minute).Unix()
		}
	}
	if e = t.save(); e != nil {
		t.connection(fmt.Errorf("cannot persist Telegram notification state"))
	}
}
