# WhiteDNS panel visual system

Scope: the existing login, dashboard, accounts, settings, events and backup views.
Keep routes, authentication, account operations and profile contracts intact.

Reference: user-supplied 3x-ui screenshots. Extract the compact operational layout,
charcoal surfaces, blue signal color, legible Persian labels and aligned controls.
Do not import 3x-ui implementation, marks, advertising or Xray-specific controls.

Genre: modern-minimal administration interface. One consistent system across views.
Structure: persistent right rail, compact top toolbar, instrument cards, a wide
time-series region and a supporting account summary. Forms use grouped fields;
account management uses the existing real table and dialogs.

Typography: bundled Vazirmatn v33.003 (SIL OFL), including Persian and Latin glyphs.
Body 14px, controls 14px, secondary labels at least 12px, headings 22–26px, primary
readings 30px. Use normal letter spacing for Persian and tabular numeric readings.

Color: dark by default, charcoal rather than saturated navy; blue actions and
plots, green availability, amber expiry and red errors. The light variant uses
the same sizes and geometry. Named tokens live in tokens.css and its embedded
web copy. All new styles consume those tokens.

Spacing: 4px base, compact 16–20px card gutters, 40px controls, 12px card corners.
Sidebar 220px, topbar 64px. At small widths the rail becomes a dismissible drawer;
the table retains its own horizontal scroll and the page must not overflow.

Interaction: visible focus, native dialogs, text help on hover/focus/touch, no
decorative entrance animations. Charts use only real dashboard samples collected
since the page opened; unknown readings display an empty state. No fabricated
history, speed claims or system controls without corresponding server actions.

Implementation: additive redesign.css and ui.js over the existing routes; retain
legacy style.css for compatibility. No production files are deleted.
