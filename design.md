# WhiteDNS panel visual system

Scope: login, dashboard, accounts, settings, events, administrator credentials,
anti-flood, backups, updates and domain/security views.
Keep routes, authentication, account operations and profile contracts intact.

Reference: the user's 2026-10-24 panel screenshots and explicit green/black/white
brand direction. Keep the operational layout, bundled Persian font and real data.
Correct navigation overflow, mixed-language options and inconsistent card widths.

Genre: modern-minimal administration interface. One consistent system across views.
Structure: a collapsible rail on the writing-direction start edge, compact sticky toolbar, instrument cards, a wide
time-series region and a supporting account summary. Forms use grouped fields;
account management uses the existing real table and dialogs.

Typography: bundled Vazirmatn v33.003 (SIL OFL), including Persian and Latin glyphs.
Body 14px, controls 14px, secondary labels at least 12px, headings 22–26px, primary
readings 30px. Use normal letter spacing for Persian and tabular numeric readings.

Color: dark by default, neutral black/gray surfaces, white text and one green
accent for actions, selected navigation, plots and availability. The light theme
uses white surfaces and a darker green with the same geometry. Errors and
destructive actions retain explicit text, icons and high-contrast borders; color
alone never communicates status. OKLCH named tokens live in tokens.css and its
identical embedded web copy. All new styles consume those tokens.

Spacing: 4px base, 24px desktop / 16px mobile card gutters, 44px select controls,
12px card corners, 24px section gaps, visible borders and restrained inset shadows.
Sidebar 272px expanded / 76px compact; desktop preference persists. Toolbar uses
two detached 56px bordered surfaces inside an 88px desktop header. The location
box contains the rail toggle and breadcrumb; the actions box contains language,
live status, refresh, theme and sign-out. Small phones stack the two boxes and
retain 44px controls. The sidebar brand uses the same inset surface and gutters,
with a 72px minimum height, 12px vertical padding and explicit 26px/18px line
heights for its two text lines. Expanded brand text must keep at least 12px clear
space from the top and bottom borders in both languages. Secondary text and
footer links use a 12px minimum, apart from the compact version-only badge.
At widths up to 800px the rail becomes a dismissible modal drawer;
the table retains its own horizontal scroll and the page must not overflow.

Interaction: visible instant focus rings, skip link, native dialogs, text help on
hover/focus/touch, keyboard-operable menus and inert background during the mobile
drawer. Motion uses opacity/transform for page/dialog/menu entry, drawer movement
and button press feedback; reduced motion disables it. Three named easings and
160/260ms durations live in tokens.css. Charts use only real dashboard samples collected
since the page opened; unknown readings display an empty state. No fabricated
history, speed claims or system controls without corresponding server actions.

Implementation: additive redesign.css and ui.js over the existing routes; retain
legacy style.css for compatibility. No production files are deleted.

Anti-flood: aligned full-width cards, four live counters, separate quarantine
status, a bounded-width mode selector, four collapsible settings groups and an
unsaved-change indicator. Refreshing reports never resets the edited policy.

Page families: operational workbench throughout; side-rail navigation, a fully
framed sidebar footer with side/bottom gutters, and a matching content footer
after the page. The desktop rail ends 16px above the viewport edge; its footer
retains another 24px inner bottom gutter. Compact and mobile variants keep a
visible footer frame. App pages have no decorative imagery, marketing blocks or invented
metrics. The shared design system takes precedence over per-page theme rotation.

Hover/focus: toolbar controls, navigation, editable fields, select options,
expandable summaries, help controls and share-mode tabs use the same green
interaction language. The active selection remains distinguishable on hover;
disabled controls retain native disabled state and a not-allowed cursor. Main
settings fieldsets and Telegram preferences use framed groups in both themes.
