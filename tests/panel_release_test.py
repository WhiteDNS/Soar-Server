"""Reject releases whose bundle, runtime, installer or tag disagree."""
import importlib.util
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location(
    "build_panel_bundle", Path(__file__).resolve().parents[1] / "scripts/build_panel_bundle.py"
)
bundle = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bundle)


class PanelReleaseTest(unittest.TestCase):
    def test_checked_in_versions_agree(self):
        with patch.dict(os.environ, {"GITHUB_REF_TYPE": "branch"}):
            bundle.verify_version()

    def test_rejects_stale_installer_or_runtime(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "internal/panel").mkdir(parents=True)
            for stale in ("installer", "runtime"):
                with self.subTest(stale=stale):
                    runtime = "0.0.0" if stale == "runtime" else bundle.VERSION
                    installer = "0.0.0" if stale == "installer" else bundle.VERSION
                    (root / "internal/panel/releases.go").write_text(f'var Version = "{runtime}"\n')
                    (root / "panel-install.sh").write_text(f"PANEL_VERSION={installer}\n")
                    with patch.object(bundle, "ROOT", root), self.assertRaises(RuntimeError):
                        bundle.verify_version()

    def test_tag_must_match_bundle(self):
        with patch.dict(os.environ, {"GITHUB_REF_TYPE": "tag", "GITHUB_REF_NAME": "panel-v" + bundle.VERSION}):
            bundle.verify_version()
        for tag in ("panel-v0.0.0", "v" + bundle.VERSION):
            with self.subTest(tag=tag), patch.dict(os.environ, {"GITHUB_REF_TYPE": "tag", "GITHUB_REF_NAME": tag}):
                with self.assertRaises(RuntimeError):
                    bundle.verify_version()


if __name__ == "__main__":
    unittest.main()
