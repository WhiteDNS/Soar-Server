"""Opt-in, sequential A/B comparison using one isolated panel account.

Each configuration is tested twice in reverse order to expose run-to-run
variation. Credentials stay in the private output directory, never in reports.
No settings are written to the panel or to user accounts.
"""
import argparse
import base64
import copy
import json
from pathlib import Path
import statistics
import subprocess
import sys
import time

PRESETS = {
    "stability": dict(maxQName=101, maxAnswer=512, maxInflight=32, maxPathWindow=8),
    "balanced": dict(maxQName=180, maxAnswer=896, maxInflight=64, maxPathWindow=16),
    "performance": dict(maxQName=253, maxAnswer=1232, maxInflight=128, maxPathWindow=32),
}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", type=Path, required=True)
    parser.add_argument("--engine", type=Path, required=True)
    parser.add_argument("--expected-ip", required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    record = json.loads(args.profile.read_text(encoding="utf8"))
    encoded = record["link"].removeprefix("soar://")
    original = json.loads(base64.urlsafe_b64decode(encoded + "=" * (-len(encoded) % 4)))
    args.output.mkdir(parents=True, exist_ok=True)
    summary = []
    order = list(PRESETS) + list(reversed(PRESETS))
    for index, name in enumerate(order):
        profile = copy.deepcopy(original)
        settings = profile["profile"]["soarSettings"]
        settings.update(PRESETS[name])
        settings.update(maxStreams=128, discovery=True, discoveryWant=1,
                        discoveryConcurrency=1, discoveryPaceMs=1500)
        link = "soar://" + base64.urlsafe_b64encode(json.dumps(profile).encode()).decode().rstrip("=")
        folder = args.output / f"{index + 1}-{name}"
        folder.mkdir(exist_ok=True)
        exported = folder / "profile.json"
        exported.write_text(json.dumps({"link": link}), encoding="utf8")
        started = time.monotonic()
        with (folder / "probe.log").open("w", encoding="utf8") as log:
            result = subprocess.run([
                sys.executable, str(Path(__file__).with_name("live_web_probe.py")),
                "--profile", str(exported), "--engine", str(args.engine.resolve()),
                "--expected-ip", args.expected_ip, "--output", str(folder),
            ], stdout=log, stderr=subprocess.STDOUT)
        payload = json.loads((folder / "result.json").read_text()) if (folder / "result.json").exists() else {}
        requests = payload.get("results", [])
        successes = [r for r in requests if r["ok"]]
        large = [r for r in successes if r["target"] == "large"]
        entry = dict(preset=name, run=index + 1, exit_code=result.returncode,
                     seconds=round(time.monotonic() - started, 3),
                     requests=len(requests), failed=sum(not r["ok"] for r in requests),
                     complete=result.returncode == 0 and len(requests) == 20,
                     median_seconds=statistics.median(r["seconds"] for r in requests) if requests else None,
                     max_seconds=max((r["seconds"] for r in requests), default=None),
                     large_effective_kib_s=[round(r["bytes"] / 1024 / r["seconds"], 2) for r in large])
        summary.append(entry)
        (args.output / "summary.json").write_text(json.dumps(summary, indent=2), encoding="utf8")
        print(json.dumps(entry), flush=True)
    # A failure is a result to retain, rather than a reason to discard a run.
    return 0 if all(r["complete"] for r in summary) else 1


if __name__ == "__main__":
    raise SystemExit(main())
