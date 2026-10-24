# WhiteDNS Server Management 0.9.1

- Unify every panel page around green actions, black/white surfaces, clearer card borders and readable secondary text in both themes.
- Add a collapsible desktop sidebar with a saved preference, keyboard tooltips and a mobile drawer with focus management. Frame the brand, toolbar groups and footers with consistent spacing.
- Improve the anti-flood page with live metric cards, grouped expandable settings, an unsaved-change indicator and the translated English Off option.
- Frame server settings and Telegram preferences; align hover, focus, selected and disabled states across controls and sharing tabs. Respect reduced motion.
- Correct mixed-language maintenance feedback and the server stream-limit description for all three engines.

## Upgrade

Back up the panel, install the update and refresh the browser page. Accounts, keys, subscriptions and the saved anti-flood policy remain in place. Version 0.9.0's default anti-flood enforcement still applies; review its thresholds against legitimate server load.

The UI was checked in Persian/English, dark/light and desktop/mobile layouts, including all nine tabs and all settings help controls. Native Linux CI and bundle checks run before publication. Local browser checks do not establish real VPS connectivity or prevention of hosting suspension.

[UI validation](https://github.com/WhiteDNS/Soar-Server/blob/panel-v0.9.1/docs/PANEL-UI-VALIDATION-2026-10-24.md) · [Anti-flood guide](https://github.com/WhiteDNS/Soar-Server/blob/panel-v0.9.1/docs/ANTIFLOOD.fa.md)
