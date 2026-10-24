# WhiteDNS Panel 0.8.1

- Fix Connection engine selection in the New Account and Edit Account dialogs. The dropdown could open while the browser blocked clicks on its options.
- Keep the shared dropdown inside its owning dialog so mouse, touch and keyboard input work correctly. Filters outside dialogs continue to use the same component.
- Add real pointer and touch browser checks for all seven engine choices, saved account creation and edits, Persian and English, desktop and mobile, and the fallback for browsers without Popover support.

Upgrade to 0.8.1, refresh the panel page, and select MasterDNS or all three engines when creating or editing an account. Existing accounts keep their current engine settings.
