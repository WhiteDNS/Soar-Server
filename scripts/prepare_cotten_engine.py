"""Prepare and verify the exact patched Cotten source used by panel releases.

The upstream revision and complete patch digest are recorded in patches/. No
branch head or unpublished module version is needed to reproduce a build.
"""
from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]


def sha(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def prepare(source: Path, check: bool = False) -> dict:
    manifest = json.loads((ROOT / "patches/cotten-engine.json").read_text())
    patch = ROOT / "patches" / manifest["patch"]
    if sha(patch) != manifest["patch_sha256"]:
        raise RuntimeError("Cotten patch checksum mismatch")
    if not source.exists():
        if check:
            raise RuntimeError("Patched Cotten source is missing; run scripts/prepare_cotten_engine.py first")
        source.parent.mkdir(parents=True, exist_ok=True)
        subprocess.run(["git", "clone", "--no-checkout", "https://github.com/WhiteDNS/CottenDNS.git", str(source)], check=True)
        subprocess.run(["git", "-C", str(source), "checkout", "--detach", manifest["base_commit"]], check=True)

    if (source / ".git").exists():
        revision = subprocess.check_output(["git", "-C", str(source), "rev-parse", "HEAD"], text=True).strip()
        if revision != manifest["base_commit"]:
            raise RuntimeError("Cotten base revision differs from the release manifest")

    def matches() -> bool:
        expected = manifest["files_sha256"]
        if not all((source / name).is_file() and sha(source / name) == digest
                   for name, digest in expected.items()):
            return False
        # Git-ignored Go/assembler/native files can still enter a build. Check
        # the filesystem too, rather than trusting only git's tracked list.
        build_suffixes = {".go", ".s", ".S", ".syso", ".c", ".h", ".cc", ".cpp", ".m", ".f", ".f90", ".swig", ".swigcxx"}
        return not any(file.relative_to(source).as_posix() not in expected
                       for file in source.rglob("*") if file.is_file()
                       and ".git" not in file.relative_to(source).parts
                       and (file.suffix in build_suffixes or file.name in ("go.mod", "go.sum")))

    if not matches():
        if check:
            raise RuntimeError("Cotten source does not match the release patch manifest")
        # Check first so a conflicting local edit never leaves a partial patch.
        subprocess.run(["git", "-C", str(source), "apply", "--check", str(patch)], check=True)
        subprocess.run(["git", "-C", str(source), "apply", str(patch)], check=True)
        if not matches():
            raise RuntimeError("Patched Cotten source failed verification")
    return manifest


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, default=ROOT / ".engine-source/CottenDNS")
    parser.add_argument("--workspace", type=Path)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    source = args.source.resolve()
    manifest = prepare(source, args.check)
    if args.workspace:
        workspace = args.workspace.resolve()
        workspace.parent.mkdir(parents=True, exist_ok=True)
        # JSON quoting is also valid for Go quoted paths, including spaces.
        workspace.write_text("go 1.26.1\n\nuse (\n\t" + json.dumps(str(ROOT)) + "\n\t" + json.dumps(str(source)) + "\n)\n")
    print(json.dumps({"source": str(source), "base_commit": manifest["base_commit"], "patch_sha256": manifest["patch_sha256"], "verified": True}))


if __name__ == "__main__":
    main()
