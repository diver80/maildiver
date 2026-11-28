# PostDive Architecture

PostDive is a native desktop & mobile mail client with calendar, contacts and an AI copilot, actively used on **macOS** and **Android**, with native builds for **Linux** and **Windows**. It talks to mail servers directly over IMAP, SMTP, CalDAV and CardDAV and keeps a local SQLite copy. Everything runs in one Go process: no Thunderbird, no external browser engine, no local bridge server, no sockets between its parts.

## One process, four layers

```
cmd/postdive [--mcp]
   │
   ├─ internal/mailengine   IMAP (IDLE) · SMTP · SQLite + FTS5 · Google Calendar/Contacts · CalDAV/CardDAV · iCal · S/MIME & OpenPGP · Thunderbird import
   │        │  Engine.Call(method, params)          events.Hub (mail.new, mail.sync, calendar.changed, …)
   │        ▼                                                │
   ├─ internal/backend      typed client: errors with codes, read-only and assistant gates, idempotency keys
   │        │                                                │
   │        ├─ internal/jobs      triage scans, archive & cleanup (Engine.CallHost "host.*")
   │        ├─ internal/copilot   chat agent with mail/calendar/contact/folder tools; only proposes changes
   │        ├─ internal/mcp       local stdio MCP server (`postdive --mcp`): read-only by default, opt-in drafts/writes
   │        ▼                                                ▼
   └─ internal/nativeui     mygo UI: folders, list, reading pane (Formatted/Text/Source), conversations, compose, calendar, contacts, unified settings
```

| Package | Job |
|---|---|
| `mailengine` | Accounts and sync (IMAP IDLE on INBOX with context socket deadlines, 45 s polling fallback where IDLE is missing, folder sync, Offline Outbox retry queue), local store, message actions written back to the server, sending, calendars (local, Google Calendar, two-way CalDAV, iCal feeds), contacts (local, Google Contacts, two-way CardDAV), filter rules, attachments, Thunderbird profile import (read-only). |
| `backend` | The API the UI, copilot and jobs use. `Engine.Call` takes JSON-shaped parameters, so the method set stays the same one the engine tests exercise. Engine errors arrive as `code: detail` and become `*backend.APIError`; a mutating call that times out becomes `outcome_unknown`; compose carries an idempotency key and returns outbox/security status. |
| `events` | In-process fan-out of engine events. Subscribers get a buffered channel; a slow subscriber drops events instead of blocking the engine. |
| `jobs`, `triage`, `archive` | Triage classification (local rules first, then the model within `ai.maxAiCallsPerScan`), result in `triage-state.json`; archive runs write `.eml` files, maintain `.postdive-index.json`, check SHA-256 and only then delete. |
| `ai`, `copilot`, `mcp` | Provider registry (API, local servers such as Ollama/llama.cpp/MTPLX, CLIs `claude-cli`/`agy`/`opencode-cli` sandboxed in `<StateDir>/cli-sandbox`), personal custom instructions (`ai.customInstructions`), encrypted/decrypted mail AI hard-block, the in-app copilot agent (`search_messages`, `get_message`, `get_thread`, `search_contacts`, `list_folders`, `list_calendar_events` + proposal-only write tools), and the built-in local stdio MCP server (`postdive --mcp`, see [MCP.md](MCP.md)). |
| `nativeui` | The app. It touches UI state only on its UI thread (`emitUpdate`); network and disk work run in goroutines; HTML WYSIWYG compose with consistent right-side AI Assist dock, inline Quick-Reply with AI chips, Snooze & Waiting-for-Reply, Subscriptions cleanup (`List-Unsubscribe` + SSRF-guarded One-Click POST), tracker-pixel counting, Cron-style calendar with mini-month/calendar sidebar + Live-Meeting toolbar pill + `.ics` Free/Busy conflict badge, 1-click MCP client installer, optional Web Preview embed platform `mygo.WebView`, and full Phone UI parity (`phone.go`). The sidebar defaults to a **compact folder view** (`app.compactFolders`, optional per-account `app.pinnedFolders`). |
| `config`, `secretstore`, `theme` | `config.json`, the API-key env file (`0600`), encrypted secrets (macOS Keychain, Android Keystore, Windows DPAPI, Linux libsecret/AES-256-GCM sealed store), Omarchy live sync and 24 bundled themes (default **Diver** / **Diver Light**). |

## Where data lives

| | macOS | Linux |
|---|---|---|
| `config.json`, `env` (API keys) | `~/Library/Application Support/postdive/` | `~/.config/postdive/` (`XDG_CONFIG_HOME`) |
| `postdive.db` (mail, calendars, contacts) | `~/Library/Application Support/postdive/` | `~/.local/share/postdive/` (`XDG_DATA_HOME`) |
| `triage-state.json`, `bar-summary.json`, `cli-sandbox/` | `~/Library/Application Support/postdive/` | `~/.local/state/postdive/` (`XDG_STATE_HOME`) |
| Passwords, OAuth tokens, S/MIME & OpenPGP keys | Keychain | `~/.config/postdive/secrets.seal` (libsecret / AES-256-GCM) |

Config files never hold secrets.

## Sync and the local store

- Mails are stored once per folder copy (`id` = account + folder + UID); search uses an FTS5 index over subject, addresses and text.
- List pages load headers only (`MessageQuery.HeadersOnly`); bodies are read when a mail opens.
- Actions (read, star, move, archive, trash) go to the server first and then update the store; the engine publishes `mail.updated` / `mail.moved` / `mail.deleted`, and the UI reloads with a debounce.

## Conversations

- `resolveThreadID` gives every mail the ID of its conversation's root: the first `References` entry, else `In-Reply-To`, else a stored reply that names this mail. When a new mail links two stored groups, they are merged – so the result does not depend on the order in which mails arrive. Subjects alone never join mails.
- Older databases are regrouped once at start (`kv_meta.thread_version`).
- `messages.conversation` returns a conversation newest first, one entry per Message-ID (the copy outside trash/junk), with a short snippet; list rows carry `threadCount`.
- *Summarize* and *Draft* send up to 20 mails of the conversation in reading order to the model.

## Security

- **Transport:** IMAP and SMTP only over TLS or a required STARTTLS upgrade; plain text only to `localhost`. Login with SASL PLAIN (UTF-8 passwords), falling back to LOGIN.
- **Remote content:** remote images are blocked by default for privacy and counted, not fetched; users can allow images once per email or per trusted sender (`app.allowedImageSenders`), and inline CID images are loaded strictly from local MIME parts.
- **Read-only:** with `mailbox.readOnly` on, `backend` refuses mail actions, drafts, sending and filter rules; the UI hides them and also respects the per-account write list. Adding an account switches read-only off; `"mailbox": {"readOnly": true}` in `config.json` switches it back on.
- **AI & MCP:** mail content is wrapped as untrusted data, and encrypted or locally decrypted S/MIME and OpenPGP emails are never sent to any AI backend (Inspector, Quick-Reply, Compose AI Assist, Copilot context snapshots, or Contact Enrichment). The in-app copilot only proposes drafts, events, mark-read, move/archive, and star actions; they run only after the user clicks *Execute*. The built-in local stdio MCP server (`postdive --mcp`) is read-only by default, never sends email or permanently deletes messages, and never decrypts S/MIME or OpenPGP messages. Endpoints come only from `config.json`; a key is sent only to its provider. CLI backends (`claude-cli`, `agy`, `opencode-cli`) run in a dedicated `0700` `<StateDir>/cli-sandbox` directory without unrestricted tool access.
- **Encrypted mails:** Full **S/MIME** (`.p12`/`.pfx` in OS secure store) AND native **OpenPGP** (`internal/pgp`, RFC 3156 PGP/MIME + MIME-decoded Inline PGP, passphrase-protected secret keys in OS secure store, SSRF-/key-binding-guarded WKD & `keys.openpgp.org` lookup) verify signatures, check signer-address mismatches, decrypt on open, and fail closed when signing/encrypting outgoing mail; scans, archive exports, AI backends, and copilot lookups never decrypt.
- **Attachments:** executables, scripts, installers and active HTML/SVG are only saved to `~/Downloads`, never opened.

## Theme

On Omarchy the app follows `~/.local/state/omarchy/current/theme/colors.toml` live; elsewhere one of 24 bundled palettes (default **Diver** / **Diver Light**) or the system's light/dark mode.

## Bar widget

`omarchy/BarWidget.qml` reads the triage state and shows the mails that need action.
