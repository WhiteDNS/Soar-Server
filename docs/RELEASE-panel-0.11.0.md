# WhiteDNS Server Management 0.11.0

## Connection and engine diagnostics

- Add **Logs & assistant** with live connection, resolver, DNS queue, engine lifecycle and panel-operation events. Search and filter by source, severity, account and time, pause the live view, or download a JSON report.
- Keep the latest 2,000 structured records across restarts, coalesce repeated events and show recent errors and warnings on the dashboard. Records contain safe event codes and classified failure reasons; traffic destinations, DNS questions, passwords and tunnel keys are excluded. Disk writes run every five seconds; abrupt shutdown can lose the latest buffered entries.

## AI analysis inside the panel

- Chat with an optional sanitized diagnostic attachment and preview the included data before sending. Account identifiers are pseudonymized. Sharing requires the administrator to approve the configured destination and explicitly send a message.
- Configure **Gemini, OpenRouter, 9Router, DeepSeek, OpenAI, Anthropic / Claude, Groq, Mistral, xAI, Together AI, Fireworks AI, Ollama, LM Studio**, or **Custom**. Native Gemini and Anthropic Messages and OpenAI-compatible Chat Completions formats are available. Base URL, exact model ID, key, output-token limit and JSON mode are configurable; model access and optional capabilities depend on the provider.
- Store provider credentials in a private machine-local configuration, outside the panel database backup. Keys are never returned to the browser or included in the model prompt. Custom endpoints require HTTPS; HTTP is supported only on the panel server's loopback interface. Private-network destinations and redirects are blocked.

## Settings proposals with approval and undo

- The assistant can propose existing Soar profile tuning, public resolvers, country and server stream limits. Every field and value is validated by the server. Chat cannot execute commands, change accounts, credentials, domains, keys, firewall or anti-flood policy.
- Review the exact before/after values, then approve the proposal separately. Proposals expire after ten minutes, belong to the creating administrator session and cannot overwrite settings changed since analysis. Dismissed proposals are revoked.
- Applying approved settings restarts active tunnel engines and closes existing streams. A separately confirmed undo is available for ten minutes if settings have not changed again. Both operations are recorded in administration events.

## Upgrade and verification limits

Update through the panel or the existing installer, then refresh the browser. Existing accounts, tunnel keys, subscription URLs and persisted usage remain valid. AI is disabled until configured and does not send diagnostics automatically. Soar profile changes require clients to refresh their subscription or reimport the profile.

Automated checks cover all three API formats with local mock providers, consent, redaction, proposal validation, expiry/replay/session protections, approval and undo conflicts, log persistence and Persian/English desktop/mobile browser workflows. Release CI verifies Linux amd64 and arm64, race checks and the packaged archives. Real AI credentials, quota, physical-device and restricted-network connectivity require separate tests on the target installation.

[راهنمای فارسی لاگ‌ها و دستیار](https://github.com/WhiteDNS/Soar-Server/blob/main/docs/DIAGNOSTICS-ASSISTANT.fa.md)
