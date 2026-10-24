"""Run the packaged Linux binaries on their native architecture; no services changed."""
import argparse
import base64
import http.cookiejar
import json
from pathlib import Path
import platform
import re
import secrets
import subprocess
import tempfile
import time
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bundle", type=Path, required=True)
    parser.add_argument("--version", required=True)
    args = parser.parse_args()
    bundle = args.bundle.resolve()
    arch = (bundle / "ARCH").read_text().strip()
    native = {"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(platform.machine())
    assert platform.system() == "Linux" and arch == native, "use the matching native Linux runner"
    binaries = bundle / "bin" / ("linux-" + arch)
    for name in ("soar-panel", "soar-panel-ops", "soar-check"):
        command = "-version" if name == "soar-check" else "version"
        result = subprocess.check_output([str(binaries / name), command], text=True, timeout=10).strip()
        assert result and (name == "soar-check" or result == args.version), (name, result)

    with tempfile.TemporaryDirectory(prefix="panel-bundle-") as directory:
        work = Path(directory)
        password = secrets.token_urlsafe(24)
        password_file, db = work / "password", work / "panel.db"
        password_file.write_text(password)
        password_file.chmod(0o600)
        panel = str(binaries / "soar-panel")
        subprocess.run([panel, "init", "--db", str(db), "--zone", "soar.example.org", "--password-file", str(password_file)], check=True, timeout=15, capture_output=True)
        password_file.unlink()
        with (work / "server.log").open("w") as log:
            server = subprocess.Popen([panel, "run", "--db", str(db), "--listen", "127.0.0.1:0", "--dns-listen", "127.0.0.1:0"], stdout=log, stderr=log)
            try:
                deadline = time.monotonic() + 15
                while True:
                    output = (work / "server.log").read_text()
                    match = re.search(r"management=(127\.0\.0\.1:\d+)", output)
                    if match:
                        break
                    assert server.poll() is None and time.monotonic() < deadline, output
                    time.sleep(0.05)
                base = "http://" + match[1]
                client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
                csrf = ""

                def request(path, body=None):
                    raw = None if body is None else json.dumps(body).encode()
                    req = urllib.request.Request(base + path, data=raw, headers={"Content-Type": "application/json", "X-CSRF-Token": csrf})
                    with client.open(req, timeout=10) as response:
                        return response.read()

                csrf = json.loads(request("/api/login", {"name": "admin", "password": password}))["csrf"]
                assert json.loads(request("/api/dashboard"))["version"] == args.version
                account = json.loads(request("/api/clients", {"name": "Native bundle smoke", "engines": "both", "enabled": True}))
                subscription = json.loads(request("/api/clients/" + account["id"] + "/subscription"))["url"]
                with client.open(subscription, timeout=10) as response:
                    profiles = base64.b64decode(response.read()).decode()
                assert "soar://" in profiles and "cottendns://" in profiles, "both engine profiles must be exported"
            finally:
                server.terminate()
                try:
                    server.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    server.kill()
                    server.wait(timeout=5)
                    raise
    print(f"PASS: native linux/{arch} bundle binaries, initialization, login, account creation and both profiles")


if __name__ == "__main__":
    main()
