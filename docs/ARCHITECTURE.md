# MailDiver Architecture

MailDiver is a native desktop mail client with calendar, contacts and an AI copilot, on **Omarchy Linux** and **macOS** (Windows and Android builds exist but are untested). It talks to mail servers directly over IMAP and SMTP and keeps a local SQLite copy. Everything runs in one Go process: no Thunderbird, no browser engine, no local server, no sockets between its parts.

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
| `mailengine` | Accounts and sync (one IDLE worker per account on INBOX, 45 s polling where the server has no IDLE, folder list every 15 min), local store, message actions written back to the server, sending, calendars (local, Google, iCal every 5 min), contacts, filter rules, attachments, Thunderbird profile import (read-only). |
| `backend` | The API the UI, copilot and jobs use. `Engine.Call` takes JSON-shaped parameters, so the method set stays the same one the engine tests exercise. Engine errors arrive as `code: detail` and become `*backend.APIError`; a mutating call that times out becomes `outcome_unknown`; compose carries an idempotency key. |
| `events` | In-process fan-out of engine events. Subscribers get a buffered channel; a slow subscriber drops events instead of blocking the engine. |
| `jobs`, `triage`, `archive` | Triage classification (local rules first, then the model within `ai.maxAiCallsPerScan`), result in `triage-state.json`; archive runs write `.eml` files, check SHA-256 and only then move or delete. |
| `ai`, `copilot` | Provider registry (API, local servers, CLIs) and the copilot agent. |
| `nativeui` | The app. It touches UI state only on its UI thread (`emitUpdate`); network and disk work run in goroutines. |
| `config`, `secretstore`, `theme` | `config.json`, the API-key env file (`0600`), passwords (macOS Keychain, elsewhere a `0600` file), Omarchy/bundled themes. |

## Where data lives

| | macOS | Linux |
|---|---|---|
| `config.json`, `env` (API keys) | `~/Library/Application Support/maildiver/` | `~/.config/maildiver/` |
| `maildiver.db` (mail, calendars, contacts), `triage-state.json` | `~/Library/Caches/maildiver/` | `~/.local/state/maildiver/` |
| Passwords, OAuth tokens | Keychain | `~/.config/maildiver/secrets.json` (`0600`) |

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
- **Encrypted mails:** OpenPGP/S/MIME are not decrypted yet. The rule for when they are: explicit user actions on the open mail may decrypt and say so; scans, archive runs and copilot lookups never decrypt.
- **Attachments:** executables, scripts, installers and active HTML/SVG are only saved to `~/Downloads`, never opened.

## Theme

On Omarchy the app follows `~/.local/state/omarchy/current/theme/colors.toml` live; elsewhere one of 22 bundled palettes or the system's light/dark mode.

## Bar widget

`omarchy/BarWidget.qml` reads the triage state and shows the mails that need action.
