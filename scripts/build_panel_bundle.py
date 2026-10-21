"""Build the stable management panel separately from the standalone bundle."""
from __future__ import annotations
import argparse
import gzip
import hashlib
import io
import os
from pathlib import Path
import shutil
import subprocess
import tarfile

ROOT=Path(__file__).resolve().parents[1]
VERSION="0.5.0"
NAME=f"soar-panel-{VERSION}-linux-amd64"

def run(*args):
    return subprocess.check_output(args,cwd=ROOT,text=True).strip()

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output",type=Path,default=ROOT/"dist")
    args=parser.parse_args()
    bundle=args.output/NAME
    binary=bundle/"bin/linux-amd64"
    binary.mkdir(parents=True,exist_ok=True)
    env=dict(os.environ,GOOS="linux",GOARCH="amd64",CGO_ENABLED="0")
    for name,package in [("soar-panel","./cmd/soar-panel"),("soar-panel-ops","./cmd/soar-panel-ops"),("soar-check","./cmd/soar-check")]:
        subprocess.run(["go","build","-mod=readonly","-trimpath","-buildvcs=false","-ldflags=-s -w","-o",str(binary/name),package],cwd=ROOT,env=env,check=True)
    for name in ["panel-install.sh","panel-security.sh","LICENSE","NOTICE","docs/PANEL.fa.md","docs/PANEL.en.md","docs/LOCAL-COTTEN.fa.md","docs/DOMAINS.fa.md","docs/SUBSCRIPTIONS.md","docs/PANEL-DESIGN.md","docs/PANEL-TUNING-2026-10-17.fa.md","docs/SOAR-WEB-RELIABILITY-2026-10-18.fa.md"]:
        target=bundle/name;target.parent.mkdir(parents=True,exist_ok=True)
        target.write_bytes((ROOT/name).read_text(encoding="utf8").replace("\r\n","\n").encode("utf8"))
    (bundle/"lib").mkdir(exist_ok=True)
    (bundle/"lib/panel-installer.sh").write_bytes((ROOT/"lib/panel-installer.sh").read_text(encoding="utf8").replace("\r\n","\n").encode("utf8"))
    (bundle/"lib/standalone-install.sh").write_bytes((ROOT/"install.sh").read_text(encoding="utf8").replace("\r\n","\n").encode("utf8"))
    for name in ["PANEL-0.5.0-VALIDATION.md", "RELEASE-panel-0.5.0.md"]:
        (bundle/"docs"/name).write_bytes((ROOT/"docs"/name).read_text(encoding="utf8").replace("\r\n","\n").encode("utf8"))
    licenses=bundle/"licenses";licenses.mkdir(exist_ok=True)
    shutil.copyfile(ROOT/"internal/panel/web/fonts/OFL.txt",licenses/"Vazirmatn-OFL.txt")
    for mod,label in [("cottendns-go","CottenDNS"),("github.com/BurntSushi/toml","BurntSushi-toml"),("github.com/klauspost/compress","compress"),("github.com/klauspost/cpuid/v2","cpuid"),("github.com/klauspost/reedsolomon","reedsolomon"),("github.com/pierrec/lz4/v4","lz4"),("github.com/getlantern/soar","Soar"),("go.etcd.io/bbolt","bbolt"),("github.com/skip2/go-qrcode","go-qrcode"),("golang.org/x/crypto","x-crypto"),("golang.org/x/sys","x-sys"),("golang.org/x/net","x-net"),("golang.org/x/text","x-text")]:
        module=Path(run("go","list","-m","-f","{{.Dir}}",mod))
        original=next((module/name for name in ("LICENSE","COPYING","LICENSE.txt") if (module/name).is_file()),None)
        if original is None:raise RuntimeError(f"Missing original license for {mod}")
        shutil.copyfile(original,licenses/(label+"-LICENSE.txt"))
    soar=Path(run("go","list","-m","-f","{{.Dir}}","github.com/getlantern/soar"))
    shutil.copyfile(soar/"data/THIRD_PARTY.md",licenses/"Soar-data-THIRD-PARTY.md")
    shutil.copyfile(Path(run("go","env","GOROOT"))/"LICENSE",licenses/"Go-LICENSE.txt")
    cotten=Path(run("go","list","-m","-f","{{.Dir}}","cottendns-go"))
    cotten_revision=run("git","-C",str(cotten),"rev-parse","HEAD") if (cotten/".git").exists() else run("go","list","-m","-f","{{.Replace.Version}}","cottendns-go")
    (bundle/"SOURCE-VERSIONS.txt").write_text(f"WhiteDNS Soar Panel: {VERSION}\nPanel source commit: {run('git','rev-parse','HEAD')}\nLantern Soar: v0.2.3\nCottenDNS source revision: {cotten_revision}\nToolchain: {run('go','version')}\nTarget: linux/amd64; CGO_ENABLED=0\n\n{run('go','list','-m','all')}\n",encoding="ascii")
    sums="".join(f"{hashlib.sha256(f.read_bytes()).hexdigest()}  {f.relative_to(bundle).as_posix()}\n" for f in sorted(bundle.rglob("*")) if f.is_file() and f.name!="SHA256SUMS")
    (bundle/"SHA256SUMS").write_bytes(sums.encode("ascii"))
    archive=args.output/(NAME+".tar.gz")
    with archive.open("wb") as raw,gzip.GzipFile(filename="",mode="wb",fileobj=raw,mtime=0) as zipped,tarfile.open(fileobj=zipped,mode="w") as tar:
        for f in sorted(bundle.rglob("*")):
            if not f.is_file():continue
            content=f.read_bytes();info=tarfile.TarInfo(NAME+"/"+f.relative_to(bundle).as_posix());info.size=len(content);info.mtime=0;info.mode=0o755 if f.parent==binary or f.name.endswith(".sh") else 0o644
            tar.addfile(info,io.BytesIO(content))
    digest=hashlib.sha256(archive.read_bytes()).hexdigest()
    archive.with_suffix(".gz.sha256").write_bytes(f"{digest}  {archive.name}\n".encode("ascii"))
    print(f"Built: {archive}\nSHA256: {digest}")

if __name__=="__main__":main()
