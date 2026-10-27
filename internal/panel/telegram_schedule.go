package panel

import (
	"context"
	"time"
)

// Polling is deliberately independent: a getUpdates conflict must not stop
// outgoing reports/backups. All dispatch and state changes still share opMu.
func (t *Telegram) runSchedule(ctx context.Context) {
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	nextAlerts := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		t.opMu.Lock()
		// Restore previews can hold a full database; release expired drafts even
		// when the administrator never sends another message.
		if t.confirmation != nil && !t.now().Before(t.confirmation.Expires) {
			t.confirmation = nil
		}
		if t.conversation != nil && !t.now().Before(t.conversation.Expires) {
			t.conversation = nil
		}
		c := t.config()
		if ctx.Err() == nil && c.Enabled && c.ChatID > 0 {
			if !t.state.Booted {
				t.state.Booted = true
				if e := t.save(); e != nil {
					t.state.Booted = false
					t.connection(e)
					t.opMu.Unlock()
					continue
				}
			}
			t.scheduled(ctx)
			if !t.now().Before(nextAlerts) {
				t.tick(ctx)
				nextAlerts = t.now().Add(time.Minute)
			}
		}
		t.opMu.Unlock()
	}
}

// Separate retry deadlines keep a failed report from suppressing a backup.
// Dates advance only after Telegram accepts the corresponding payload.
func (t *Telegram) scheduled(ctx context.Context) {
	c := t.config()
	if !c.Enabled || c.ChatID == 0 {
		return
	}
	if c.DailyReport && !t.now().Before(t.reportRetry) {
		if day, due := telegramReportDue(t.now(), c, t.state.LastReport, t.state.ReportStarted); due {
			job, cancel := context.WithTimeout(ctx, 45*time.Second)
			text, e := t.report()
			if e == nil {
				_, e = t.notify(job, c, text, nil)
			}
			cancel()
			if e == nil {
				t.state.LastReport = day
				t.state.ReportError = ""
				e = t.save()
			}
			if e != nil {
				t.state.ReportError = e.Error()
				_ = t.save()
				t.connection(e)
				t.reportRetry = t.now().Add(time.Minute)
			}
		}
	}
	if c.AutoBackup && !t.now().Before(t.backupRetry) {
		schedule := c
		schedule.ReportTime = c.BackupTime
		if day, due := telegramReportDue(t.now(), schedule, t.state.LastBackup, t.state.BackupStarted); due {
			job, cancel := context.WithTimeout(ctx, 90*time.Second)
			e := t.sendBackup(job)
			cancel()
			if e == nil {
				t.state.LastBackup = day
				t.state.BackupError = ""
				e = t.save()
			}
			if e != nil {
				t.state.BackupError = e.Error()
				_ = t.save()
				t.connection(e)
				t.backupRetry = t.now().Add(time.Minute)
			}
		}
	}
}
