# omathunder Architecture

`omathunder` brings Omarchy-style live theming and multi-stage, proposal-based AI triage to Thunderbird on **Omarchy Linux**, **macOS**, and **Windows** — without breaking Thunderbird or requiring unsigned profile hacks.

## Core Design Principles

1. **Thunderbird stays untouched & intact:**
   - The Thunderbird extension is a WebExtension (`extension/manifest.json`) with two small **experiment APIs** where Thunderbird has no official API: `omaFilters` (read/create native message filters, open the filter dialog; it also registers the custom headers filters need in `mailnews.customHeaders`) and `omaCalendar` (Lightning/CalDAV calendars). Experiments need extra ATN review and must be re-checked on every Thunderbird ESR. The installer never edits `prefs.js`; changes inside Thunderbird (moves, tags, the `oma-*` status tags, filters, calendar entries) only happen through the add-on and only when read-only mode is off.
   - `install.sh` / `omathunder setup` only registers a per-user Native Messaging Host manifest (`dev.omathunder.host.json`).
   - `uninstall.sh` / `omathunder uninstall` cleanly removes all registered manifests, hooks, and `omathunder` state without modifying any Thunderbird mail data or user preferences.

2. **Live Omarchy Theme Sync (Chrome + Message Body + Compose):**
   - On **Omarchy Linux**, `omathunder` reads `~/.local/state/omarchy/current/theme/colors.toml` and watches changes live.
   - On **macOS / Windows**, `omathunder` embeds the curated Omarchy theme palettes (`catppuccin`, `tokyo-night`, `nord`, `kanagawa`, `gruvbox`, `rose-pine`, `everforest`, `matte-black`, `flexoki-light`, etc.) and can follow system dark/light appearance or manual selection.
   - The WebExtension applies themes immediately without restarting Thunderbird via:
     - `browser.theme.update()` for toolbars, tabs, sidebar, popups, and color-scheme hints;
     - `messageDisplayScripts` & `composeScripts` CSS custom properties (`--oma-*`) for consistent reading/composing surfaces.

3. **Three-Stage AI Pipeline (Beyond Single-Mail Popups):**
   - **Stage 0 — Deterministic Rules (0 tokens, <1 ms):** Filters newsletters (`List-Unsubscribe`), bulk/automated mail (`Auto-Submitted`, `Precedence: bulk`) and invoices locally in Go. Security and transaction mails (password resets, codes, orders) are never downgraded here.
   - **Stage 1 — Fast Structured Classifier:** Uses a fast/cheap tier (e.g., **Gemini Hydrogen / Flash**, **Claude Haiku**, or **local Ollama**) with stripped plaintext snippets (~1.5 KB max) and strict JSON output to assign `bucket` (`action`, `waiting`, `invoice`, `event`, `fyi`, `newsletter`), `urgency`, `summary`, and extracted `deadlines`/`amounts`.
   - **Stufe 2 — Deep Thread & Context Agent:** Uses **Claude Code CLI / Anthropic API**, **Gemini CLI / API**, **OpenCode**, or **Ollama** on demand with full thread history + cross-mail context.

4. **Strict Security Boundary (Untrusted Mail → Reviewable Proposals):**
   - All email content is wrapped and explicitly delimited as **untrusted data**.
   - AI output **never** sends emails, deletes messages, or modifies folders on its own. The only automatic change is the deterministic Stage-0 auto-move, and only under the rules in "Safety Model" below.
   - Every AI suggestion is emitted as a typed **Proposal** (`draft_reply`, `add_tags`, `move_folder`) stored in `omathunder`'s local state and applied in Thunderbird only on explicit user click.

## Safety Model

- **Feature flags:** `features.theme`, `features.assistant`, `features.triage` switch the three building blocks independently. The host refuses disabled methods; the extension blocks them as well.
- **Read-only (`mailbox.readOnly`, default on):** no moves, tags or folder creation. A scan returns `plannedMoves` as a dry run.
- **Account scope (`mailbox.triageAccountIds`):** only these accounts are scanned, classified and changed. Cross-account moves are rejected.
- **Auto-move planning lives in Go** (`internal/triage/automove.go`): INBOX only, sender-domain match only, optionally unread only, never out of a rule's folder tree, never when `Authentication-Results` records `dkim=fail` or `dmarc=fail`. The extension re-checks each move against the live mailbox before executing it.
- **Message identity:** Thunderbird's numeric message IDs are session-scoped. Stored references are resolved by RFC Message-ID; ambiguous matches are refused before any write.
- **Single write path:** all mailbox changes go through the `mailbox` helpers in `background.js`, guarded by `requireWritable` / `requireMovable` from `extension/lib/core.js` (unit-tested in `tests/core.test.js`).
- **Thread context** for the assistant is built from `References` / `In-Reply-To`, not from matching subjects.
- **CLI backends:** `claude` runs with `--tools ""`, `--strict-mcp-config` and `--disallowedTools "mcp__*"` and gets the prompt on stdin. `gemini-cli` / `opencode-cli` cannot disable tools and need `ai.allowUnrestrictedCli`.

## Local Bridge & Native App

- **Opt-in:** `bridge.enabled` is false by default. `bridge.Supervisor` (started by the host) polls `config.json` every 2 s and right after every config write, opens `127.0.0.1:<port>` when the bridge gets enabled and closes it when it gets disabled. The discovery file `bridge.json` (`0600`, token + URL) is removed on shutdown only if it still belongs to this host, so a second Thunderbird profile keeps its own.
- **Reverse RPC:** HTTP handlers in `internal/bridge` call `RPCBroker.Call`, which sends a `tb_rpc` frame to the extension; `dispatchThunderbirdRPC` in `background.js` answers with `tb_rpc_result`. The extension reconnects the native port with backoff, so the bridge comes back after a host restart.
- **One policy table:** every RPC method has an entry in `extension/lib/core.js` (read / write / send / filter / calendar / contacts / ui) that is checked centrally before the handler runs, plus per-object checks (account of the mail, of the target folder, of the sending identity). Reads are limited by `bridge.accountIds` (empty = all), writes need read-only off and an account in `mailbox.triageAccountIds`, sending additionally `bridge.allowSend`. Bridge filters cannot use the `delete` action. Go re-checks read-only, send and assistant gates as a second layer.
- **Errors** travel as `code: detail` (`read_only`, `account_not_enabled`, `not_found`, `ambiguous`, `message_changed`, …) and are mapped to HTTP status codes. A mutating call that times out returns `504 outcome_unknown`; compose calls carry an `idempotencyKey` so a retry cannot send twice.
- **Events:** the extension batches mail/calendar changes (~500 ms), filters them by readable account and forwards them as `tb_event`; `EventHub` fans them out to SSE subscribers. Calendar events carry only IDs.
- **Native app** (`cmd/omathunder-ui`, `internal/nativeui`, `internal/copilot`): a separate binary built with mygo, so the host stays free of GUI code. A connection manager re-reads `bridge.json` when the stream ends and swaps in the new port/token. Background work only touches UI state through `emitUpdate` on the UI thread.
- **Copilot:** tool calls that change anything (drafts, calendar entries, mark read) are returned as proposals and executed only after the user confirms; mail and tool output are wrapped as untrusted data.
- **AI providers:** one registry in `internal/ai` maps case-insensitive backend names to one implementation, default model and timeout for both the classify/analyze path and the chat path. Endpoints come only from `config.json`; an API key is only sent to its provider's official host or the endpoint configured for that provider.

## Writing Assistant

- Entry points: `compose_action` popup (`ui/compose.html`) on the draft being written, `message_display_action` popup on the open mail, and the OmaThunder tab.
- Modes handled by `analyze_thread`: `summarize`, `draft_reply`, `review_draft`, `improve_draft`, `compose_new`. Compose-window requests are `ephemeral`: nothing is stored as a proposal.
- Drafts are split into the user's own text and the rest (signature, quote) by `splitDraft` in `extension/lib/core.js`. Only the own part is sent to the model and only the own part is replaced; inserting requires an explicit click.
- Reply proposals open a normal reply window (`beginReply`, optionally reply-all) and write the text above the quote.

## Versioning

`ping` returns the host version and `ProtocolVersion`. The extension compares it with its own `PROTOCOL` constant and shows a warning when they differ. Bump both whenever the messages change incompatibly.

## Runtime Notes

- The host handles each request in its own goroutine, so a long AI call does not block the UI. Config writes are serialized.
- The theme watcher polls once per second; the macOS/GNOME appearance lookup is cached for 5 seconds.
- The board keeps at most 400 mails (newest first, manually placed cards last to go) so responses stay below Thunderbird's 1 MB limit for host messages.

## Component Map

```
┌──────────────────────────────────┐   4-byte LE JSON (stdio)    ┌────────────────────────────────┐
│  Thunderbird WebExtension        │ ◄─────────────────────────► │  omathunder (host + CLI)       │
│  - theme.update()                │   requests / theme events   │  - internal/theme (watcher,    │
│  - messageDisplay/compose CSS    │   tb_rpc ◄─► tb_rpc_result  │    22 presets)                 │
│  - spacesToolbar Triage UI       │   tb_event ──►              │  - internal/triage (Stage 0)   │
│  - policy table (lib/core.js)    │                             │  - internal/ai (provider       │
│  - omaFilters / omaCalendar      │                             │    registry, any-llm, CLIs)    │
│    experiment APIs               │                             │  - internal/bridge (opt-in)    │
└──────────────────────────────────┘                             └──────┬─────────────────┬───────┘
                                                                        │ JSON cache      │ 127.0.0.1 HTTP + SSE
                                                       ┌────────────────▼──────────┐  ┌───▼──────────────────────────┐
                                                       │ Omarchy Quickshell Plugin │  │ omathunder-ui / OmaThunder.app│
                                                       │ (omarchy/BarWidget.qml)   │  │ (nativeui, copilot) & scripts │
                                                       └───────────────────────────┘  └──────────────────────────────┘
```
