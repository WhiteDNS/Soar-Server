"""Verify release assets without building, merging or starting workflows."""
from __future__ import annotations
import hashlib
import io
import json
from pathlib import PurePosixPath
import struct
import subprocess
import tarfile
import time
import urllib.request

MAX_ASSET = 96 << 20


def api(method, endpoint, body=None, missing=False):
    command = ["gh", "api", "--method", method, endpoint]
    if body is not None:
        command += ["--input", "-"]
    result = subprocess.run(command, input=None if body is None else json.dumps(body),
                            text=True, capture_output=True, timeout=60)
    if result.returncode:
        if missing and "HTTP 404" in result.stderr:
            return None
        raise RuntimeError(f"GitHub API {method} {endpoint}: {result.stderr.strip()}")
    return json.loads(result.stdout) if result.stdout.strip() else None


def resolve_tag(prefix, tag):
    ref = api("GET", f"{prefix}/git/ref/tags/{tag}", missing=True)
    if ref is None:
        return None
    obj = ref["object"]
    for _ in range(4):
        if obj["type"] == "commit":
            return obj["sha"]
        if obj["type"] != "tag":
            break
        obj = api("GET", f"{prefix}/git/tags/{obj['sha']}")["object"]
    raise RuntimeError("Release tag does not resolve to a commit")


def download(url, limit):
    for attempt in range(3):
        try:
            request = urllib.request.Request(url, headers={"User-Agent": "WhiteDNS-release-verification"})
            with urllib.request.urlopen(request, timeout=90) as response:
                data = response.read(limit + 1)
                if response.status != 200 or len(data) > limit:
                    raise RuntimeError("Public release download failed or exceeded its size bound")
                return data
        except (OSError, TimeoutError):
            if attempt == 2:
                raise
            time.sleep(5)


def verify_archive(raw, arch, version, merged):
    root = f"soar-panel-{version}-linux-{arch}/"
    files = {}
    total = 0
    with tarfile.open(fileobj=io.BytesIO(raw), mode="r:gz") as archive:
        for member in archive:
            path = PurePosixPath(member.name)
            if not member.name.startswith(root) or path.is_absolute() or ".." in path.parts or not member.isfile():
                raise RuntimeError("Unsafe path or non-regular file in public bundle")
            relative = member.name[len(root):]
            total += member.size
            if relative in files or member.size > MAX_ASSET or total > 256 << 20:
                raise RuntimeError("Duplicate or oversized bundle member")
            files[relative] = archive.extractfile(member).read()
    listed = set()
    for line in files["SHA256SUMS"].decode("ascii").splitlines():
        expected, relative = line.split("  ", 1)
        if relative in listed or relative not in files or hashlib.sha256(files[relative]).hexdigest() != expected:
            raise RuntimeError("Internal bundle checksum mismatch")
        listed.add(relative)
    if listed != set(files) - {"SHA256SUMS"}:
        raise RuntimeError("Incomplete internal bundle checksum manifest")
    source = files["SOURCE-VERSIONS.txt"].decode("ascii")
    if f"Panel source commit: {merged}\n" not in source or f"WhiteDNS Server Management: {version}\n" not in source:
        raise RuntimeError("Published bundle source/version differs from the merged PR")
    if files["ARCH"].decode("ascii").strip() != arch or f"PANEL_VERSION={version}\n".encode() not in files["panel-install.sh"]:
        raise RuntimeError("Published architecture or installer version mismatch")
    for name in ("soar-panel", "soar-panel-ops", "soar-check"):
        data = files[f"bin/linux-{arch}/{name}"]
        if len(data) < 20 or data[:6] != b"\x7fELF\x02\x01" or struct.unpack("<H", data[18:20])[0] != {"amd64": 62, "arm64": 183}[arch]:
            raise RuntimeError("Published executable architecture mismatch")
    return len(listed)


def verify_public_release(repo, tag, version, merged):
    # All metadata and assets in this phase are fetched without credentials.
    release = json.loads(download(f"https://api.github.com/repos/{repo}/releases/tags/{tag}", 1 << 20))
    if release["draft"] or release["prerelease"] or release["tag_name"] != tag:
        raise RuntimeError("Release is not a public stable release of the expected tag")
    assets = {asset["name"]: asset for asset in release["assets"]}
    expected_names = {f"soar-panel-{version}-linux-{arch}.tar.gz{suffix}" for arch in ("amd64", "arm64") for suffix in ("", ".sha256")}
    if set(assets) != expected_names:
        raise RuntimeError("Public release must contain both archives and both checksums")
    results = []
    for arch in ("amd64", "arm64"):
        name = f"soar-panel-{version}-linux-{arch}.tar.gz"
        base = f"https://github.com/{repo}/releases/download/{tag}/"
        for asset_name in (name, name + ".sha256"):
            asset = assets[asset_name]
            if asset["browser_download_url"] != base + asset_name or asset["state"] != "uploaded" or not 0 < asset["size"] <= MAX_ASSET:
                raise RuntimeError("Unexpected release asset URL, state or size")
        checksum = download(base + name + ".sha256", 16384).decode("ascii").strip().split()
        raw = download(base + name, MAX_ASSET)
        digest = hashlib.sha256(raw).hexdigest()
        if len(checksum) != 2 or checksum[1].lstrip("*") != name or checksum[0] != digest or len(raw) != assets[name]["size"]:
            raise RuntimeError("Public archive checksum/size mismatch")
        if assets[name].get("digest") not in (None, "sha256:" + digest):
            raise RuntimeError("GitHub asset digest differs from the downloaded bytes")
        count = verify_archive(raw, arch, version, merged)
        results.append({"asset": name, "sha256": digest, "bytes": len(raw), "verified_internal_files": count})
    return {"url": release["html_url"], "tag": tag, "commit": merged, "anonymous_downloads": True, "assets": results}
