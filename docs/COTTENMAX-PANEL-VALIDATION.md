# Managed CottenMax validation

Panel 0.10.0 uses Cotton source `7db7b73658c791cce62300d29647e4d369d6e1e4` plus the checksummed patch recorded in `patches/cotten-engine.json`. Both CI workflows check out this exact revision. The manifest verifies every source file, including the new mixed-mode session test, and rejects unexpected build inputs.

## Confirmed local checks

- Native HTTPS succeeded in Normal and Max modes over both UDP and TCP.
- The real current Cotton executable passed eight primary/backup-domain transport scenarios. Tests verify byte-exact stream traffic, tunneled DNS, shared usage with Soar, quota exhaustion and synchronous egress disconnection. Normal's existing generic UDP relay checks remain enabled; Max does not implement SOCKS UDP association.
- The embedded mixed-mode test verified that a Normal session blocks Max at the account cap, releasing it admits Max, Max blocks another Normal session, diagnostics include Max, and shutdown releases the budget.
- Native arm64 CI exposed a shutdown ordering issue: the Max server returned before its sweep released session reservations. Shutdown now joins the sweep and HELLO workers and rejects a cancelled HELLO before allocation. The regression passed thirty consecutive local race-detector runs after this correction.
- Focused panel lifecycle/settings/HTTPS and embedded engine tests passed with the race detector. Vet passed.
- The complete Android unit suite and complete Desktop Go suite passed. Their configuration regressions cover long panel domains, backup domains, label shaping and jitter; generated settings fit the initial Max HELLO without mutating stored options.
- Panel browser checks passed all nine tabs in Persian/English, dark/light themes and desktop/mobile sizes. The source link appears once in the sidebar. Edge and toolbar folding, keyboard activation and saved compact preference are exercised. Screenshots were visually inspected.
- All twelve release-consistency and publication-guard tests passed.

The full local panel Go run encountered the existing Telegram file-mode assertion on Windows (`unsafe config permissions`); command and operations packages passed. Linux CI is the required platform check for the complete Go/race/browser/installer and native bundle suites on amd64/arm64 before publication. A successful local build is not a production deployment or a restricted-network field test.

## Client compatibility

The server retains account domains, shared keys and profile links. Android 1.6.14 and Desktop 1.4.5 calculate the initial Max QNAME budget from active/standby names and shaping settings; older installed clients may need a larger initial QNAME setting for long names. Normal mode remains available. Max session identity derives from the existing Cotton credential, with no separate service or key file.

No VPS or physical-device test was performed for this change.
