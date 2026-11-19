# نصب پنل WhiteDNS با داکر

این راهنما برای کسانی است که می‌خواهند پنل را داخل داکر اجرا کنند. اگر دلیل خاصی برای داکر ندارید، نصب معمولی با `panel-install.sh` ساده‌تر است، چون دامنه، رکوردهای DNS و فایروال را خودش بررسی می‌کند و به‌روزرسانی و بازیابی از داخل پنل را هم دارد.

ایمیج داکر با هر انتشار پنل خودکار ساخته می‌شود و همان فایل‌های تست‌شدهٔ بستهٔ لینوکس را دارد:

```text
ghcr.io/whitedns/soar-panel:latest
ghcr.io/whitedns/soar-panel:<نسخه>
```

## پیش از شروع

- سرور لینوکس با معماری amd64 یا arm64، با Docker Engine و افزونهٔ `docker compose`.
- یک دامنه که DNS آن دست شماست، و دو رکورد NS و A مثل نصب معمولی. ساخت این دو رکورد در [راهنمای سرور](../SERVER-GUIDE.fa.md) بخش «Create two DNS records» توضیح داده شده است.
- پورت ۵۳ (UDP و TCP) روی IP اصلی سرور باید آزاد باشد. سرویس `systemd-resolved` اوبونتو مشکلی ندارد، چون فقط روی `127.0.0.53` است.
- پنل معمولی و پنل داکری را هم‌زمان روی یک سرور نصب نکنید؛ هر دو به پورت ۵۳ نیاز دارند.

کانتینر با شبکهٔ خود سرور (`network_mode: host`) اجرا می‌شود تا DNS را با IP واقعی سرور جواب بدهد. پنل داخل کانتینر با کاربر معمولی اجرا می‌شود، نه root.

## مرحلهٔ ۱: دریافت فایل compose

```bash
mkdir -p /opt/whitedns && cd /opt/whitedns
```

```bash
curl -fsSLO https://raw.githubusercontent.com/WhiteDNS/Soar-Server/main/docker/compose.yml
```

```bash
docker compose pull
```

## مرحلهٔ ۲: راه‌اندازی اول

به جای `t.example.com` دامنهٔ تونل، به جای `ns1.example.com` نام nameserver و به جای `203.0.113.10` IP عمومی سرور را بگذارید. رمز مدیر باید ۱۲ تا ۷۲ نویسهٔ انگلیسی باشد. دستور رمز را می‌پرسد و آن را روی صفحه نشان نمی‌دهد:

```bash
read -rsp 'Admin password: ' P; echo; printf '%s' "$P" | docker compose run --rm -T panel init --zone t.example.com --nameserver ns1.example.com --public-ip 203.0.113.10 --password-file -; unset P
```

بعد پنل را روشن کنید:

```bash
docker compose up -d
```

اگر IP اصلی سرور با IP مسیر پیش‌فرض فرق دارد (مثلاً پشت NAT)، کنار `compose.yml` یک فایل `.env` با این خط بسازید و دوباره `docker compose up -d` بزنید:

```text
SOAR_DNS_LISTEN=10.0.0.5:53
```

## مرحلهٔ ۳: باز کردن پنل

پنل در ابتدا فقط روی خود سرور (`127.0.0.1:2053`) باز است. از کامپیوتر خودتان با تونل SSH وصل شوید:

```bash
ssh -L 2053:127.0.0.1:2053 root@203.0.113.10
```

حالا در مرورگر کامپیوتر خودتان `http://127.0.0.1:2053` را باز کنید و با `admin` و رمزی که گذاشتید وارد شوید.

برای دسترسی دائمی با HTTPS، در پنل به «دامنه و امنیت» بروید و دامنهٔ پنل را تنظیم کنید. پنل گواهی را خودش از Let's Encrypt می‌گیرد. در داکر، باز کردن پورت‌ها در فایروال سرور با شماست؛ اگر UFW فعال است:

```bash
ufw allow 53 && ufw allow 80/tcp && ufw allow 443/tcp
```

## به‌روزرسانی

دکمهٔ نصب نسخهٔ جدید داخل پنل در داکر کار نمی‌کند؛ صفحهٔ «به‌روزرسانی پنل» همین دستور را نشان می‌دهد. در پوشهٔ `/opt/whitedns` اجرا کنید:

```bash
docker compose pull && docker compose up -d
```

اطلاعات پنل در volume داکر می‌ماند و با به‌روزرسانی پاک نمی‌شود. برای ثابت نگه داشتن نسخه، در فایل `.env` بنویسید `SOAR_PANEL_VERSION=0.16.0` (یا هر نسخهٔ دیگر).

## پشتیبان و بازیابی

**پشتیبان:** از صفحهٔ «پشتیبان و بازیابی» پنل، دکمهٔ «دانلود پشتیبان» مثل نصب معمولی کار می‌کند. فایل شامل کلیدهای حساب‌هاست؛ آن را امن نگه دارید.

**بازیابی:** داخل پنل در داکر ممکن نیست. فایل پشتیبان را مثلاً با نام `backup.db` در `/opt/whitedns` بگذارید و این دستورها را به ترتیب اجرا کنید. پنل خاموش می‌شود، نسخهٔ فعلی پایگاه داده با نام `panel.db.before-restore` نگه داشته می‌شود و پشتیبان جای آن می‌نشیند:

```bash
docker compose down
```

```bash
docker run --rm -v whitedns_panel-data:/data -v "$PWD/backup.db:/in/backup.db:ro" alpine sh -c 'cp /data/panel.db /data/panel.db.before-restore && cp /in/backup.db /data/panel.db && chown 65532:65532 /data/panel.db && chmod 600 /data/panel.db'
```

```bash
docker compose up -d
```

نام volume (`whitedns_panel-data`) از نام پوشه می‌آید؛ اگر پوشهٔ دیگری استفاده کرده‌اید، نام را با `docker volume ls` پیدا کنید. بعد از این بازیابی، رمز مدیر همان رمز داخل فایل پشتیبان است. دامنهٔ تونل فایل پشتیبان باید همان دامنهٔ این سرور باشد.

## فراموشی رمز مدیر

```bash
docker compose down
```

```bash
read -rsp 'New admin password: ' P; echo; printf '%s' "$P" | docker compose run --rm -T panel admin-reset --password-file -; unset P
```

```bash
docker compose up -d
```

## لاگ و وضعیت

```bash
docker compose logs -f panel
```

```bash
docker compose ps
```

## تفاوت‌ها با نصب معمولی

- به‌روزرسانی و بازیابی از داخل پنل یا ربات تلگرام در دسترس نیست؛ از دستورهای بالا استفاده کنید.
- نصب‌کننده در داکر نیست؛ ساخت درست رکوردهای DNS و باز بودن پورت‌ها را خودتان بررسی کنید.
- بقیهٔ امکانات پنل (حساب‌ها، ساب، چند دامنه، ضدفلاد، تلگرام، HTTPS) یکسان است.
