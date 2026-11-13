# omathunder Architecture

`omathunder` brings Omarchy-style live theming and multi-stage, proposal-based AI triage to Thunderbird on **Omarchy Linux**, **macOS**, and **Windows** — without breaking Thunderbird or requiring unsigned profile hacks.

## Core Design Principles

1. **Thunderbird stays untouched & intact:**
   - The Thunderbird extension is a pure, ATN-compatible WebExtension (`extension/manifest.json`) with **zero** `experiment_apis` and **zero** destructive `prefs.js` mutations.
   - `install.sh` / `omathunder setup` only registers a per-user Native Messaging Host manifest (`dev.omathunder.host.json`).
   - `uninstall.sh` / `omathunder uninstall` cleanly removes all registered manifests, hooks, and `omathunder` state without modifying any Thunderbird mail data or user preferences.

2. **Live Omarchy Theme Sync (Chrome + Message Body + Compose):**
   - On **Omarchy Linux**, `omathunder` reads `~/.local/state/omarchy/current/theme/colors.toml` and watches changes live.
   - On **macOS / Windows**, `omathunder` embeds the curated Omarchy theme palettes (`catppuccin`, `tokyo-night`, `nord`, `kanagawa`, `gruvbox`, `rose-pine`, `everforest`, `matte-black`, `flexoki-light`, etc.) and can follow system dark/light appearance or manual selection.
   - The WebExtension applies themes immediately without restarting Thunderbird via:
     - `browser.theme.update()` for toolbars, tabs, sidebar, popups, and color-scheme hints;
     - `messageDisplayScripts` & `composeScripts` CSS custom properties (`--oma-*`) for consistent reading/composing surfaces.

3. **Three-Stage AI Pipeline (Beyond Single-Mail Popups):**
   - **Stage 0 — Deterministic Rules (0 tokens, <1 ms):** Filters newsletters (`List-Unsubscribe`), bulk/automated mail (`Auto-Submitted`, `Precedence: bulk`), and already-replied threads locally in Go.
   - **Stage 1 — Fast Structured Classifier:** Uses a fast/cheap tier (e.g., **Gemini Hydrogen / Flash**, **Claude Haiku**, or **local Ollama**) with stripped plaintext snippets (~1.5 KB max) and strict JSON output to assign `bucket` (`action`, `waiting`, `invoice`, `event`, `fyi`, `newsletter`), `urgency`, `summary`, and extracted `deadlines`/`amounts`.
   - **Stufe 2 — Deep Thread & Context Agent:** Uses **Claude Code CLI / Anthropic API**, **Gemini CLI / API**, **OpenCode**, or **Ollama** on demand with full thread history + cross-mail context.

4. **Strict Security Boundary (Untrusted Mail → Reviewable Proposals):**
   - All email content is wrapped and explicitly delimited as **untrusted data**.
   - Agents **never** send emails, delete messages, or modify folders autonomously.
   - Every AI suggestion is emitted as a typed **Proposal** (`draft_reply`, `add_tags`, `calendar_event`, `followup_reminder`) stored in `omathunder`'s local state and applied in Thunderbird only on explicit user click.

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
