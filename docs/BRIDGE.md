# OmaThunder Bridge API (Mail + Calendar + Contacts + Filters + Multi-LLM)

The OmaThunder helper (`omathunder`, started by Thunderbird as Native Messaging host) can expose Thunderbird – mail, compose, folders, contacts, message filters and Lightning/CalDAV calendars – plus its AI layer over an authenticated **localhost HTTP + SSE bridge**. The native OmaThunder app (`omathunder-ui`, on macOS `OmaThunder.app`) and your own scripts use it, so they never need IMAP, SMTP, OAuth2 or CalDAV themselves.

```
omathunder-ui / scripts
   │  HTTP + SSE (127.0.0.1, Bearer token)
   ▼
omathunder host (Go)                          ← started by Thunderbird
   ├── /v1/ai/*        provider registry (any-llm-go + CLI backends)
   ├── /v1/triage/*    local triage store
   └── reverse RPC + EventHub  (tb_rpc / tb_rpc_result / tb_event over stdio)
          │
          ▼
Thunderbird WebExtension (background.js)
   ├── policy table (lib/core.js) – checked before every call
   ├── accounts, folders, messages, compose, contacts
   └── omaFilters / omaCalendar experiment APIs
```

## Switching it on

The bridge is **off by default** (`bridge.enabled = false`). Enable it with any of:

- the **Enable bridge** button in the native app,
- the add-on settings page (*Local bridge for the OmaThunder app*),
- `omathunder bridge enable` (and `omathunder bridge disable`).

The host checks `config.json` every two seconds and right after every settings change, and opens or closes the port accordingly. It only runs while Thunderbird runs with the add-on connected.

## Discovery & Authentication

While enabled, the bridge listens on `127.0.0.1` (ephemeral port, or `bridge.port`) and writes a `0600` discovery file:

- **macOS:** `~/Library/Caches/omathunder/bridge.json`
- **Linux:** `${XDG_STATE_HOME:-~/.local/state}/omathunder/bridge.json`
- **Windows:** `%LOCALAPPDATA%\omathunder\bridge.json`

```bash
omathunder bridge status     # running?, URL, PID, health
omathunder bridge path       # where bridge.json lives
```

```json
{
  "version": "0.2.9",
  "protocol": 1,
  "url": "http://127.0.0.1:54321",
  "port": 54321,
  "token": "<64-hex-char-256-bit-secret>",
  "pid": 12345,
  "startedAt": "2026-11-18T02:00:00Z"
}
```

Send `Authorization: Bearer <token>` with every request. For browser `EventSource` connections on `GET /v1/events`, `?token=<token>` is also accepted.

**Port and token change on every start** (Thunderbird restart, add-on reconnect, bridge re-enabled). Clients must re-read `bridge.json` when a request fails with a connection error or the event stream ends. With several Thunderbird profiles each host writes the file when it starts; on shutdown a host removes it only if it is still its own.

## Safety model

| Area | Rule |
|---|---|
| **Network** | `127.0.0.1` only, Bearer token required on every endpoint. No CORS headers, so web pages cannot read responses. |
| **Reading** | All accounts, unless `bridge.accountIds` lists the readable ones. Messages, folders, threads and events of other accounts are rejected (`account_not_readable`) or filtered out. |
| **Mail writes** (`messages/{id}/actions`, silent drafts, filters) | Need `mailbox.readOnly = false` **and** the account in `mailbox.triageAccountIds` (and in `bridge.accountIds`, if set). Moves stay inside one account. |
| **Sending** (`compose/send`) | Additionally needs `bridge.allowSend = true`. The check uses the account that owns the **identity actually used**, not a caller-supplied account. |
| **Filters** | Same as mail writes. Filters created through the bridge can never use the `delete` action, are appended after your own filters, and never overwrite an existing filter unless you explicitly use `mode: "replace"`. |
| **Calendar & contacts writes** | Need `mailbox.readOnly = false`. Read-only calendars are rejected. |
| **AI** | `/v1/ai/*` needs `features.assistant`. Backend names are case-insensitive; unknown names are rejected instead of falling back to another provider. Endpoints come only from `config.json` – a request's `endpoint` field is ignored – and an API key is only sent to its provider's official host or the endpoint configured for that provider. |
| **Unrestricted CLIs** | `gemini-cli`, `opencode-cli` stay blocked unless `ai.allowUnrestrictedCli = true`. |

The extension enforces these rules centrally (one policy table in `extension/lib/core.js`); the Go side re-checks read-only, send and assistant gates.

## Responses & errors

JSON responses are `{ "ok": true, "result": ... }` or `{ "ok": false, "error": "code: detail" }`.

| Error code | HTTP | Meaning |
|---|---|---|
| `read_only`, `account_not_enabled`, `account_not_readable`, `send_disabled`, `feature_disabled:<name>`, `cross_account_move` | 403 | Blocked by the safety settings. |
| `invalid_argument` | 400 | Bad or missing parameter (e.g. calendar `end` before `start`, filter without conditions). |
| `not_found` | 404 | Message, folder, filter, calendar or event does not exist. |
| `ambiguous`, `message_changed`, `conflict` | 409 | The Message-ID matches several copies, the numeric ID now points to a different mail (Thunderbird restarted), or a name clash. |
| `outcome_unknown` | 504 | A changing call did not finish in time. It may still have been applied – check before retrying. |
| – | 503 | Thunderbird / the add-on is not connected. |

Compose calls accept an `idempotencyKey`. Repeating a call with the same key within 15 minutes returns the first result instead of sending or saving twice – use it whenever you retry.

## Endpoints

### Health & live events

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/health` | Version, protocol, `thunderbirdConnected`, `readOnly`, bridge settings, active AI config. |
| `GET` | `/v1/events` | SSE stream (see below). |

### Mail

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/accounts` | Mail accounts and identities (readable accounts only). |
| `GET` | `/v1/folders` | Folders (`?accountId=...`). |
| `GET` | `/v1/messages` | Search/list headers (`?accountId=&folderId=&q=&unread=true\|false&days=30&limit=50&cursor=0`). `q` supports `from:`, `to:`, `subject:`. Each header has `id` (session-scoped numeric ID) and `messageId` (RFC Message-ID). |
| `GET` | `/v1/messages/{id}` | Full message, headers, attachments (`?format=text\|html\|both`). `{id}` is a numeric ID or a Message-ID; optional `expectedMessageId`, `folderId`, `accountId` disambiguate. |
| `GET` | `/v1/messages/{id}/thread` | Messages linked via `References` / `In-Reply-To` (`?limit=20`, max 50). |
| `POST` | `/v1/messages/{id}/actions` | `{ "expectedMessageId": "...", "read": true, "flagged": false, "addTags": [], "removeTags": [], "moveToFolderId": "...", "moveToPath": "...", "trash": false, "archive": false }`. Send `expectedMessageId` whenever you stored the numeric ID earlier: a mismatch returns `409 message_changed` instead of touching another mail. At most one of `trash`, `archive` and `moveToFolderId`/`moveToPath` per request (else `400 invalid_argument`). Moves stay inside the mail's account (`403 cross_account_move`); `archive` uses Thunderbird's own archive settings of the identity (folder, yearly/monthly layout) and returns an empty `newId`. |
| `GET` | `/v1/triage/state` | Kanban classifications, bar summary and proposals from the local store. |

### Compose

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/v1/compose/draft` | Reply or new draft (`to`, `cc`, `bcc`, `subject`, `body`, `isHtml`, `replyToMessageId`, `replyType`, `identityId`, `openWindow`, `idempotencyKey`). `openWindow: true` only opens a compose window for you to review; saving silently needs write permission. |
| `POST` | `/v1/compose/send` | Send through Thunderbird (needs read-only off and `bridge.allowSend`). Same fields plus `idempotencyKey`. |

### Calendar (`omaCalendar`)

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/calendars` | Calendars (`id`, `name`, `type`, `readOnly`, `color`). |
| `GET` | `/v1/calendar/events` | Events in a range (`?start=ISO8601&end=ISO8601&calendarId=...&limit=100`). Recurring series are expanded; each occurrence has `isRecurring` and `recurrenceId`. |
| `POST` | `/v1/calendar/events` | Create (`calendarId`, `title`, `start`, `end`, `allDay`, `location`, `description`). For all-day events `end` is exclusive (the day after the last day). |
| `PATCH` | `/v1/calendar/events/{calendarId}/{eventId}` | Update fields. For a recurring event pass `recurrenceId` and `scope` (`occurrence` – default when `recurrenceId` is set – or `series`). |
| `DELETE` | `/v1/calendar/events/{calendarId}/{eventId}` | Delete. `?recurrenceId=...&scope=occurrence` removes only that occurrence; without `recurrenceId` the whole event/series is deleted. |

### Contacts

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/contacts` | Search address books (`?q=...`). |
| `POST` | `/v1/contacts/upsert` | Create or enrich a contact (needs read-only off). |

### Message filters (`omaFilters`)

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/filters` | Native filters (`?accountId=...`). |
| `POST` | `/v1/filters/upsert` | `mode`: `create` (always a new filter; an existing name gets a suffix like "Name (2)"; appended at the end), `replace` (overwrite `existingName`), `append_conditions`, `toggle`, `fix_match_all`. At least one non-empty condition is required; `delete` actions are rejected; target folders must be in the same account. |
| `POST` | `/v1/filters/run` | Run filters on an account's inbox (`accountId`, `filterNames`). Same permissions as writes. |
| `POST` | `/v1/filters/open` | Open Thunderbird's filter dialog. Editing, ordering and deleting filters happens there. |

### AI (`any-llm-go` + CLIs)

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/ai/providers` | Supported backends + active `agent`/`classifier` config. `?models=1&backend=...` lists models of a backend; its endpoint comes from config (`ai.agent`, `ai.classifier` or the per-backend map `ai.endpoints`, e.g. `{"ollama": "http://127.0.0.1:11434"}`). |
| `POST` | `/v1/ai/chat` | Chat completion with optional OpenAI-style `tools` and SSE streaming (`"stream": true`). `backend`/`model` may override the configured agent; `endpoint` is ignored. |
| `POST` | `/v1/ai/analyze` | Mail/thread assistant (`summarize`, `draft_reply`, `review_draft`, `extract_events`, …). |

Backends (case-insensitive; aliases are rewritten to the canonical name when the config is loaded):

- **API (via `any-llm-go`):** `gemini-api` (alias `gemini`), `claude-api` (alias `anthropic`), `openai`, `openaicompat` (needs an endpoint in config; uses `OPENAICOMPAT_API_KEY`, never `OPENAI_API_KEY`), `ollama`, `deepseek`, `groq`, `mistral`, `llamacpp`, `llamafile`, `zai`
- **Local CLIs:** `claude-cli`, `gemini-cli`, `opencode-cli` – content always goes via stdin, never as a process argument.

## Event stream (`GET /v1/events`)

The first event is `connected` (`{ "thunderbirdConnected": true, "version": "..." }`), followed by `: keepalive` comments every 25 s. Every other event's `data:` line is a wrapper:

```json
{ "event": "mail.new", "data": { ... }, "timestamp": "2026-11-18T08:00:00Z" }
```

| Event | `data` |
|---|---|
| `mail.new` | `{ accountId, folderId, folder, count, messages: [{ id, messageId, subject, from, date, accountId, folderId, unread }] }` (max 20 messages) |
| `mail.updated` | `{ ids, accountIds, truncated }` – batched (~500 ms), max 500 IDs |
| `mail.deleted` | `{ ids, accountIds, truncated }` |
| `mail.moved` | `{ oldIds, ids, fromFolderIds, toFolderIds, accountIds, truncated }` |
| `calendar.changed` | `{ calendarId, ids }` – IDs only; fetch details via `/v1/calendar/events` |
| `theme_updated` | active theme palette |

Events only cover readable accounts: a mail moved out of a readable account arrives as `mail.deleted`, one moved into a readable account as `mail.updated`. `mail.new` is sent immediately (not batched). When Thunderbird or the add-on goes away, the stream ends; reconnect after re-reading `bridge.json`.

While the bridge is disabled, the add-on answers every bridge call except `ping` with `feature_disabled:bridge`.

## Native app & Go SDK

`omathunder ui` starts the native app (`cmd/omathunder-ui`: main window, menu bar/tray widget, desktop notifications), built with [mygo](https://github.com/egoist/mygo). It is a separate binary, so the Native Messaging host carries no GUI code.

```bash
omathunder ui              # main window + menu bar / tray widget
omathunder ui --minimized  # start in the menu bar / tray only
omathunder ui --no-tray    # main window only (the app quits when the window closes)
```

Go programs inside this module can use `internal/bridgeclient`: `bridgeclient.Discover("")` returns a typed client for all endpoints above.
