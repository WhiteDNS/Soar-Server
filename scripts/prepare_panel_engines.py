"""Verify both pinned engine patchsets and write one reproducible Go workspace."""
from __future__ import annotations

import argparse
import json
from pathlib import Path

import prepare_cotten_engine
import prepare_masterdns_engine

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--workspace", type=Path, default=ROOT / ".engine-source/patched.go.work")
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    sources = [ROOT, ROOT / ".engine-source/CottenDNS", ROOT / ".engine-source/MasterDnsVPN"]
    cotten = prepare_cotten_engine.prepare(sources[1], args.check)
    master = prepare_masterdns_engine.prepare(sources[2], args.check)
    if not args.check:
        workspace = args.workspace.resolve()
        workspace.parent.mkdir(parents=True, exist_ok=True)
        workspace.write_text("go 1.26.1\n\nuse (\n" + "".join("\t" + json.dumps(str(p)) + "\n" for p in sources) + ")\n")
    print(json.dumps({"verified": True, "cotten": cotten["base_commit"], "masterdns": master["base_commit"]}))


if __name__ == "__main__":
    main()
