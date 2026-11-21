# MailDiver Architecture

MailDiver is a native desktop & mobile mail client with calendar, contacts and an AI copilot, actively used on **macOS** and **Android**, with native builds for **Linux** and **Windows**. It talks to mail servers directly over IMAP, SMTP, CalDAV and CardDAV and keeps a local SQLite copy. Everything runs in one Go process: no Thunderbird, no external browser engine, no local bridge server, no sockets between its parts.

## One process, four layers

```
cmd/maildiver
   │
   ├─ internal/mailengine   IMAP (IDLE) · SMTP · SQLite + FTS5 · Google Calendar/Contacts · iCal · Thunderbird import
   │        │  Engine.Call(method, params)          events.Hub (mail.new, mail.sync, calendar.changed, …)
   │        ▼                                                │
   ├─ internal/backend      typed client: errors with codes, read-only and assistant gates, idempotency keys
   │        │                                                │
   │        ├─ internal/jobs      triage scans, archive & cleanup (Engine.CallHost "host.*")
   │        ├─ internal/copilot   chat agent with mail/calendar tools; only proposes changes
   │        ▼                                                ▼
   └─ internal/nativeui     mygo UI: folders, list, reading pane, conversations, compose, calendar, contacts, settings
```

| Package | Job |
|---|---|
| `mailengine` | Accounts and sync (IMAP IDLE on INBOX with context socket deadlines, 45 s polling fallback where IDLE is missing, folder sync, Offline Outbox retry queue), local store, message actions written back to the server, sending, calendars (local, Google Calendar, two-way CalDAV, iCal feeds), contacts (local, Google Contacts, two-way CardDAV), filter rules, attachments, Thunderbird profile import (read-only). |
| `backend` | The API the UI, copilot and jobs use. `Engine.Call` takes JSON-shaped parameters, so the method set stays the same one the engine tests exercise. Engine errors arrive as `code: detail` and become `*backend.APIError`; a mutating call that times out becomes `outcome_unknown`; compose carries an idempotency key and returns outbox/security status. |
| `events` | In-process fan-out of engine events. Subscribers get a buffered channel; a slow subscriber drops events instead of blocking the engine. |
| `jobs`, `triage`, `archive` | Triage classification (local rules first, then the model within `ai.maxAiCallsPerScan`), result in `triage-state.json`; archive runs write `.eml` files, maintain `.maildiver-index.json`, check SHA-256 and only then delete. |
| `ai`, `copilot` | Provider registry (API, local servers such as Ollama/llama.cpp, CLIs) and the copilot agent. |
| `nativeui` | The app. It touches UI state only on its UI thread (`emitUpdate`); network and disk work run in goroutines; HTML WYSIWYG compose & optional Web Preview embed platform `mygo.WebView`. |
| `config`, `secretstore`, `theme` | `config.json`, the API-key env file (`0600`), encrypted secrets (macOS Keychain, Android Keystore, Windows DPAPI, Linux libsecret/AES-256-GCM sealed store), Omarchy/bundled themes. |

## Where data lives

| | macOS | Linux |
|---|---|---|
| `config.json`, `env` (API keys) | `~/Library/Application Support/maildiver/` | `~/.config/maildiver/` |
| `maildiver.db` (mail, calendars, contacts), `triage-state.json` | `~/Library/Caches/maildiver/` | `~/.local/state/maildiver/` |
| Passwords, OAuth tokens, S/MIME keys | Keychain | `~/.config/maildiver/secrets.seal` (libsecret / AES-256-GCM) |

`XDG_CONFIG_HOME` / `XDG_STATE_HOME` override the directories. Config files never hold secrets.

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
- **Remote content:** the reader never loads anything from the network; HTML is converted to Markdown, images are counted, not fetched.
- **Read-only:** with `mailbox.readOnly` on, `backend` refuses mail actions, drafts, sending and filter rules; the UI hides them and also respects the per-account write list. Adding an account switches read-only off; `"mailbox": {"readOnly": true}` in `config.json` switches it back on.
- **AI:** mail content is wrapped as untrusted data. The copilot only proposes drafts, events and "mark read"; they run after the user clicks *Execute*. Endpoints come only from `config.json`; a key is sent only to its provider. `claude-cli` runs without tools and gets the prompt on stdin.
- **Encrypted mails:** **S/MIME** certificates (`.p12`/`.pfx` in OS secure store) verify signatures, check signer-address mismatches, decrypt on open, and sign/encrypt outgoing mails; **OpenPGP (PGP/MIME)** detection displays a Phase 2 status notice (full OpenPGP arrives in Phase 2); scans, archive exports, and copilot lookups never decrypt.
- **Attachments:** executables, scripts, installers and active HTML/SVG are only saved to `~/Downloads`, never opened.

## Theme

On Omarchy the app follows `~/.local/state/omarchy/current/theme/colors.toml` live; elsewhere one of 22 bundled palettes or the system's light/dark mode.

## Bar widget

`omarchy/BarWidget.qml` reads the triage state and shows the mails that need action.
