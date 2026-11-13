# omathunder Architecture

`omathunder` brings Omarchy-style live theming and multi-stage, proposal-based AI triage to Thunderbird on **Omarchy Linux**, **macOS**, and **Windows** — without breaking Thunderbird or requiring unsigned profile hacks.

## Core Design Principles

1. **Thunderbird stays untouched & intact:**
   - The Thunderbird extension is a pure, ATN-compatible WebExtension (`extension/manifest.json`) with **zero** `experiment_apis`. The installer never edits `prefs.js`; changes inside Thunderbird (moves, tags, the `oma-*` status tags) only happen through the official add-on APIs and only when read-only mode is off.
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
- **Auto-move planning lives in Go** (`internal/triage/automove.go`): INBOX only, sender-domain match only, optionally unread only, never out of a rule's folder tree. The extension re-checks each move against the live mailbox before executing it.
- **Message identity:** Thunderbird's numeric message IDs are session-scoped. Stored references are resolved by RFC Message-ID; ambiguous matches are refused before any write.
- **Single write path:** all mailbox changes go through the `mailbox` helpers in `background.js`, guarded by `requireWritable` / `requireMovable` from `extension/lib/core.js` (unit-tested in `tests/core.test.js`).
- **Thread context** for the assistant is built from `References` / `In-Reply-To`, not from matching subjects.
- **CLI backends:** `claude` runs with `--tools ""` and gets the prompt on stdin. `gemini-cli` / `opencode-cli` cannot disable tools and need `ai.allowUnrestrictedCli`.

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
┌──────────────────────────────────┐       4-byte LE JSON        ┌────────────────────────────────┐
│  Thunderbird WebExtension        │ ◄─────── (stdio) ─────────► │  Go Binary: omathunder host    │
│  - theme.update()                │                             │  - internal/theme (colors.toml │
│  - messageDisplay/compose CSS    │                             │    watcher + 22 presets)       │
│  - spacesToolbar Triage UI       │                             │  - internal/triage (Stage 0)   │
│  - Action & Proposal executor    │                             │  - internal/ai (Gemini/Claude/ │
└──────────────────────────────────┘                             │    OpenCode/Ollama + Proposals)│
                                                                 └───────────────┬────────────────┘
                                                                                 │ JSON cache
                                                                 ┌───────────────▼────────────────┐
                                                                 │  Omarchy Quickshell Plugin     │
                                                                 │  (omarchy/BarWidget.qml)       │
                                                                 └────────────────────────────────┘
```
