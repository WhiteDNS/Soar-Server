"""Build the stable management panel separately from the standalone bundle."""
from __future__ import annotations
import argparse
import gzip
import hashlib
import io
import json
import os
import re
from pathlib import Path
import shutil
import subprocess
import sys
import struct
import tarfile

ROOT=Path(__file__).resolve().parents[1]
VERSION="0.14.0"

def verify_version():
    for path, pattern in [
        ("internal/panel/releases.go", r'var Version = "([^"]+)"'),
        ("panel-install.sh", r"^PANEL_VERSION=(\S+)$"),
    ]:
        match = re.search(pattern, (ROOT/path).read_text(encoding="utf8"), re.MULTILINE)
        if match is None or match[1] != VERSION:
            raise RuntimeError(f"Panel version in {path} must match bundle version {VERSION}")
    if os.environ.get("GITHUB_REF_TYPE") == "tag" and os.environ.get("GITHUB_REF_NAME") != f"panel-v{VERSION}":
        raise RuntimeError("Panel release tag must match the bundle version")

def verify_binary(path, arch):
    with path.open("rb") as executable:
        header = executable.read(20)
    if len(header) != 20 or header[:6] != b"\x7fELF\x02\x01" or struct.unpack("<H", header[18:20])[0] != {"amd64": 62, "arm64": 183}[arch]:
        raise RuntimeError(f"{path.name} is not a Linux {arch} executable")

def run(*args):
    return subprocess.check_output(args,cwd=ROOT,text=True).strip()

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output",type=Path,default=ROOT/"dist")
    parser.add_argument("--print-version",action="store_true")
    parser.add_argument("--arch",choices=("amd64", "arm64"),default="amd64")
    args=parser.parse_args()
    verify_version()
    if args.print_version:
        print(VERSION)
        return
    name=f"soar-panel-{VERSION}-linux-{args.arch}"
    bundle=args.output/name
    binary=bundle/f"bin/linux-{args.arch}"
    binary.mkdir(parents=True,exist_ok=True)
    env=dict(os.environ,GOOS="linux",GOARCH=args.arch,CGO_ENABLED="0")
    cotten=Path(run("go","list","-m","-f","{{.Dir}}","cottendns-go"))
    subprocess.run([sys.executable,str(ROOT/"scripts/prepare_cotten_engine.py"),"--source",str(cotten),"--check"],cwd=ROOT,check=True)
    master=Path(run("go","list","-m","-f","{{.Dir}}","masterdnsvpn-go"))
    subprocess.run([sys.executable,str(ROOT/"scripts/prepare_masterdns_engine.py"),"--source",str(master),"--check"],cwd=ROOT,check=True)
    for name,package in [("soar-panel","./cmd/soar-panel"),("soar-panel-ops","./cmd/soar-panel-ops"),("soar-check","./cmd/soar-check")]:
        subprocess.run(["go","build","-mod=readonly","-trimpath","-buildvcs=false","-ldflags=-s -w","-o",str(binary/name),package],cwd=ROOT,env=env,check=True)
        verify_binary(binary/name, args.arch)
    (bundle/"ARCH").write_text(args.arch+"\n", encoding="ascii")
    for name in ["panel-install.sh","panel-security.sh","panel-menu.sh","LICENSE","NOTICE","docs/EGRESS.fa.md","docs/EGRESS.en.md","docs/SERVER-MENU.fa.md","docs/SERVER-MENU.en.md","docs/PANEL.fa.md","docs/PANEL.en.md","docs/LOCAL-COTTEN.fa.md","docs/DOMAINS.fa.md","docs/SUBSCRIPTIONS.md","docs/TELEGRAM.md","docs/MASTERDNS.md","docs/PANEL-DESIGN.md","docs/PANEL-TUNING-2026-10-17.fa.md","docs/SOAR-WEB-RELIABILITY-2026-10-18.fa.md"]:
        target=bundle/name;target.parent.mkdir(parents=True,exist_ok=True)
        target.write_bytes((ROOT/name).read_text(encoding="utf8").replace("\r\n","\n").encode("utf8"))
    (bundle/"lib").mkdir(exist_ok=True)
    (bundle/"lib/panel-installer.sh").write_bytes((ROOT/"lib/panel-installer.sh").read_text(encoding="utf8").replace("\r\n","\n").encode("utf8"))
    (bundle/"lib/standalone-install.sh").write_bytes((ROOT/"install.sh").read_text(encoding="utf8").replace("\r\n","\n").encode("utf8"))
    for name in ["ANTIFLOOD.fa.md", "COTTENMAX-PANEL-VALIDATION.md", "PANEL-0.5.0-VALIDATION.md", f"RELEASE-panel-{VERSION}.md", "COTTEN-TCP-RELIABILITY-2026-10-21.md", "telegram-emergency-management.fa.md", "telegram-management-audit.md", "DIAGNOSTICS-ASSISTANT.fa.md"]:
        (bundle/"docs"/name).write_bytes((ROOT/"docs"/name).read_text(encoding="utf8").replace("\r\n","\n").encode("utf8"))
    shutil.copytree(ROOT/"patches",bundle/"patches",dirs_exist_ok=True)
    licenses=bundle/"licenses";licenses.mkdir(exist_ok=True)
    shutil.copyfile(ROOT/"internal/panel/web/fonts/OFL.txt",licenses/"Vazirmatn-OFL.txt")
    for mod,label in [("masterdnsvpn-go","MasterDNS"),("cottendns-go","CottenDNS"),("github.com/quic-go/quic-go","quic-go"),("github.com/quic-go/qpack","qpack"),("github.com/BurntSushi/toml","BurntSushi-toml"),("github.com/klauspost/compress","compress"),("github.com/klauspost/cpuid/v2","cpuid"),("github.com/klauspost/reedsolomon","reedsolomon"),("github.com/pierrec/lz4/v4","lz4"),("github.com/getlantern/soar","Soar"),("go.etcd.io/bbolt","bbolt"),("github.com/skip2/go-qrcode","go-qrcode"),("golang.org/x/crypto","x-crypto"),("golang.org/x/sys","x-sys"),("golang.org/x/net","x-net"),("golang.org/x/text","x-text")]:
        module=Path(run("go","list","-m","-f","{{.Dir}}",mod))
        original=next((module/name for name in ("LICENSE","COPYING","LICENSE.txt","LICENSE.md") if (module/name).is_file()),None)
        if original is None:raise RuntimeError(f"Missing original license for {mod}")
        shutil.copyfile(original,licenses/(label+"-LICENSE.txt"))
    soar=Path(run("go","list","-m","-f","{{.Dir}}","github.com/getlantern/soar"))
    shutil.copyfile(soar/"data/THIRD_PARTY.md",licenses/"Soar-data-THIRD-PARTY.md")
    shutil.copyfile(Path(run("go","env","GOROOT"))/"LICENSE",licenses/"Go-LICENSE.txt")
    cotten=Path(run("go","list","-m","-f","{{.Dir}}","cottendns-go"))
    for source, label in [("internal/cottenmax/engine/LICENSE", "CottenMax-Soar-LICENSE.txt"), ("internal/cottenmax/engine/NOTICE.cottendns.txt", "CottenMax-NOTICE.txt"), ("internal/cottenmax/engine/data/THIRD_PARTY.md", "CottenMax-data-THIRD-PARTY.md")]:
        shutil.copyfile(cotten/source,licenses/label)
    cotten_revision=run("git","-C",str(cotten),"rev-parse","HEAD") if (cotten/".git").exists() else run("go","list","-m","-f","{{.Replace.Version}}","cottendns-go")
    patch_manifest=json.loads((ROOT/"patches/cotten-engine.json").read_text())
    (bundle/"SOURCE-VERSIONS.txt").write_text(f"WhiteDNS Server Management: {VERSION}\nPanel source commit: {run('git','rev-parse','HEAD')}\nLantern Soar: v0.2.3\nCottenDNS source revision: {cotten_revision}\nCottenDNS patch SHA256: {patch_manifest['patch_sha256']}\nMasterDNS source revision: {json.loads((ROOT/'patches/masterdns-engine.json').read_text())['base_commit']}\nMasterDNS patch SHA256: {json.loads((ROOT/'patches/masterdns-engine.json').read_text())['patch_sha256']}\nToolchain: {run('go','version')}\nTarget: linux/{args.arch}; CGO_ENABLED=0\n\n{run('go','list','-m','all')}\n",encoding="ascii")
    sums="".join(f"{hashlib.sha256(f.read_bytes()).hexdigest()}  {f.relative_to(bundle).as_posix()}\n" for f in sorted(bundle.rglob("*")) if f.is_file() and f.name!="SHA256SUMS")
    (bundle/"SHA256SUMS").write_bytes(sums.encode("ascii"))
    archive=args.output/(bundle.name+".tar.gz")
    with archive.open("wb") as raw,gzip.GzipFile(filename="",mode="wb",fileobj=raw,mtime=0) as zipped,tarfile.open(fileobj=zipped,mode="w") as tar:
        for f in sorted(bundle.rglob("*")):
            if not f.is_file():continue
            content=f.read_bytes();info=tarfile.TarInfo(bundle.name+"/"+f.relative_to(bundle).as_posix());info.size=len(content);info.mtime=0;info.mode=0o755 if f.parent==binary or f.name.endswith(".sh") else 0o644
            tar.addfile(info,io.BytesIO(content))
    digest=hashlib.sha256(archive.read_bytes()).hexdigest()
    archive.with_suffix(".gz.sha256").write_bytes(f"{digest}  {archive.name}\n".encode("ascii"))
    print(f"Built: {archive}\nSHA256: {digest}")

if __name__=="__main__":main()
