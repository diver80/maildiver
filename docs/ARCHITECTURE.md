# OmaThunder Architecture

OmaThunder turns Thunderbird into the mail, calendar and contacts backend of a native desktop app with an AI copilot, and themes Thunderbird itself with Omarchy colors – on **Omarchy Linux** and **macOS** (Windows builds exist but are untested). Thunderbird keeps doing what it is good at (IMAP/SMTP/OAuth2, CalDAV, OpenPGP/S/MIME, filters); OmaThunder never needs credentials of its own.

## Three parts

| Part | Where | Job |
|---|---|---|
| **Add-on** (`extension/`) | inside Thunderbird | The bridge into Thunderbird's data, every call checked against one policy table (`lib/core.js`), plus the Omarchy theme for Thunderbird's own windows. Its settings page holds what OmaThunder may do at all: accounts & permissions, read-only, bridge, modules. |
| **Host** (`cmd/omathunder`, `internal/native`, `internal/bridge`, `internal/jobs`) | Go, started by Thunderbird over Native Messaging | Config, themes, the opt-in localhost bridge for the app and scripts, the AI layer, and the automatic work: triage scans (manual or on new mail) and archive & cleanup runs. Runs while Thunderbird runs, also when the app is closed. |
| **App** (`cmd/omathunder-ui`, `internal/nativeui`, `internal/copilot`) | Go/mygo, started by the user (`omathunder ui`) | Mail cockpit, reading pane, compose, calendar, contacts, quick rules (Thunderbird filters), copilot, AI settings and keys, scan and archive dialogs. Talks only to the bridge. |

```
┌───────────────────────────────────┐  Native Messaging (stdio)   ┌──────────────────────────────────┐
│ Thunderbird add-on                │ ◄─────────────────────────► │ omathunder host                  │
│ - bridge RPC (bridge policy)      │  tb_rpc ◄─► tb_rpc_result   │ - internal/bridge (127.0.0.1)    │
│ - host.* RPC (mailbox policy)     │  tb_event, triage_new_mail  │ - internal/jobs (scans, archive) │
│ - theme.update, body/compose CSS  │  get_config, save_config…   │ - internal/ai, triage, theme     │
│ - omaFilters / omaCalendar        │                             └───────┬──────────────────┬───────┘
└───────────────────────────────────┘                      127.0.0.1 HTTP + SSE         JSON cache
                                                     ┌────────────────▼────────────┐ ┌───▼───────────────────────┐
                                                     │ omathunder-ui / OmaThunder.app│ │ Omarchy bar widget        │
                                                     │ (nativeui, copilot), scripts │ │ (omarchy/BarWidget.qml)   │
                                                     └─────────────────────────────┘ └───────────────────────────┘
```

## Thunderbird stays untouched

- The add-on is a WebExtension with two small **experiment APIs** where Thunderbird has no official one: `omaFilters` (read/create native filters, open the filter dialog; registers the custom headers filters need) and `omaCalendar` (Lightning/CalDAV calendars). Experiments need extra ATN review and must be re-checked on every Thunderbird ESR.
- `omathunder setup` / `install.sh` only register a per-user Native Messaging manifest and a small launcher. Nothing edits `prefs.js` or the profile; `omathunder uninstall` removes only OmaThunder's own files.

## Two RPC paths, two policies

- **Bridge calls** (`RPCBroker.Call`): the bridge server forwards app/script requests with fixed method names. The add-on checks each against `BRIDGE_RPC_POLICY`: reads limited by `bridge.accountIds`, writes need read-only off and an account in `mailbox.triageAccountIds`, sending additionally `bridge.allowSend`, bridge filters can never delete. Go re-checks read-only, send and assistant gates as a second layer.
- **Host calls** (`RPCBroker.CallHost`, frame field `origin: "host"`): the host's own jobs. The add-on accepts only `host.*` methods this way (`host.triage.collect`, `host.archive.candidates|raw|finish`) and checks them against the mailbox policy (triage accounts, read-only, `features.triage` for scans) – not the bridge switch, so scans and archive runs also work with the bridge off. The bridge server never sets an origin, so bridge clients cannot reach these methods.
- **Message identity:** numeric message IDs are session-scoped; references are resolved by RFC Message-ID, ambiguous matches are refused before any write, and mutating bridge calls take `expectedMessageId`.

## Encrypted and signed mails

- Thunderbird decrypts OpenPGP/S/MIME for add-ons by default. OmaThunder reads with `decrypt: false` unless a caller asks for decryption: only the app's reading pane and AI actions the user starts on the open mail do (`?decrypt=1`). Scans, archive runs and the copilot's own lookups never decrypt; Thunderbird then asks for no password or PIN either.
- `describeMessageSecurity` (core.js) derives `{encrypted, decryption, signed}` from `decryptionStatus` and the MIME types. Signatures are never verified – the add-on API cannot; the app says so and offers to open the mail in Thunderbird.
- Compose sets signing via `selectedEncryptionTechnology` (TB 128+) and verifies Thunderbird took it; a reply to an encrypted mail is always encrypted, and a silent draft of one opens its window instead (the add-on cannot check that drafts are stored encrypted).

## Jobs in the host (`internal/jobs`)

- **Triage scan:** collects the inboxes of the triage accounts (all unread plus the newest read mails, capped), classifies them – Stage 0 local rules (newsletters, bots, invoices; security and transaction mails are never downgraded), earlier model results reused by Message-ID, then the classifier model within `ai.maxAiCallsPerScan` if `ai.classifyWithAi` is on – and stores the result in `triage-state.json`. One scan at a time; new-mail notifications are coalesced. The app shows the result (action view, tags, tray counts).
- **Archive & cleanup:** lists candidates (age, read, starred, scope), writes each mail as `.eml` (`internal/archive`), reads it back and compares SHA-256, and only then moves it into *Local Folders* or deletes it at the provider – both need read-only off. Simulate writes nothing. The archive folder comes only from `config.json`.
- Progress and results go to the app as SSE events (`triage.scan.*`, `archive.*`).

## AI

- One provider registry (`internal/ai`) for classify/analyze and chat: API backends through any-llm-go, local servers (Ollama, llama.cpp, llamafile) and CLIs. Endpoints come only from `config.json`; a key is only sent to its provider's host or the configured endpoint.
- Keys live in the env file next to `config.json` (`0600`). The app edits it; `config.LookupAPIKey` reads it fresh, so a new key works in the running host.
- Mail content is wrapped as untrusted data. The copilot only proposes changes (drafts, calendar entries, mark read); they run after the user clicks *Execute*. `claude-cli` runs without tools or MCP servers and gets the prompt on stdin; `gemini-cli` / `opencode-cli` cannot disable tools and need `ai.allowUnrestrictedCli`.

## Bridge

- Off by default. `bridge.Supervisor` polls `config.json` every 2 s and after every config write, opens `127.0.0.1:<port>` with a random 256-bit token in `bridge.json` (`0600`) and closes it when disabled.
- Errors travel as `code: detail` and map to HTTP statuses; a mutating call that times out answers `504 outcome_unknown`, compose calls carry an `idempotencyKey`.
- The add-on batches mail/calendar changes (~500 ms), filters them by readable account and forwards them as `tb_event`; `EventHub` fans them out to SSE subscribers.
- Endpoints and events: [BRIDGE.md](BRIDGE.md).

## Theme

- On Omarchy the host follows `~/.local/state/omarchy/current/theme/colors.toml` live; elsewhere it uses the 22 bundled palettes or the system's light/dark mode.
- The add-on applies the palette with `theme.update()` and, if `theme.styleBody` is on, CSS for message bodies and the compose editor. The app uses the same palette.

## Versioning

`ping` returns the host version and `ProtocolVersion` (4: bridge-only add-on, jobs in the host). The add-on compares it with its `PROTOCOL` constant and its settings page warns when they differ. Bump both whenever the messages change incompatibly.

## Runtime notes

- The host handles each request in its own goroutine; config writes are serialized. The app touches UI state only through `emitUpdate` on its UI thread.
- The triage store keeps at most 800 mails (open ones first, newest first), so responses stay below Thunderbird's 1 MB limit for host messages.
