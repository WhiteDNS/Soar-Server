# One manual release workflow

Open **Actions → Build and release WhiteDNS → Run workflow**. Select `main`
and choose `panel` (default) or `standalone`.

This is the repository's only workflow. PRs, pushes and tags do not start builds.
The workflow does not merge PRs. Review and merge the intended changes, update
the product version and release notes, then trigger it once.

One run performs:

1. Check the immutable source, version and release state. An already published
   version skips the build runners. Conflicting unpublished tags stop here.
2. Verify and build the panel on native amd64 and arm64 runners, including Go,
   race, installer, recovery, package and browser tests. Standalone releases use
   the amd64 runner. Shared checks have one definition.
3. Download artifacts from those jobs in the **same run**, verify their source,
   checksums and architectures, create the version tag and publish the release.
   The release job does not rebuild anything or dispatch another workflow.

All build jobs must pass before publication. Tags are never moved and published
releases are never overwritten. Panel releases remain the latest release;
standalone publication does not replace that designation.

If only publication fails, use **Re-run failed jobs**, not **Re-run all jobs**.
The release job reuses the successful build jobs' artifacts and can resume its
partial draft upload. It does not repeat verification or builds.

Panel versions must agree in `internal/panel/releases.go`, `panel-install.sh`
and `scripts/build_panel_bundle.py`; add `docs/RELEASE-panel-VERSION.md`.
Standalone versions must agree in `setup.sh` and `scripts/build_bundle.py`;
update `RELEASE-NOTES.md`.

Local validation for pipeline changes:

```sh
python -m unittest discover -s tests -p 'panel_release*_test.py'
actionlint -shellcheck= -pyflakes= .github/workflows/panel.yml
```
