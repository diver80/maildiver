# AGENTS.md — PostDive Contributor & Agent Guide

Operating rules and hard invariants for coding agents working in this repository (`diver80/postdive`).
For subsystem architecture, see `docs/ARCHITECTURE.md`.

## 1. Language & Documentation Rules
- **Code, comments, commit messages, PRs, `CHANGELOG.md`, `docs/*.md`, `AGENTS.md`:** English.
- **Bilingual (EN + DE):**
  - `README.md` (GitHub landing page keeps both EN and DE sections/hints).
  - `docs/index.html` (GitHub Pages site supports `?lang=en` and `?lang=de`).
  - In-app UI strings: always bilingual via `a.tr(de, en)` / `a.trf(de, en, ...)`.
- **Internal / local notes (e.g. `HANDOFF.md`, internal wiki pages):** match the user's language (usually German); keep `HANDOFF.md` local and untracked.
- **User responses:** concise German by default unless the user writes in English.

## 2. Hard Security & Privacy Invariants
- **Never touch production user data:** Never read, modify, or overwrite `~/Library/Application Support/postdive/config.json`, production SQLite DBs, or OS Keychain entries during tests or debugging. Tests rely on the `testing.Testing()` sandbox in `internal/config/config.go`; still verify `config.json` is untouched after sensitive changes.
- **Never print secrets:** Never output passwords, OAuth tokens, API keys, or private keys in logs, errors, tests, or chat. Account diagnostic logs (`internal/mailengine/diaglog.go`) must always scrub secrets (`diagSecretRe`) and keep `PasswordMeta` secret-free.
- **Encrypted mail boundaries (S/MIME & OpenPGP):**
  - Never send encrypted or decrypted mail content/attachments to any AI backend (Inspector, Compose AI Assist, Copilot context snapshots, Triage, or Contact Enrichment).
  - Never silently save an encrypted or decrypted compose session (`req.Encrypt || composeHasDecrypted`) as an unencrypted IMAP draft on send failure or undo-send flush.
  - Contact S/MIME certificates (`CacheContactCertFor`) may only be auto-cached from trusted, valid signatures matching the `From` address, and must never overwrite an existing cert with a different fingerprint.
  - MCP (`postdive --mcp`) never decrypts S/MIME/OpenPGP, never sends email, and never permanently deletes.
- **Read-only default:** `config.Default()` sets `Mailbox.ReadOnly = true`. Tests that expect mailbox or calendar write side effects must explicitly set `cfg.Mailbox.ReadOnly = false`.
- **AI CLI sandboxing:** CLI backends (`claude-cli`, `agy`, `opencode-cli`) must run from the dedicated `0700` `<StateDir>/cli-sandbox` directory (never `$HOME`), with sanitized `TMPDIR`, bounded stdout/stderr buffers, prompt-fence escaping, and tool restrictions (`--sandbox` + denied tools for `agy`).

## 3. Concurrency, UI & Engine Rules
- **UI-thread discipline (`internal/nativeui`):** All `App` UI state is owned by the UI thread. Background goroutines must snapshot needed state before spawning and mutate UI state only inside `a.emitUpdate(func() { ... })`.
- **No blocking calls on the UI thread:** Network calls, IMAP/SMTP/DAV I/O, AI requests, and webview `ReadContent` calls must run off the UI thread with sequence/generation guards (`seq`) against stale callbacks.
- **Race-free headless UI tests:** Any test in `internal/nativeui` that triggers async callbacks (`RefreshAll`, `LoadMessages`, etc.) must register `app.SetHooks(...)` so `emitUpdate` does not race with `ui.NewTester` frames under `go test -race`.
- **SQLite (`internal/mailengine/store.go`):** Single-writer pool (`MaxOpenConns(1)`). Prefer indexed queries (e.g. `message_id IN (?, '<' || ? || '>')` to hit `idx_messages_msgid` instead of `trim(message_id, ...)`).
- **Calendar & dates:** Parse naive local dates/times with `time.ParseInLocation(..., time.Local)` (never UTC); keep all-day `End` exclusive per RFC 5545; avoid `.UTC()` on local test cursors (breaks between 00:00 and 01:00 CET).

## 4. Supported Platforms & Cross-Platform Rules
PostDive targets four platforms from one pure-Go (`CGO_ENABLED=0` on Desktop) codebase via `mygo`:
- **macOS (primary daily driver):** Universal `.app`/`.dmg`/`.zip` (`darwin/arm64` + `darwin/amd64`), ad-hoc signed, OS Keychain (chunked for large S/MIME/PGP blobs), `UNUserNotificationCenter` status via `purego`. Remember that apps launched from Finder/Dock do **not** inherit shell `PATH` — always check standard Homebrew (`/opt/homebrew/bin`, `/usr/local/bin`) and `~/.local/bin` paths when locating CLIs.
- **Linux (`amd64` + `arm64`):** Wayland/Hyprland + Omarchy live theme sync (`omarchy/BarWidget.qml`), `libsecret`/`libnotify`, AUR package (`packaging/aur/postdive-bin/`), `webview: false` in `mygo.json` so builds stay zero-CGO.
- **Windows (`amd64` + `arm64`):** Pure Go (`CGO_ENABLED=0`), avoid POSIX-only assumptions (path separators, `0600` semantics, shell tools).
- **Android (`android/arm64`, `minSdk 26`):** Built via `mygo` + Android NDK; phone UI lives in `internal/nativeui/phone.go` (44pt touch targets, swipe actions, phone router, Android Keystore, WorkManager background sync, 3s wake-up offline debounce).
- **Cross-platform invariants:**
  - Keep **Desktop & Phone UI parity**: new mail, search, activity, calendar, or contact behaviors must also work cleanly in `internal/nativeui/phone.go` (no desktop-only hover/keyboard assumptions).
  - Guard OS-specific code with explicit `//go:build` tags (note `android` builds have `GOOS=android`, and `linux && !android` / `darwin && !ios` exclusions — see `internal/secretstore/store_*.go` and `internal/nativeui/notify_settings_*.go`).
  - When touching OS-specific files or syscalls, verify cross-compilation (`CGO_ENABLED=0 GOOS=linux go vet ./... && CGO_ENABLED=0 GOOS=windows go vet ./...`).

## 5. Verification Before Finishing
Always run after code changes:
```bash
go vet ./... && go test -race -count=1 ./...
```
Do not claim a fix is complete until `go vet` and the full `-race` suite pass cleanly.

## 6. Release & Local macOS Install Workflow
Only commit, push, or release when explicitly requested by the user. When the user asks to release, **always include the AUR update and the local macOS installation afterwards** unless they say otherwise:
1. Bump version in all 6 files:
   - `VERSION`
   - `mygo.json`
   - `manifest.json`
   - `cmd/postdive/main.go` (`var version = "X.Y.Z-dev"`)
   - `CHANGELOG.md` (English entry at top)
   - `docs/HISTORY.md`
2. Commit on `main`, then run `./release.sh` (runs tests, cross-compiles Desktop + macOS Universal `.app`/`.dmg`/`.zip` + Android `.apk`/`.aab`, creates tag `vX.Y.Z`, pushes `main` & tag, publishes GitHub release, prints Homebrew tap URL/SHA256).
3. Update AUR package metadata and push:
   ```bash
   ./scripts/update-aur.sh <VERSION>
   git add packaging/aur/postdive-bin/ && git commit -m "aur: bump postdive-bin to <VERSION>" && git push origin main
   ```
4. Install locally on macOS right after the release (the user runs PostDive from `/Applications/PostDive.app`, **not** `~/Applications`):
   - Quit running app cleanly (`osascript -e 'tell application "PostDive" to quit'`), wait until the process exits.
   - Install via `POSTDIVE_APP_DIR=/Applications ./install.sh` (reuses `dist/PostDive.app` just built by `./release.sh`).
   - Verify `/Applications/PostDive.app/Contents/MacOS/PostDive --version` prints `postdive <VERSION>`, then relaunch (`open /Applications/PostDive.app`) and confirm the process is running.
5. Report the release URL, commits, local installed version, and the Homebrew tap `url`/`sha256`.

## 7. Session Handoff Protocol (`HANDOFF.md`)
`HANDOFF.md` (gitignored, local workspace root) provides explicit handoff state across threads, models, and restarts—stronger than `/compact` alone.
- **When starting a continuation thread:** If `HANDOFF.md` exists and the user asks to resume/continue, read it first before exploring.
- **When asked to hand off / wrap up:** Overwrite `HANDOFF.md` concisely (do not append endlessly) with:
  1. Current state (HEAD commit, release tag, local installed version)
  2. Active goal & exact next steps
  3. Key decisions & rejected alternatives
  4. Open risks / blockers / untouched checks
  5. Relevant file paths
- **When a workstream finishes:** Reset `HANDOFF.md` to a clean idle state so future sessions are not misled by stale tasks.
