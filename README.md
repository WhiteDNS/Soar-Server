# WhiteDNS Soar Server

Guided Soar server installer for **Ubuntu 24.04 LTS, amd64/x86_64**.
The SSH terminal uses English ASCII text. Each installation step explains what
to enter and why; a full Persian guide is included.

## نصب با یک دستور

ابتدا با SSH وارد VPS شوید. سپس همین دستور را در ترمینال سرور اجرا کنید:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/WhiteDNS/Soar-Server/main/setup.sh)
```

اسکریپت بستهٔ نسخه‌دار و checksum را از Releases می‌گیرد، تطبیق فایل‌ها را
بررسی می‌کند و نصب هفت‌مرحله‌ای را در همان ترمینال ادامه می‌دهد. کاربر عادی
با دسترسی sudo هم می‌تواند اجرا کند؛ ابزارهای لازم پس از توضیح و تأیید نصب می‌شوند.
اگر `curl: command not found` دیدید، ابتدا `sudo apt-get update` و سپس
`sudo apt-get install -y curl` را اجرا کنید و دستور بالا را دوباره بزنید.

**[راهنمای کامل فارسی، از ورود SSH تا DNS و اتصال گوشی](SERVER-GUIDE.fa.md)**

به یک دامنه و دسترسی پنل DNS آن نیاز دارید. نام دامنه و رکوردهای دقیق را
نصب‌کننده توضیح می‌دهد. برای Soar زیردامنهٔ جدا بسازید؛ رکوردهای MasterDNS
و CottenDNS موجود را تغییر ندهید. DNSهای فعال سرور به‌صورت خودکار خاموش نمی‌شوند.

## After installation

```bash
sudo soar-manager             # explained management menu
sudo soar-manager test        # authenticated tunnel + certificate-verified HTTPS
sudo soar-manager profile     # complete IR or GLOBAL import link for WhiteDNS
sudo soar-manager status
```

Paste the generated `soar://` link into the official
[WhiteDNS Android app](https://github.com/WhiteDNS/WhiteDNS-Android/releases).
The server generates its own private key; the import link includes only its
public configuration. See the [Iran/network guide](SOAR-IRAN-GUIDE.fa.md).

Upgrade to the version pinned by the current bootstrap:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/WhiteDNS/Soar-Server/main/setup.sh) update
```

The updater preserves the domain and key and restores the previous service if
activation fails. Rollback and uninstall are available in the manager. The key
is kept by default on uninstall. An existing managed installation can be
opened by rerunning the normal installation command.

## Scope and verification

The pinned engine is Lantern Soar v0.2.2. The server listens on public UDP/53
and permits TCP to public destinations on ports 80/443. It does not provide
per-user accounts or quotas. IR discovery is intended for users in Iran;
GLOBAL is available elsewhere. Resolver reachability depends on the network.
Operation inside Iran has not yet been field-verified.

See [validation](AUDIT-REPORT.md), [tests](TESTING.md), and
[release downloads](https://github.com/WhiteDNS/Soar-Server/releases).
The download checksum detects corruption; obtain the script and release from
this official repository. You can inspect `setup.sh` before running it.

## Build from source

Requires Go 1.26.1, Python 3 and Bash:

```bash
bash tests/installer_test.sh
bash tests/bootstrap_test.sh
go test ./... -count=1 -timeout 60s
go vet ./...
python3 scripts/build_bundle.py
```

Only release assets contain precompiled binaries; users do not need Go on the VPS.
Installer source is Apache-2.0. Bundled dependencies retain their own notices
and licenses. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
