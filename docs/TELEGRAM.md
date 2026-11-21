# Telegram management / مدیریت با تلگرام

Panel 0.12.0 adds emergency server settings, diagnostics, anti-flood release,
panel-domain/HTTPS migration, encrypted manual/daily backups and validated restore.
See [the Persian setup and recovery guide](telegram-emergency-management.fa.md).

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
برای مالک ربات ثبت می‌شود؛ وارد کردن دستی آن لازم نیست.

Keep the connection link private: whoever opens it before it expires can become
this server's Telegram bot owner. A plain `/start` from an unknown person
never grants access. The link is issued only inside the authenticated dashboard,
requires a private chat with matching sender/chat IDs, and is consumed durably
before access is granted. Replacing the bot token requires fresh pairing.
Previously paired installations retain the same owner without re-entering an ID.
Disconnect Telegram before pairing a different owner.

The token is a secret. The panel never displays a saved token: leave its input
blank to preserve it. Use **Disable bot** to pause management and notifications
while keeping the credentials; **Disconnect Telegram** removes credentials and
bot state without touching server accounts. The local backup recovery key is
retained so existing encrypted backups remain recoverable. If a token leaks, revoke/replace it
in BotFather, then save the replacement in the dashboard.

## Management / مدیریت

The bot supports Persian and English. Choose **فارسی / English** at the top of
the first user or administrator menu. Each administrator can save a chat
language. Use buttons to list/search
users, add/edit accounts, change engines and all account limits, enable/disable,
reset usage, disconnect connections, delete accounts, rotate tunnel keys or the
subscription URL, and receive subscription/direct connection strings in copyable
code blocks. The dashboard also supports sharing QR images.

Quota uses **GiB**, speed uses **KiB/s**, and zero means unlimited for quota,
speed, expiry and TCP stream limits. Session limits have the same bounds as the
web panel. TCP streams and engine sessions are not device counts. Custom expiry
uses a future **Gregorian `YYYY-MM-DD HH:MM`** in the configured report timezone.
Persian and Arabic digits are accepted for numeric values.

Creation defaults to both engines and the dashboard's default limits. Review
all changes before confirming. Confirmations last five minutes and work once;
forms last fifteen minutes. `/cancel` discards the current form. `/menu` returns
home; `/stats` returns current server resources, uptime, version, account counts, active TCP streams and usage totals; `/settings` opens server settings; `/backup`, `/restore`, `/diagnostics` and `/access` open the corresponding management menus. Preferences are available from the main menu; `/help` describes the controls. Concurrent
web edits invalidate stale account confirmations.

Management accepts authorized senders in their matching **private** chats. The
owner can open **Free configs → Bot administrators** to add up to eight
administrators by numeric Telegram ID, choose their role, change it, or remove
them. Added administrators open the bot and send `/start`. Each administrator
has a separate form and confirmation. Changing a role or removing an administrator
invalidates their open forms and confirmations immediately.

- **Free configurations** manages the public service, plans, requests,
  Telegram users and broadcasts.
- **Full management** also includes server settings, all panel accounts,
  maintenance, and access to encrypted backups and the recovery key.

Only the owner grants, changes or removes these roles. Disconnecting or
replacing the bot owner or token revokes the additional administrator list.

مالک از «داشبورد کانفیگ رایگان ← مدیران ربات»، شناسهٔ عددی هر مدیر را وارد
می‌کند و نقش «مدیر کانفیگ رایگان» یا «مدیر کامل ربات» را انتخاب می‌کند.
برای تغییر نقش یا حذف، شناسه را در همان فهرست باز کنید. مدیر کامل به
پشتیبان‌ها و کلید بازیابی هم دسترسی دارد. ربات پیام مدیریتی را در گفت‌وگوی
خصوصی همان مدیر می‌پذیرد. عملیات نوشتن، حذف،
تعویض کلید، صفر کردن مصرف و نگهداری پس از بررسی و تأیید انجام می‌شوند. تأییدها
یک‌بارمصرف و پنج‌دقیقه‌ای هستند. فرم‌ها پس از پانزده دقیقه منقضی می‌شوند. تغییر
هم‌زمان از پنل، تأیید قدیمی حساب را نامعتبر می‌کند. رمز پنل را در تلگرام نفرستید.

Restart restarts `soar-panel.service`, its tunnels and bot, briefly dropping
connections. Updates install newer **official stable WhiteDNS panel bundles**
through the existing verified update/backup/readiness/rollback workflow. They do
not upgrade OS packages, reboot the server or run arbitrary commands. Server-wide
settings and backup/restore are available from the bot. Bulk operations and
administrator credential changes remain in the dashboard.

## Free configurations / کانفیگ رایگان

Open `/admin` or **Free configs** from the paired administrator chat. Public
access starts off. Set up the plans and engines, then open **Public access and
messages** and choose **Enable public access**. Share the ordinary bot link or
`?start=free`. Keep the administrator pairing link private.

**Plans and engines** supports drafts, edit, publish, unpublish and delete. Use the
inline editor to change individual fields, then save. **Public engines** controls
which of Soar, CottenDNS and MasterDNS are available.
Each plan has a quota (MiB), validity (days), speed (KiB/s), shared TCP stream
limit and an independent session limit for each engine. The default published
plan is MasterDNS with 1 GiB, 7 days, 512 KiB/s, 8 streams and 2 sessions.
Users select from the available published plans and answer a five-minute
one-use code with a short reason. Approval uses the terms saved at submission.
Withdrawing a plan or required engine holds the pending request.

The dashboard provides request review with individual and selected approvals
and rejections, statistics, all and active account lists, search, individual
and selected account deletion, custom configurations, Telegram access controls,
and editing of English and Persian greetings, terms and connection instructions.
New pending requests are grouped into one notification per administrator per
15-second check, with a button to open the queue. Delivery is tracked
independently for each administrator.
To link an existing account, open it from **Users** and choose **Link to Telegram
user**. The account must have finite limits and the recipient must have opened
the bot. Bulk deletion is confirmed against the account revisions shown in the
preview. A custom configuration for an existing linked account edits the same
account and preserves the keys and usage history.

Open **Accounts → Manage a Telegram user** and enter the numeric Telegram ID,
or open the user from a pending request. This page shows the request or account,
manual blocks and the original deadline to submit another request. For a
declined or cancelled request, or an account removed from the panel, the
administrator can confirm **Allow one early retry** or withdraw the exception
before it is used. A new request still goes through verification, approval,
and the system's admission and issuance limits.

The public chat's Telegram **Menu** offers quick commands for the main menu,
applications, account status, help and language. Inline navigation updates the
current bot message. **My account** lets users check status, cancel a pending
request, or get a connection link or QR code for a selected engine. An approved
account has one shared volume and expiry across its selected engines.
Account readiness means the panel has registered the tunnel route.
CottenDNS and MasterDNS start on demand when the first DNS query arrives.

در «داشبورد کانفیگ رایگان»، مدیر از بخش «پلن‌ها و موتورها» نام، حجم، مدت و
محدودیت‌های پلن را جداگانه تنظیم می‌کند. اعلان درخواست‌های تازه با دکمهٔ
«مشاهدهٔ درخواست‌ها» در گفت‌وگوی خصوصی مدیر می‌آید. پس از فعال‌کردن دسترسی
عمومی، کاربر از دکمهٔ Menu تلگرام یا منوی درون پیام به درخواست رایگان، حساب،
راهنما و انتخاب زبان دسترسی دارد.

در «حساب‌ها ← مدیریت کاربر تلگرام»، وضعیت درخواست، مسدودی یا زمان پایان
مهلت را با شناسهٔ عددی ببینید. برای درخواست رد یا لغوشده و کانفیگ حذف‌شده،
«اجازهٔ یک درخواست مجدد» را بعد از بازبینی تأیید کنید. تا قبل از استفاده،
امکان لغو این اجازه هم وجود دارد. مهلت و مسیر بعدی در پیام کاربر نمایش داده
می‌شوند.

**Pause applications** stops new requests. Administrators can still review
saved requests and existing owners can retrieve their links. **Emergency stop**
closes the public bot workflows, stops the current broadcast, and pauses new requests until explicitly
reopened. Issued tunnels follow their account settings. Use the account's
Enable / Disable control to suspend the tunnel itself. After database restore,
public delivery and issuance are held until the administrator explicitly
confirms the restore review. Old broadcast jobs are cleared on restore and
cannot resume with a replaced token.

The bot has separate bounded work queues for admissions and existing owners.
Valid challenge replies have reserved space. At most 8 other admission
operations and 12 total admission operations are processed per minute. An ID
can receive up to 3 challenges per hour. The queue holds up to 20 pending
requests, and at most 40 applications and 10 new accounts are accepted in a
rolling 24-hour period. Pending, cancelled or rejected requests use a 24-hour
window. Existing free-linked accounts (including expired ones) are limited to
100, and 20 ordinary account slots are reserved. New or renewed free accounts
are checked against a combined budget of 512 configured TCP streams. A flood
of new requests may delay admission while existing owners retain separate
capacity. Upstream Telegram throttling can still affect the shared bot.
Submitted requests and issuance counts survive a restart. Users with an
unfinished challenge can request a fresh code.

Broadcasts preview and confirm a fixed audience, recheck eligibility when
sending, and report sent, skipped, failed and uncertain outcomes. They run at
the standard free send rate. Stop ends future sends. A request already in
flight can complete. The database and backups contain numeric Telegram IDs,
preferences and current pending explanations. Explanations are cleared at
review and expired requests are pruned regularly.

در چت خصوصی مدیر `/admin` یا «کانفیگ رایگان» را باز کنید. موتورهای عمومی و
پلن‌ها را انتخاب کنید و سپس «فعال‌کردن دسترسی عمومی» را بزنید. کاربران با
`/start` پلن رایگان را انتخاب می‌کنند و کد و توضیح را در پاسخ به همان پیام
می‌فرستند. پس از تأیید، لینک و QR هر موتور را جداگانه می‌گیرند. حجم و انقضا
برای تمام موتورهای یک حساب مشترک است.

«توقف پذیرش» جلوی درخواست تازه را می‌گیرد و تحویل کانفیگ به صاحبان حساب را
باز می‌گذارد. «توقف اضطراری» مسیر عمومی ربات را می‌بندد. برای قطع خود کانفیگ،
حساب را غیرفعال کنید. داشبورد مدیر شامل آمار، صف بررسی تکی و گروهی، فهرست
و جست‌وجو، ساخت و تمدید سفارشی، تنظیم پلن‌ها و متن‌های عمومی، و پیام همگانی است.

## Alerts and daily report / هشدار و گزارش

Preferences are optional; the bot token is the only required input, followed by the one-time connection link.
Defaults: transition alerts enabled; daily report at **09:00 Asia/Tehran**.
Change time, IANA timezone, language, alerts or reports in the dashboard or bot.
Automatic encrypted backups are opt-in and initially scheduled for 03:00 in the
same timezone. Keep the recovery key separately outside this server before relying
on backups. Only the validated, confirmed restore route applies uploaded backups.

Alerts cover account expiry/quota exhaustion, detected runtime failures/recovery
and newer stable releases. Initial setup establishes a baseline without sending
historical account alerts. Runtime/account checks run about once a minute;
release checks run every six hours. A separate one-second scheduler checks report
and backup deadlines even when incoming polling fails or blocks. Reports describe the **current snapshot**:
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
