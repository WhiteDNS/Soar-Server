"""Exercise the fail-closed gates used by the unattended release job."""
import copy
import hashlib
import importlib.util
import io
from pathlib import Path
import struct
import tarfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("finish_panel_release", Path(__file__).resolve().parents[1] / "scripts/finish_panel_release.py")
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)
HEAD, BASE, MERGED = "1" * 40, "2" * 40, "3" * 40
REPO = "WhiteDNS/Soar-Server"


class ReleaseGates(unittest.TestCase):
    def pr(self):
        return {"head": {"sha": HEAD, "repo": {"full_name": REPO}}, "base": {"sha": BASE, "ref": "main", "repo": {"full_name": REPO}}, "draft": False, "state": "open", "merged": False}

    def test_only_authorized_head_base_and_repository(self):
        release.validate_pr(self.pr(), REPO, HEAD, BASE)
        for mutate in (lambda p: p["head"].update(sha=MERGED), lambda p: p["base"].update(sha=MERGED),
                       lambda p: p["head"]["repo"].update(full_name="someone/fork"),
                       lambda p: p["base"].update(ref="other"), lambda p: p.update(draft=True), lambda p: p.update(state="closed")):
            pr = self.pr()
            mutate(pr)
            with self.assertRaises(RuntimeError):
                release.validate_pr(pr, REPO, HEAD, BASE)

    def ci(self):
        runs, jobs = [], {}
        for index, (path, names) in enumerate(release.REQUIRED.items(), 1):
            runs.append({"id": index, "path": path, "status": "completed", "conclusion": "success"})
            jobs[index] = [{"name": name, "status": "completed", "conclusion": "success"} for name in names]
        return runs, jobs

    def test_requires_both_workflows_and_both_architectures(self):
        runs, jobs = self.ci()
        self.assertTrue(release.checks_ready(runs, jobs))
        self.assertFalse(release.checks_ready(runs[:1], jobs))
        jobs[1].pop()
        self.assertFalse(release.checks_ready(runs, jobs))

    def test_latest_run_cannot_be_replaced_by_older_success(self):
        runs, jobs = self.ci()
        later = dict(runs[0], id=99, status="in_progress", conclusion=None)
        self.assertFalse(release.checks_ready(runs + [later], jobs))
        later.update(status="completed", conclusion="failure")
        with self.assertRaises(RuntimeError):
            release.checks_ready(runs + [later], jobs)

    def test_skipped_or_failed_required_jobs_are_not_success(self):
        for conclusion in ("failure", "cancelled", "skipped", "timed_out"):
            runs, jobs = self.ci()
            jobs[1][0]["conclusion"] = conclusion
            with self.subTest(conclusion=conclusion), self.assertRaises(RuntimeError):
                release.checks_ready(runs, jobs)

    def test_merge_uses_exact_head_and_checks_target(self):
        def api(method, endpoint, body=None):
            if endpoint.endswith("heads/main"):
                return {"object": {"sha": BASE}}
            if "/reviews?" in endpoint:
                return []
            self.assertEqual(body, {"sha": HEAD, "merge_method": "squash"})
            return {"merged": True, "sha": MERGED}
        with patch.object(release, "api", side_effect=api):
            self.assertEqual(release.merge_pr("repos/" + REPO, 18, self.pr(), HEAD, BASE), MERGED)
        with patch.object(release, "api", return_value={"object": {"sha": MERGED}}), self.assertRaises(RuntimeError):
            release.merge_pr("repos/" + REPO, 18, self.pr(), HEAD, BASE)

    def test_different_merged_tree_stops_tagging(self):
        with patch.object(release, "api", side_effect=[{"tree": {"sha": HEAD}}, {"tree": {"sha": MERGED}}]), self.assertRaises(RuntimeError):
            release.verify_merge_tree("repos/" + REPO, HEAD, MERGED)

    def test_existing_tag_is_never_moved(self):
        with patch.object(release, "resolve_tag", return_value=HEAD), patch.object(release, "api") as api, self.assertRaises(RuntimeError):
            release.ensure_tag("repos/" + REPO, "panel-v0.9.1", MERGED)
        api.assert_not_called()

    def archive(self, arch="amd64", mutate=None, unsafe=False):
        version = "0.9.1"
        files = {"ARCH": (arch + "\n").encode(), "panel-install.sh": b"PANEL_VERSION=0.9.1\n",
                 "SOURCE-VERSIONS.txt": f"WhiteDNS Server Management: {version}\nPanel source commit: {MERGED}\n".encode()}
        for name in ("soar-panel", "soar-panel-ops", "soar-check"):
            elf = bytearray(20)
            elf[:6] = b"\x7fELF\x02\x01"
            elf[18:20] = struct.pack("<H", {"amd64": 62, "arm64": 183}[arch])
            files[f"bin/linux-{arch}/{name}"] = bytes(elf)
        files["SHA256SUMS"] = "".join(f"{hashlib.sha256(data).hexdigest()}  {name}\n" for name, data in files.items()).encode()
        if mutate:
            mutate(files)
        out = io.BytesIO()
        with tarfile.open(fileobj=out, mode="w:gz") as tar:
            for name, data in files.items():
                member = tarfile.TarInfo(f"soar-panel-{version}-linux-{arch}/" + ("../escape" if unsafe else name))
                member.size = len(data)
                tar.addfile(member, io.BytesIO(data))
        return out.getvalue()

    def test_downloaded_bundle_provenance_checksums_and_architectures(self):
        for arch in ("amd64", "arm64"):
            self.assertEqual(release.verify_archive(self.archive(arch), arch, "0.9.1", MERGED), 6)
        with self.assertRaises(RuntimeError):
            release.verify_archive(self.archive(), "amd64", "0.9.1", HEAD)
        with self.assertRaises(RuntimeError):
            release.verify_archive(self.archive(unsafe=True), "amd64", "0.9.1", MERGED)
        with self.assertRaises(RuntimeError):
            release.verify_archive(self.archive(mutate=lambda files: files.update(ARCH=b"tampered")), "amd64", "0.9.1", MERGED)
        with self.assertRaises(RuntimeError):
            release.verify_archive(self.archive(mutate=lambda files: files.update(unlisted=b"unexpected")), "amd64", "0.9.1", MERGED)


if __name__ == "__main__":
    unittest.main()
