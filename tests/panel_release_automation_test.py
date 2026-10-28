"""Release artifact integrity and publication gates."""
import hashlib
import io
import json
import os
from pathlib import Path
import struct
import sys
import tarfile
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0,str(Path(__file__).resolve().parents[1]/"scripts"))
import release_assets as release
import publish_release as publish
HEAD, BASE, MERGED = "1"*40, "2"*40, "3"*40
REPO="WhiteDNS/Soar-Server"

class ReleaseGates(unittest.TestCase):
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



    def test_missing_architecture_or_tampered_asset_cannot_publish(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory=Path(temporary)
            archive=directory/"soar-panel-0.9.1-linux-amd64.tar.gz"
            raw=self.archive()
            archive.write_bytes(raw)
            manifest=archive.with_suffix(".gz.sha256")
            manifest.write_text(hashlib.sha256(raw).hexdigest()+"  "+archive.name+"\n")
            with self.assertRaises(RuntimeError):
                publish.collect_assets(directory,"panel","0.9.1",MERGED)
            archive.write_bytes(raw+b"changed")
            with self.assertRaises(RuntimeError):
                publish.checksum_file(archive)

    def test_workflow_is_manual_and_publishes_same_run_artifacts(self):
        root=Path(__file__).resolve().parents[1]
        workflows=list((root/".github/workflows").glob("*.yml"))
        self.assertEqual([p.name for p in workflows],["panel.yml"])
        workflow=workflows[0].read_text()
        self.assertIn("  workflow_dispatch:",workflow)
        for trigger in ("  push:","  pull_request:","  workflow_run:"):
            self.assertNotIn(trigger,workflow)
        self.assertIn("needs: [plan, verify]",workflow)
        self.assertNotIn("run-id:",workflow)
        self.assertNotIn("finish-release:",workflow)
        self.assertNotIn("actions: write",workflow)
        self.assertNotIn("pull-requests: write",workflow)
        self.assertNotIn("dispatches",(root/"scripts/publish_release.py").read_text())
        self.assertFalse((root/"scripts/finish_panel_release.py").exists())

    def test_only_exact_main_checkout_can_publish(self):
        env={"GITHUB_REPOSITORY":REPO,"GITHUB_REF":"refs/heads/main","GITHUB_SHA":HEAD}
        with patch.dict(os.environ,env),patch.object(publish.subprocess,"check_output",return_value=HEAD+"\n"):
            self.assertEqual(publish.validate_context(),HEAD)
            with patch.dict(os.environ,{"GITHUB_REF":"refs/heads/other"}),self.assertRaises(RuntimeError):
                publish.validate_context()
        with patch.dict(os.environ,env),patch.object(publish.subprocess,"check_output",return_value=BASE+"\n"),self.assertRaises(RuntimeError):
            publish.validate_context()

    def test_existing_release_skips_builds_and_wrong_tag_stops_early(self):
        with tempfile.TemporaryDirectory() as temporary:
            output=Path(temporary)/"outputs"
            notes=Path(temporary)/"notes.md"
            notes.write_text("notes")
            env={"RELEASE_TARGET":"panel","GITHUB_OUTPUT":str(output)}
            with patch.dict(os.environ,env),patch.object(sys,"argv",["publish_release.py","--plan"]),patch.object(publish,"validate_context",return_value=HEAD),patch.object(publish,"configuration",return_value=("0.9.1","panel-v0.9.1","Panel",notes)),patch.object(publish,"api",return_value={"draft":False,"prerelease":False}),patch.object(publish,"resolve_tag",return_value=BASE):
                publish.main()
            self.assertIn("build=false",output.read_text())
            output.unlink()
            with patch.dict(os.environ,env),patch.object(sys,"argv",["publish_release.py","--plan"]),patch.object(publish,"validate_context",return_value=HEAD),patch.object(publish,"configuration",return_value=("0.9.1","panel-v0.9.1","Panel",notes)),patch.object(publish,"api",return_value=None),patch.object(publish,"resolve_tag",return_value=BASE),self.assertRaises(RuntimeError):
                publish.main()
            self.assertFalse(output.exists())

    def test_failed_draft_is_resumed_without_allocating_new_builds(self):
        with tempfile.TemporaryDirectory() as temporary:
            notes=Path(temporary)/"notes.md"
            notes.write_text("notes")
            with patch.dict(os.environ,{"RELEASE_TARGET":"panel"}),patch.object(sys,"argv",["publish_release.py","--plan"]),patch.object(publish,"validate_context",return_value=HEAD),patch.object(publish,"configuration",return_value=("0.9.1","panel-v0.9.1","Panel",notes)),patch.object(publish,"api",return_value={"draft":True,"prerelease":False}),self.assertRaises(RuntimeError):
                publish.main()

    def test_publication_reuses_verified_bytes_and_retry_does_not_reupload(self):
        for existing in (None, {"draft":True,"prerelease":False}, {"draft":False,"prerelease":False}):
            with self.subTest(existing=existing),tempfile.TemporaryDirectory() as temporary:
                directory=Path(temporary)
                for arch in ("amd64","arm64"):
                    archive=directory/f"soar-panel-0.9.1-linux-{arch}.tar.gz"
                    raw=self.archive(arch)
                    archive.write_bytes(raw)
                    archive.with_suffix(".gz.sha256").write_text(hashlib.sha256(raw).hexdigest()+"  "+archive.name+"\n")
                notes=directory/"notes.md"
                notes.write_text("notes")
                summary=directory/"summary.md"
                previous=Path.cwd()
                try:
                    os.chdir(directory)
                    with patch.dict(os.environ,{"RELEASE_TARGET":"panel","GITHUB_STEP_SUMMARY":str(summary)}),patch.object(sys,"argv",["publish_release.py","--artifacts",str(directory)]),patch.object(publish,"validate_context",return_value=MERGED),patch.object(publish,"configuration",return_value=("0.9.1","panel-v0.9.1","Panel",notes)),patch.object(publish,"api",return_value=existing) as api,patch.object(publish,"resolve_tag",return_value=MERGED if existing else None),patch.object(publish.subprocess,"run") as command,patch.object(publish,"public_report",return_value={"verified":True}):
                        publish.main()
                    if existing is None:
                        self.assertEqual(api.call_args_list[-1].args[0],"POST")
                        self.assertEqual(command.call_args.args[0][2],"create")
                    elif existing["draft"]:
                        self.assertEqual([call.args[0][2] for call in command.call_args_list],["upload","edit"])
                    else:
                        command.assert_not_called()
                    self.assertTrue((directory/"release-verification.json").exists())
                    self.assertIn("verified artifacts",summary.read_text())
                finally:
                    os.chdir(previous)

    def test_standalone_source_and_internal_checksum_are_required(self):
        files={"install.sh":b"installer","SOURCE-VERSIONS.txt":f"Setup: 0.9.1\nSource commit: {HEAD}\n".encode()}
        for name in ("soar-server","soar-check"):
            elf=bytearray(20);elf[:6]=b"\x7fELF\x02\x01";elf[18:20]=struct.pack("<H",62)
            files[f"bin/linux-amd64/{name}"]=bytes(elf)
        files["SHA256SUMS"]="".join(f"{hashlib.sha256(raw).hexdigest()}  {name}\n" for name,raw in files.items()).encode()
        def archive():
            out=io.BytesIO()
            with tarfile.open(fileobj=out,mode="w:gz") as tar:
                for name,raw in files.items():
                    member=tarfile.TarInfo("soar-server-setup-0.9.1-linux-amd64/"+name);member.size=len(raw);tar.addfile(member,io.BytesIO(raw))
            return out.getvalue()
        publish.verify_standalone(archive(),"0.9.1",HEAD)
        with self.assertRaises(RuntimeError):
            publish.verify_standalone(archive(),"0.9.1",MERGED)
        files["install.sh"]=b"tampered"
        with self.assertRaises(RuntimeError):
            publish.verify_standalone(archive(),"0.9.1",HEAD)

if __name__ == "__main__":
    unittest.main()
