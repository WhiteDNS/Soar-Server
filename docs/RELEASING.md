# One manual release workflow

Open **Actions → Build and release WhiteDNS → Run workflow**. Select `main`
and choose `panel` (default) or `standalone`.

This is the repository's only release workflow. PRs run the read-only tests in
`ci.yml`; they cannot publish. Pushes and tags do not start builds.
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

## Signing panel releases

The release job signs each panel release automatically, right after publishing,
as `*.tar.gz.sha256.ci.sig`. Anyone who can run this workflow on `main` can publish
a release panels will install, with no key to handle. The key lives only in the
`release` GitHub Environment, as `RELEASE_SIGNING_KEY`, restricted to `main`. Add a
required reviewer to that environment to require a second person's approval.

Keep access small and use two-factor authentication: this signature does not
protect against a stolen account that can run the workflow. The offline key
exists for that case and for recovery. Keep it off GitHub, with an offline backup.

Panels up to 0.14.4 trust only the offline key and read only `*.sha256.sig`. They
stay on the newest release that has that file. **The first release after this
change must also be signed offline once**, so those panels can reach it and then
continue with workflow signatures:

```sh
gh release download panel-vVERSION --repo WhiteDNS/Soar-Server --pattern '*.sha256' --dir sign
go run ./cmd/soar-release-sign sign OFFLINE_KEY_FILE sign/*.sha256
gh release upload panel-vVERSION sign/*.sig --repo WhiteDNS/Soar-Server
```

If the workflow key leaks: remove it from `releaseSigningKeys` in
`internal/panel/releases.go`, add a new public key, replace the secret, and sign
that release with the offline key the same way. Panels then stop trusting the old
key once updated. The install bootstrap checks SHA-256 only and does not verify
signatures.

Panel versions must agree in `internal/panel/releases.go`, `panel-install.sh`
and `scripts/build_panel_bundle.py`; add `docs/RELEASE-panel-VERSION.md`.
Standalone versions must agree in `setup.sh` and `scripts/build_bundle.py`;
update `RELEASE-NOTES.md`.

Local validation for pipeline changes:

```sh
python -m unittest discover -s tests -p 'panel_release*_test.py'
actionlint -shellcheck= -pyflakes= .github/workflows/panel.yml
```
