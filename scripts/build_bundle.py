"""Build a local, offline-installable Ubuntu amd64 server bundle. No publication."""
from __future__ import annotations

import gzip
import hashlib
import io
import os
from pathlib import Path
import shutil
import subprocess
import tarfile

ROOT = Path(__file__).resolve().parents[1]
VERSION = "0.1.2"
NAME = f"soar-server-setup-{VERSION}-linux-amd64"


def run(*args: str, env: dict[str, str] | None = None) -> str:
    return subprocess.check_output(args, cwd=ROOT, env=env, text=True).strip()


def main() -> None:
    out = ROOT / "dist"
    bundle = out / NAME
    bundle.mkdir(parents=True, exist_ok=True)
    binaries = bundle / "bin" / "linux-amd64"
    binaries.mkdir(parents=True, exist_ok=True)
    env = os.environ.copy()
    env.update(GOOS="linux", GOARCH="amd64", CGO_ENABLED="0")
    for program, package in (
        ("soar-server", "github.com/getlantern/soar/cmd/soar-server"),
        ("soar-check", "./cmd/soar-check"),
    ):
        subprocess.run(
            ["go", "build", "-mod=readonly", "-trimpath", "-buildvcs=false", "-ldflags=-s -w", "-o", str(binaries / program), package],
            cwd=ROOT, env=env, check=True,
        )
    for name in ("install.sh", "SERVER-GUIDE.fa.md", "SOAR-IRAN-GUIDE.fa.md", "AUDIT-REPORT.md", "TESTING.md", "LICENSE", "NOTICE"):
        content = (ROOT / name).read_text(encoding="utf-8").replace("\r\n", "\n")
        (bundle / name).write_bytes(content.encode("utf-8"))
    licenses = bundle / "licenses"
    licenses.mkdir(exist_ok=True)
    for module, filename in (
        ("github.com/getlantern/soar", "Soar-APACHE-2.0.txt"),
        ("golang.org/x/crypto", "x-crypto-LICENSE.txt"),
        ("golang.org/x/sys", "x-sys-LICENSE.txt"),
    ):
        directory = Path(run("go", "list", "-m", "-f", "{{.Dir}}", module))
        shutil.copyfile(directory / "LICENSE", licenses / filename)
    soar_dir = Path(run("go", "list", "-m", "-f", "{{.Dir}}", "github.com/getlantern/soar"))
    # The embedded resolver lists retain upstream's third-party notice.
    shutil.copyfile(soar_dir / "data" / "THIRD_PARTY.md", licenses / "Soar-data-THIRD-PARTY.md")
    shutil.copyfile(Path(run("go", "env", "GOROOT")) / "LICENSE", licenses / "Go-LICENSE.txt")
    metadata = (
        f"Setup: {VERSION}\n"
        "Soar: v0.2.2\n"
        "Soar commit: f0d55048c7035786265e2b671a45c4faf5b97a92\n"
        f"Toolchain: {run('go', 'version')}\n"
        "Target: linux/amd64; CGO_ENABLED=0\n"
        "Module integrity: pinned go.mod + go.sum; Go checksum database verification\n"
        "Distribution: WhiteDNS/Soar-Server GitHub release; SHA-256 checksums\n\n"
        + run("go", "list", "-m", "all") + "\n"
    )
    (bundle / "SOURCE-VERSIONS.txt").write_bytes(metadata.encode("ascii"))
    files = ("install.sh", "bin/linux-amd64/soar-server", "bin/linux-amd64/soar-check", "SOURCE-VERSIONS.txt")
    checksums = "".join(f"{hashlib.sha256((bundle / name).read_bytes()).hexdigest()}  {name}\n" for name in files)
    (bundle / "SHA256SUMS").write_bytes(checksums.encode("ascii"))
    archive = out / f"{NAME}.tar.gz"
    with archive.open("wb") as raw, gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0) as compressed:
        with tarfile.open(fileobj=compressed, mode="w") as tar:
            for file in sorted(bundle.rglob("*")):
                if not file.is_file():
                    continue
                data = file.read_bytes()
                info = tarfile.TarInfo(f"{NAME}/{file.relative_to(bundle).as_posix()}")
                info.size, info.mtime, info.uid, info.gid = len(data), 0, 0, 0
                info.mode = 0o755 if file.name == "install.sh" or file.parent == binaries else 0o644
                tar.addfile(info, io.BytesIO(data))
    digest = hashlib.sha256(archive.read_bytes()).hexdigest()
    archive.with_suffix(archive.suffix + ".sha256").write_bytes(f"{digest}  {archive.name}\n".encode("ascii"))
    print(f"Built: {archive}\nSHA256: {digest}")


if __name__ == "__main__":
    main()
