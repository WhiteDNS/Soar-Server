# Panel UI validation — 2026-10-24

Scope: the green panel redesign on `feature/panel-green-ui`, prepared for
panel-v0.9.1. The checks below describe local validation; the unattended release
workflow separately waits for Linux CI and verifies published assets.

## Changes reviewed

- Two separate framed toolbar groups, a framed brand area and framed footers.
- Expanded/compact desktop navigation, saved preference and mobile modal drawer.
- Neutral dark/light surfaces with green actions, visible borders and consistent
  input, hover, focus, disabled and selected states.
- Anti-flood metric cards, quarantine view, four collapsible settings groups,
  translated Off option and an unsaved policy indicator.
- Framed server settings and Telegram preferences; all dropdowns and all 21 help
  controls opened during browser validation.
- Explicit brand line heights and at least 12px vertical text clearance; readable
  secondary/footer typography; inset sidebar bottom and footer gutters.
- Known missing-maintenance-service feedback no longer appends raw English to
  the Persian restore guidance. Unknown diagnostic details are preserved.

## Verification

`tests/panel-ui.cjs` includes `tests/panel-design.cjs`. It checks every authenticated
tab in Persian/English, dark/light and 1680/1024/768/414/375/320 widths. It checks
toolbar frames and hover feedback, brand typography/clearance, navigation bounds,
collapse persistence, compact keyboard tooltips, drawer focus and inert state,
footer gutters, anti-flood dropdown translation, editable policy preservation,
representative WCAG contrast and reduced motion.

The complete browser acceptance suite also exercises login, account creation and
editing, limits, profile/QR/subscription sharing, clipboard fallback, account menus,
settings save/reload, domain forms, backup/update views, anti-flood, bilingual
rendering, administrator credentials, session revocation and bulk actions.

Full-page review artifacts are generated in the parent workspace's
`output/panel-ui-review-20261024`: 72 complete tab captures (9 tabs × 2 languages ×
2 themes × 2 device sizes), separate settings-group captures, interaction states,
an offline gallery, PNG checksums and a ZIP archive. The gallery is verified in
Chrome, including all eight page-filter combinations and image decoding.

These are local Windows/Chrome checks with temporary test accounts. They do not
establish production deployment, Linux CI success for this branch, real VPS
resource readings, or external connectivity. Unavailable host/maintenance data
remains visibly unavailable rather than being filled with example metrics.
