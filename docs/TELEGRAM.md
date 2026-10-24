# Telegram management / مدیریت با تلگرام

## Connect / اتصال

1. Create a **dedicated bot for this server** using `/newbot` in Telegram's official
   [BotFather](https://t.me/BotFather). Keep its token private. Do not reuse a bot
   that has a webhook or is running on another server.
2. In the panel, open **Server settings → Telegram management**. Enter only the
   bot token and choose **Save and connect**. No chat ID, webhook URL, new public
   port, domain or extra installation is required.
3. Choose **Connect my Telegram account** in the dashboard. Open the link in your
   own Telegram account and press **Start**. The link expires after five minutes
   and works once; **Generate a new connection link** replaces an expired link.
4. The bot confirms pairing and shows the management buttons. The dashboard
   automatically changes to connected; **Send test message** is then available.

در BotFather با دستور `/newbot` یک ربات اختصاصی برای همین سرور بسازید. فقط توکن
را در «تنظیمات سرور ← مدیریت با تلگرام» ذخیره کنید. «اتصال حساب تلگرام من» را
بزنید و در تلگرام با حساب خودتان Start را انتخاب کنید. شناسهٔ چت خودکار و فقط
برای همین مدیر ثبت می‌شود؛ وارد کردن دستی آن لازم نیست.

Keep the connection link private: whoever opens it before it expires can become
this server's Telegram administrator. A plain `/start` from an unknown person
never grants access. The link is issued only inside the authenticated dashboard,
requires a private chat with matching sender/chat IDs, and is consumed durably
before access is granted. Replacing the bot token requires fresh pairing.
Previously paired installations retain their administrator without re-entering
an ID. Disconnect Telegram before pairing a different administrator.

The token is a secret. The panel never displays a saved token: leave its input
blank to preserve it. Use **Disable bot** to pause management and notifications
while keeping the credentials; **Disconnect Telegram** removes credentials and
bot state without touching server accounts. If a token leaks, revoke/replace it
in BotFather, then save the replacement in the dashboard.

## Management / مدیریت

The bot supports Persian and English. The first authorized message selects the
initial language; change it under **Preferences**. Use buttons to list/search
users, add/edit accounts, change engines and all account limits, enable/disable,
reset usage, disconnect connections, delete accounts, rotate tunnel keys or the
subscription URL, and receive subscription/direct connection strings in copyable
code blocks. The Telegram bot sends no QR images; dashboard QR sharing remains available.

Quota uses **GiB**, speed uses **KiB/s**, and zero means unlimited for quota,
speed, expiry and TCP stream limits. Session limits have the same bounds as the
web panel. TCP streams and engine sessions are not device counts. Custom expiry
uses a future **Gregorian `YYYY-MM-DD HH:MM`** in the configured report timezone.
Persian and Arabic digits are accepted for numeric values.

Creation defaults to both engines and the dashboard's default limits. Review
all changes before confirming. Confirmations last five minutes and work once;
forms last fifteen minutes. `/cancel` discards the current form. `/menu` returns
home; `/stats` returns current server resources, uptime, version, account counts, active TCP streams and usage totals; `/settings` opens preferences; `/help` describes the controls. Concurrent
web edits invalidate stale account confirmations.

Only the configured sender in the configured **private** chat is authorized.
Groups, channels, forwarded callback identities and other Telegram users cannot
manage the server. Keep the administrator's Telegram account secure: possession
of that account authorizes button-confirmed operations without a panel password.
Web password checks remain unchanged.

سرور فقط پیام و دکمهٔ مدیر تعیین‌شده در چت خصوصی را می‌پذیرد. عملیات نوشتن، حذف،
تعویض کلید، صفر کردن مصرف و نگهداری پس از بررسی و تأیید انجام می‌شوند. تأییدها
یک‌بارمصرف و پنج‌دقیقه‌ای هستند. فرم‌ها پس از پانزده دقیقه منقضی می‌شوند. تغییر
هم‌زمان از پنل، تأیید قدیمی حساب را نامعتبر می‌کند. رمز پنل را در تلگرام نفرستید.

Restart restarts `soar-panel.service`, its tunnels and bot, briefly dropping
connections. Updates install newer **official stable WhiteDNS panel bundles**
through the existing verified update/backup/readiness/rollback workflow. They do
not upgrade OS packages, reboot the server or run arbitrary commands. Server-wide
settings, bulk operations, backup/restore and administrator credentials stay in
the dashboard.

## Alerts and daily report / هشدار و گزارش

Preferences are optional; the bot token is the only required input, followed by the one-time connection link.
Defaults: transition alerts enabled; daily report at **09:00 Asia/Tehran**.
Change time, IANA timezone, language, alerts or reports in the dashboard. The bot
also provides language and alert/report toggles.

Alerts cover account expiry/quota exhaustion, detected runtime failures/recovery
and newer stable releases. Initial setup establishes a baseline without sending
historical account alerts. Runtime/account checks run about once a minute;
release checks run every six hours. Reports describe the **current snapshot**:
resources, uptime/version, account state counts, accounts with active streams,
current upload/download totals and five accounts with highest current usage.
These are not previous-day traffic statistics. A missed report is retried once
on recovery without sending a backlog of older reports.

The bot runs inside the panel. It cannot send during a complete panel or server
outage. It reports recovery after restarting, and the result of requested
maintenance once the panel is back. Runtime alerts reflect diagnostics available
inside the panel, not an external reachability or uptime guarantee.

گزارش پیش‌فرض ساعت ۹ صبح تهران، وضعیت فعلی را می‌فرستد؛ مصرف آن مربوط به مجموع
فعلی حساب‌هاست، نه مصرف روز گذشته. هنگام قطع کامل سرور یا پنل، ربات نمی‌تواند
پیام بفرستد و پس از بازگشت سرویس بازیابی را گزارش می‌کند.

## Network and recovery / شبکه و بازیابی

The server needs direct outbound HTTPS access to `api.telegram.org:443`, plus the
existing official GitHub release hosts for update checks/downloads. No inbound
Telegram listener is installed. Network failures retry with backoff; Telegram
rate limits are respected. Tunnels and dashboard continue working independently.
A polling conflict means another consumer is using this token; stop it or use a
new dedicated bot. A webhook conflict requires a dedicated bot without a webhook.
Access denied usually means the bot is blocked. If pairing has expired, generate a new connection link in the dashboard.

Credentials, update offsets and notification/action markers live in private
`telegram.json` beside `panel.db` (normally `/var/lib/soar-panel/telegram.json`),
with mode `0600`. They are outside downloadable database backups, so restoring
another server's database does not clone its Telegram administrator. Existing
server Telegram configuration survives database restores and panel updates.
Protect this file when taking machine-level backups.

Dispatched updates and confirmation actions are persisted before mutation.
After a crash, the bot does not replay an ambiguous account operation: inspect
the current account state before trying again. Persisted maintenance job status
is used to report restart/update outcomes.

If the configuration file is damaged, stop the panel service, move the damaged
file to a private recovery location, restart the service and reconnect from the
dashboard. Never publish this file in an issue, log or support attachment.

## Development checks

After preparing the pinned engine workspace as described in the README, run:

```sh
go test ./internal/panel ./internal/panelops -run 'Telegram|PanelRestart' -count=1
go test -race ./internal/panel ./internal/panelops -run 'Telegram|PanelRestart' -count=1
```

The Go tests use a local fake Bot API; no real bot token is needed. The existing
`tests/panel-ui.cjs` suite includes token-only settings, pairing-link controls,
token privacy, Persian/English and mobile checks. Real Telegram delivery and
systemd restart/update behavior still require a disposable Linux VPS and bot.


## MasterDNS (0.8.0)

The engine menu supports MasterDNS, any pair, or all three engines. The separate MasterDNS session control accepts 1–255. Account operations, shared quotas, profile sharing, confirmed changes and lazy-engine failure/recovery alerts include MasterDNS. Existing Both selections retain Soar+Cotten.

منوی موتور، MasterDNS و ترکیب‌های دو یا سه‌موتوره را هم دارد. سقف نشست MasterDNS از ۱ تا ۲۵۵ است؛ حجم، سرعت و انقضا با موتورهای همان حساب مشترک‌اند. گزینهٔ قدیمی «هر دو» همچنان Soar و Cotten است.
