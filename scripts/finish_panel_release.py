"""Finish an explicitly dispatched, immutable same-repository panel release."""
from __future__ import annotations

import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import re
import struct
import subprocess
import tarfile
import time
import urllib.request

REQUIRED = {
    ".github/workflows/panel.yml": {"verify (ubuntu-24.04, amd64)", "verify (ubuntu-24.04-arm, arm64)"},
    ".github/workflows/release.yml": {"verify"},
}
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


def validate_pr(pr, repo, head, base):
    if pr["head"]["sha"] != head:
        raise RuntimeError("PR head changed after dispatch; refusing a different patch")
    if pr["head"]["repo"]["full_name"] != repo or pr["base"]["repo"]["full_name"] != repo:
        raise RuntimeError("Only same-repository PRs are supported")
    if pr["base"]["ref"] != "main" or pr.get("draft"):
        raise RuntimeError("Release requires a ready PR targeting main")
    if not pr.get("merged"):
        if pr["state"] != "open" or pr["base"]["sha"] != base:
            raise RuntimeError("PR closed or main changed since authorization")


def checks_ready(runs, jobs):
    latest = {}
    for run in runs:
        path = run.get("path", "").split("@", 1)[0]
        if path in REQUIRED and (path not in latest or run["id"] > latest[path]["id"]):
            latest[path] = run
    if set(latest) != set(REQUIRED):
        return False
    ready = True
    for path, run in latest.items():
        if run["status"] != "completed":
            ready = False
            continue
        if run["conclusion"] != "success":
            raise RuntimeError(f"Required PR workflow did not pass: {path}: {run['conclusion']}")
        found = {job["name"]: job for job in jobs.get(run["id"], [])}
        for name in REQUIRED[path]:
            if name not in found or found[name]["status"] != "completed":
                ready = False
            elif found[name]["conclusion"] != "success":
                raise RuntimeError(f"Required PR job did not pass: {name}")
    return ready


def wait_for_ci(prefix, number, repo, head, base):
    deadline = time.monotonic() + 55 * 60
    while time.monotonic() < deadline:
        pr = api("GET", f"{prefix}/pulls/{number}")
        validate_pr(pr, repo, head, base)
        if pr.get("merged"):
            return pr
        runs = api("GET", f"{prefix}/actions/runs?event=pull_request&head_sha={head}&per_page=100")["workflow_runs"]
        runs = [run for run in runs if not run.get("pull_requests") or
                any(p["number"] == number for p in run["pull_requests"])]
        jobs = {run["id"]: api("GET", f"{prefix}/actions/runs/{run['id']}/jobs?per_page=100")["jobs"]
                for run in runs if run.get("path", "").split("@", 1)[0] in REQUIRED and run["status"] == "completed"}
        if checks_ready(runs, jobs):
            print("All required PR workflows and both panel architectures passed.", flush=True)
            return pr
        print("Waiting for required PR CI on the authorized commit...", flush=True)
        time.sleep(20)
    raise TimeoutError("PR CI did not complete in 55 minutes; no merge was attempted")


def merge_pr(prefix, number, pr, head, base):
    if pr.get("merged"):
        return pr["merge_commit_sha"]
    main = api("GET", f"{prefix}/git/ref/heads/main")["object"]["sha"]
    if main != base:
        raise RuntimeError("main changed after PR CI; revalidation is required")
    reviews = api("GET", f"{prefix}/pulls/{number}/reviews?per_page=100")
    decisions = {}
    for review in reviews:
        if review["state"] != "COMMENTED":
            decisions[review["user"]["login"]] = review["state"]
    if "CHANGES_REQUESTED" in decisions.values():
        raise RuntimeError("A reviewer requested changes; automatic merge stopped")
    result = api("PUT", f"{prefix}/pulls/{number}/merge", {"sha": head, "merge_method": "squash"})
    if not result.get("merged"):
        raise RuntimeError("GitHub did not merge the authorized PR")
    return result["sha"]


def verify_merge_tree(prefix, head, merged):
    original = api("GET", f"{prefix}/git/commits/{head}")["tree"]["sha"]
    actual = api("GET", f"{prefix}/git/commits/{merged}")["tree"]["sha"]
    if original != actual:
        raise RuntimeError("Merged tree differs from the tested PR; release stopped before tagging")


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


def ensure_tag(prefix, tag, merged):
    current = resolve_tag(prefix, tag)
    if current is None:
        api("POST", f"{prefix}/git/refs", {"ref": f"refs/tags/{tag}", "sha": merged})
    elif current != merged:
        raise RuntimeError("Release tag already points elsewhere; it will not be replaced")


def tag_runs(prefix, tag, merged):
    runs = api("GET", f"{prefix}/actions/workflows/panel.yml/runs?event=workflow_dispatch&head_sha={merged}&per_page=100")["workflow_runs"]
    return [run for run in runs if run["head_branch"] == tag]


def publish_tag(prefix, tag, merged):
    runs = tag_runs(prefix, tag, merged)
    if not runs:
        # GITHUB_TOKEN ref writes do not trigger push workflows. Explicit dispatch
        # does, and the tag ref makes the existing verify/release jobs run normally.
        api("POST", f"{prefix}/actions/workflows/panel.yml/dispatches", {"ref": tag})
    deadline = time.monotonic() + 55 * 60
    while time.monotonic() < deadline:
        runs = tag_runs(prefix, tag, merged)
        if runs:
            run = max(runs, key=lambda value: value["id"])
            if run["status"] == "completed":
                if run["conclusion"] != "success":
                    raise RuntimeError(f"Tag verification/publication failed: {run['html_url']}")
                return run["html_url"]
        print(f"Waiting for tag verification and publication: {tag}", flush=True)
        time.sleep(20)
    raise TimeoutError("Tag workflow did not complete in 55 minutes")


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


def main():
    repo = os.environ["GITHUB_REPOSITORY"]
    head = os.environ["GITHUB_SHA"]
    base = os.environ["RELEASE_BASE_SHA"]
    number = os.environ["RELEASE_PR"]
    if repo != "WhiteDNS/Soar-Server" or not re.fullmatch(r"[0-9a-f]{40}", head) or not re.fullmatch(r"[0-9a-f]{40}", base) or not re.fullmatch(r"[1-9][0-9]*", number):
        raise RuntimeError("Invalid release repository, immutable commits or PR number")
    version = subprocess.check_output(["python3", "scripts/build_panel_bundle.py", "--print-version"], text=True).strip()
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", version):
        raise RuntimeError("Automatic publication requires a stable version")
    tag = "panel-v" + version
    prefix = f"repos/{repo}"
    pr = api("GET", f"{prefix}/pulls/{number}")
    validate_pr(pr, repo, head, base)
    if not pr.get("merged") and resolve_tag(prefix, tag) is not None:
        raise RuntimeError("Version already has a tag; refusing to merge another release of it")
    pr = wait_for_ci(prefix, int(number), repo, head, base)
    merged = merge_pr(prefix, number, pr, head, base)
    verify_merge_tree(prefix, head, merged)
    print(f"PR #{number} merged; verified tree: {merged}", flush=True)
    ensure_tag(prefix, tag, merged)
    workflow = publish_tag(prefix, tag, merged)
    report = verify_public_release(repo, tag, version, merged)
    report.update(pr=int(number), workflow=workflow)
    Path("release-verification.json").write_text(json.dumps(report, indent=2) + "\n", encoding="utf8")
    summary = f"## Panel {version} published and independently verified\n\nPR #{number} → `{merged}` → [{tag}]({report['url']})\n\n"
    for asset in report["assets"]:
        summary += f"- `{asset['asset']}`: SHA-256 `{asset['sha256']}`; {asset['verified_internal_files']} internal files verified.\n"
    with open(os.environ["GITHUB_STEP_SUMMARY"], "a", encoding="utf8") as stream:
        stream.write(summary)
    print(summary, flush=True)


if __name__ == "__main__":
    main()
