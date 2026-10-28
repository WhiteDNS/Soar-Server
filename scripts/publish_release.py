"""Publish same-run verified artifacts; never dispatch another workflow."""
from __future__ import annotations
import argparse
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import re
import struct
import subprocess
import tarfile

from release_assets import api, download, resolve_tag, verify_archive, verify_public_release

ROOT = Path(__file__).resolve().parents[1]
REPO = "WhiteDNS/Soar-Server"


def configuration(target):
    if target == "panel":
        import build_panel_bundle as bundle
        bundle.verify_version()
        version = bundle.VERSION
        return version, "panel-v" + version, f"WhiteDNS Server Management {version}", ROOT / f"docs/RELEASE-panel-{version}.md"
    if target == "standalone":
        import build_bundle as bundle
        version = bundle.VERSION
        match = re.search(r"^SETUP_RELEASE=(\S+)$", (ROOT / "setup.sh").read_text(), re.M)
        if match is None or match[1] != version:
            raise RuntimeError("Standalone installer and bundle versions disagree")
        return version, "v" + version, f"WhiteDNS Soar Server {version}", ROOT / "RELEASE-NOTES.md"
    raise RuntimeError("Choose panel or standalone")


def validate_context():
    if os.environ.get("GITHUB_REPOSITORY") != REPO or os.environ.get("GITHUB_REF") != "refs/heads/main":
        raise RuntimeError("Run this release workflow from WhiteDNS/Soar-Server main")
    head = os.environ.get("GITHUB_SHA", "")
    actual = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
    if not re.fullmatch(r"[0-9a-f]{40}", head) or actual != head:
        raise RuntimeError("Checkout differs from the immutable workflow source")
    return head


def checksum_file(archive):
    manifest = archive.with_suffix(".gz.sha256")
    parts = manifest.read_text(encoding="ascii").split()
    if len(parts) != 2 or parts[1].lstrip("*") != archive.name:
        raise RuntimeError("Invalid archive checksum filename")
    raw = archive.read_bytes()
    if len(raw) > 96 << 20 or hashlib.sha256(raw).hexdigest() != parts[0]:
        raise RuntimeError("Archive checksum mismatch")
    return raw, manifest


def verify_standalone(raw, version, head):
    prefix = f"soar-server-setup-{version}-linux-amd64/"
    files, total = {}, 0
    with tarfile.open(fileobj=io.BytesIO(raw), mode="r:gz") as archive:
        for member in archive:
            path = PurePosixPath(member.name)
            total += member.size
            relative = member.name[len(prefix):]
            if not member.name.startswith(prefix) or path.is_absolute() or ".." in path.parts or not member.isfile() or relative in files or member.size > 96 << 20 or total > 256 << 20:
                raise RuntimeError("Unsafe standalone archive")
            files[relative] = archive.extractfile(member).read()
    source = files["SOURCE-VERSIONS.txt"].decode("ascii")
    if f"Setup: {version}\n" not in source or f"Source commit: {head}\n" not in source:
        raise RuntimeError("Standalone source/version mismatch")
    for name in ("soar-server", "soar-check"):
        elf = files[f"bin/linux-amd64/{name}"]
        if len(elf) < 20 or elf[:6] != b"\x7fELF\x02\x01" or struct.unpack("<H", elf[18:20])[0] != 62:
            raise RuntimeError("Standalone executable architecture mismatch")
    listed = set()
    for line in files["SHA256SUMS"].decode("ascii").splitlines():
        digest, name = line.split("  ", 1)
        if name in listed or name not in files or hashlib.sha256(files[name]).hexdigest() != digest:
            raise RuntimeError("Standalone internal checksum mismatch")
        listed.add(name)
    if not {"install.sh", "SOURCE-VERSIONS.txt", "bin/linux-amd64/soar-server", "bin/linux-amd64/soar-check"} <= listed:
        raise RuntimeError("Standalone checksum manifest is incomplete")


def collect_assets(directory, target, version, head):
    names = [f"soar-panel-{version}-linux-{arch}.tar.gz" for arch in ("amd64", "arm64")] if target == "panel" else [f"soar-server-setup-{version}-linux-amd64.tar.gz"]
    assets = []
    for index, name in enumerate(names):
        matches = list(directory.rglob(name))
        if len(matches) != 1:
            raise RuntimeError("Expected exactly one artifact: " + name)
        archive = matches[0]
        raw, manifest = checksum_file(archive)
        if target == "panel":
            verify_archive(raw, ("amd64", "arm64")[index], version, head)
        else:
            verify_standalone(raw, version, head)
        assets += [archive, manifest]
    return assets


def public_report(target, tag, version, head, assets):
    if target == "panel":
        report = verify_public_release(REPO, tag, version, head)
        expected = {path.name: hashlib.sha256(path.read_bytes()).hexdigest() for path in assets[::2]}
        if any(expected[asset["asset"]] != asset["sha256"] for asset in report["assets"]):
            raise RuntimeError("Public assets differ from this run's verified files")
        return report
    for path in assets:
        public = download(f"https://github.com/{REPO}/releases/download/{tag}/{path.name}", 96 << 20)
        if hashlib.sha256(public).digest() != hashlib.sha256(path.read_bytes()).digest():
            raise RuntimeError("Public standalone asset differs from this run's verified file")
    return {"tag": tag, "commit": head, "anonymous_downloads": True, "assets": [path.name for path in assets]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--plan", action="store_true")
    parser.add_argument("--artifacts", type=Path)
    args = parser.parse_args()
    head = validate_context()
    target = os.environ.get("RELEASE_TARGET", "")
    version, tag, title, notes = configuration(target)
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", version) or not notes.is_file():
        raise RuntimeError("Stable version and release notes required")
    prefix = "repos/" + REPO
    existing = api("GET", f"{prefix}/releases/tags/{tag}", missing=True)
    if args.plan:
        if existing and (existing["draft"] or existing["prerelease"]):
            raise RuntimeError("An unfinished release exists. Resume its failed release job instead of rebuilding")
        current = resolve_tag(prefix, tag)
        if existing is None and current is not None and current != head:
            raise RuntimeError("Unpublished tag points elsewhere; stopped before allocating build runners")
        matrix = {"include": [{"os": "ubuntu-24.04", "arch": "amd64"}]}
        if target == "panel":
            matrix["include"].append({"os": "ubuntu-24.04-arm", "arch": "arm64"})
        with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf8") as stream:
            stream.write(f"build={str(existing is None).lower()}\nversion={version}\nmatrix={json.dumps(matrix)}\n")
        print(f"{tag}: already published; no build needed" if existing else f"{tag}: verify, build and publish in this run")
        return
    if args.artifacts is None:
        raise RuntimeError("Same-run artifacts directory required")
    assets = collect_assets(args.artifacts, target, version, head)
    current = resolve_tag(prefix, tag)
    if current is not None and current != head:
        raise RuntimeError("Existing tag points elsewhere; it will not be moved")
    if existing is not None and current is None:
        raise RuntimeError("Existing release tag is missing; it will not be recreated implicitly")
    if existing and existing["prerelease"]:
        raise RuntimeError("Stable publication cannot overwrite a prerelease")
    if current is None:
        api("POST", prefix + "/git/refs", {"ref": "refs/tags/" + tag, "sha": head})
    latest = "true" if target == "panel" else "false"
    if existing and not existing["draft"]:
        # Publication may have completed before a network failure interrupted
        # anonymous verification. Verify it again without changing public bytes.
        pass
    elif existing:
        subprocess.run(["gh", "release", "upload", tag, *map(str, assets), "--repo", REPO, "--clobber"], check=True)
        subprocess.run(["gh", "release", "edit", tag, "--repo", REPO, "--draft=false", "--latest=" + latest], check=True)
    else:
        subprocess.run(["gh", "release", "create", tag, *map(str, assets), "--repo", REPO, "--verify-tag", "--latest=" + latest, "--title", title, "--notes-file", str(notes)], check=True)
    report = public_report(target, tag, version, head, assets)
    Path("release-verification.json").write_text(json.dumps(report, indent=2) + "\n", encoding="utf8")
    with open(os.environ["GITHUB_STEP_SUMMARY"], "a", encoding="utf8") as stream:
        stream.write(f"Published [{tag}](https://github.com/{REPO}/releases/tag/{tag}) from the verified artifacts of this run.\n")


if __name__ == "__main__":
    main()
