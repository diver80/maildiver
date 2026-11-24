<p align="center">
  <img src="assets/icon.svg" width="72" height="72" alt="PostDive" />
</p>

<h1 align="center">PostDive</h1>

<p align="center">
  <strong>Native Email, Calendar &amp; Contacts Suite with Flexible Local &amp; Cloud AI</strong><br/>
  <em>Nativer E-Mail-, Kalender- &amp; Kontakt-Client mit flexibler lokaler &amp; Cloud-KI</em>
</p>

<p align="center">
  <a href="https://diver80.github.io/postdive/?lang=en">🌐 Website (EN)</a> ·
  <a href="https://diver80.github.io/postdive/?lang=de">🌐 Webseite (DE)</a> ·
  <a href="https://github.com/diver80/postdive/releases/latest">⬇️ Latest Release</a> ·
  <a href="docs/MCP.md">🔌 MCP Server</a> ·
  <a href="docs/LOCAL_AI.md">🤖 Local AI Guide</a> ·
  <a href="docs/ARCHITECTURE.md">📐 Architecture</a> ·
  <a href="LICENSE">⚖️ MIT License</a>
</p>

![PostDive Desktop Workspace](docs/screenshots/app-main.png)

> **Status (EN):** Actively in development and **used daily on macOS**. Prebuilt binaries for macOS, Linux, and Windows are attached to every release; Android builds are supported via `mygo`. **Testers, feedback, and Pull Requests are very welcome!** Licensed under **MIT**.
>
> **Status (DE):** Aktiv in Entwicklung und **täglich auf macOS im produktiven Einsatz**. Fertige Binaries für macOS, Linux und Windows hängen an jedem Release; Android-Builds laufen über `mygo`. **Tester, Feedback und Pull-Requests sind herzlich willkommen!** Lizenz: **MIT**.

---

## Highlights (EN)

- **Flexible AI — Local, Cloud & Built-in MCP Server:** Run completely locally via **MTPLX** ([Inkling 2 Mini](docs/LOCAL_AI.md) on Apple Silicon), **Ollama**, `llama.cpp`, or `llamafile` (see [docs/LOCAL_AI.md](docs/LOCAL_AI.md)) — or connect **Anthropic Claude**, **Google Gemini**, **OpenAI**, and CLI agents (`claude-cli`, `gemini-cli`, `opencode-cli`) with **Personal AI Instructions** (`Settings → AI & Sync`) and live model dropdowns. The in-app AI Copilot (`search_messages`, `get_thread`, `search_contacts`, `list_folders`, `list_calendar_events`, **Follow-ups**) only *proposes* changes (`create_draft`, `create_calendar_event`, `mark_message_read`, `move_message`, `flag_message`) until you click Execute. Deterministic local filter rules (incl. **header-based AI filter suggestions** across all RFC 5322 headers) run at **0 tokens**.
- **Built-in Local MCP Server (`postdive --mcp`):** Connect **Claude Code**, **Claude Desktop**, **Antigravity**, **T3 Code**, or **Cursor** straight to your local mailbox, calendar, contacts, and triage state over **stdio** — zero open network ports, read-only by default (opt-in `--allow-drafts` / `--allow-writes`), never sends email or permanently deletes, never decrypts S/MIME/OpenPGP over MCP, with 1-click config copy in **Settings → AI & Sync** (see [docs/MCP.md](docs/MCP.md)).
- **Native Performance, Raw Source & Unified Settings:** Single compiled Go binary + GPU-accelerated [mygo](https://github.com/egoist/mygo) UI — zero Electron/Chromium memory overhead. Local SQLite FTS5 full-text search, real-time IMAP IDLE, Focus (`Unread & ★`), **smart compact folder sidebar**, **Formatted / Text / Source** reader (with all RFC 5322 headers, live filter & 1-click copy), embedded **Web Preview** (opening Jira/Atlassian work items directly by key), unified **Settings (`⌘,`)** incl. Accounts, keyboard-first shortcuts (`j`/`k`/`r`/`e`/`v`/`c`/`s`/`⌘K`), and detached native windows for Compose (with inline **✨ AI Assist**) & Calendar Events.
- **Security & Privacy (S/MIME & Native OpenPGP):** Full **S/MIME** (`.p12`/`.pfx` import with chunked OS Keychain storage, **team bulk recipient cert import** from files/folders/multi-PEM/`.p7b`/`.zip`, signature verification, decryption incl. encrypted `.ics` invites & attachments, signing & AES-256-CBC encryption) AND **native OpenPGP** (PGP/MIME RFC 3156 & Inline PGP with zero external `gpg`, RSA-3072 key generation, passphrase-protected OS Keychain storage, WKD / `keys.openpgp.org` lookup with SSRF/key-binding guards), remote images blocked by default with persistent per-sender allowlist, strict HTML sanitizer, pixel-bomb & SSRF guards, TLS/STARTTLS-only transport, encrypted **LAN QR-code** device transfer (AES-256-GCM), and risky attachment protection.
- **Complete Suite (Email, Calendar & Contacts):** Multi-account IMAP/SMTP (incl. **Gmail & Microsoft 365 XOAUTH2**) with newest-on-top conversations, **Offline Outbox & Scheduled Send**, desktop **HTML WYSIWYG compose editor** (inline pasted images, editable HTML tables, drag & drop / clipboard files), inline `.ics` RSVP with target calendar picker, **24h Day / Week / Month / Agenda** calendar (remembers last view, Day view default) with **Google Calendar & CalDAV** writeback + 1-click Teams/Meet/Zoom join links, and unified local + Google + **CardDAV** Contacts with autocomplete.
- **Effortless Setup (Thunderbird Import & LAN QR):** 1-click read-only Thunderbird profile importer brings over accounts, signatures, calendars, contacts, and filter rules in seconds — plus provider presets, full inline Filter Rule Editor, and one-shot AES-256-GCM LAN QR setup transfer to another desktop or Android phone.
- **Cross-Platform:** Native builds for **macOS** (Apple Silicon & Intel `.app`), **Linux** (`x64`/`arm64`, live Omarchy theme sync), **Windows** (`x64`/`arm64`), and **Android** (`mygo`, swipe actions + long-press multi-select, WorkManager background sync).

---

## Highlights (DE)

- **KI flexibel (Lokal, Cloud & eingebauter MCP-Server):** Komplett lokal mit **MTPLX** ([Inkling 2 Mini](docs/LOCAL_AI.md) auf Apple Silicon), **Ollama**, `llama.cpp` oder `llamafile` (Anleitung in [docs/LOCAL_AI.md](docs/LOCAL_AI.md)) – oder mit **Anthropic Claude**, **Google Gemini**, **OpenAI** sowie CLI-Agenten (`claude-cli`, `gemini-cli`, `opencode-cli`) inkl. **Persönlicher KI-Hinweise** (*Einstellungen → KI & Sync*). Der KI-Assistent (`search_messages`, `get_thread`, `search_contacts`, `list_folders`, `list_calendar_events`, **Follow-ups**) *schlägt Änderungen nur vor* (`create_draft`, `create_calendar_event`, `mark_message_read`, `move_message`, `flag_message`) – nichts läuft ohne deinen Klick. Lokale Filterregeln (inkl. **Header-basierter KI-Filtervorschläge** über alle RFC-5322-Header) laufen mit **0 Tokens**.
- **Eingebauter lokaler MCP-Server (`postdive --mcp`):** Verbindet **Claude Code**, **Claude Desktop**, **Antigravity**, **T3 Code** oder **Cursor** direkt über lokales **stdio** mit Postfach, Kalender, Kontakten und Triage – ohne offene Ports, standardmäßig Read-Only (Opt-in `--allow-drafts` / `--allow-writes`), sendet und löscht niemals, entschlüsselt keine S/MIME-/PGP-Mails über MCP, inkl. 1-Klick-Config-Kopieren in **Einstellungen → KI & Sync** ([docs/MCP.md](docs/MCP.md)).
- **Native Performance, Quelltext-Ansicht & Zentrale Einstellungen:** Ein einziges kompiliertes Go-Binary mit GPU-beschleunigter [mygo](https://github.com/egoist/mygo)-Oberfläche ohne Electron-Ballast. Sofortige lokale SQLite-FTS5-Volltextsuche, Echtzeit-IMAP-IDLE, Fokus (**Ungelesen & ★**), **kompakte Ordner-Seitenleiste**, **Formatiert / Text / Quelltext**-Ansicht (mit allen RFC-5322-Headern, Live-Filter & Kopieren), eingebettete **Web-Vorschau** (öffnet Jira-/Atlassian-Work-Items direkt per Key), einheitliche **Einstellungen (`⌘,`)** inkl. Konten, Tastatur-Bedienung (`j`/`k`/`r`/`e`/`v`/`c`/`s`/`⌘K`) und eigene Fenster für Verfassen (mit **✨ KI-Hilfe**) & Termine.
- **Sicherheit & Datenschutz (S/MIME & Natives OpenPGP):** Vollständiges **S/MIME** (`.p12`/`.pfx`-Import mit Schlüsselbund-Chunking, **Team-Bulk-Import** aus Dateien/Ordnern/Multi-PEM/`.p7b`/`.zip`, Signaturprüfung, Entschlüsselung inkl. verschlüsselter `.ics`-Einladungen & Anhänge, Signieren & AES-256-Verschlüsselung) UND **natives OpenPGP** (PGP/MIME RFC 3156 & Inline-PGP ohne externes `gpg`, RSA-3072-Schlüsselerzeugung, Passphrase-Schutz im Schlüsselbund, abgesicherter WKD-/VKS-Abruf), externe Bilder standardmäßig blockiert (mit dauerhafter Absender-Allowlist), strikter HTML-Sanitizer, Pixel-Bomb- & SSRF-Schutz, nur TLS/STARTTLS, verschlüsselter **LAN-QR-Code-Transfer** (AES-256-GCM) und Anhang-Schutz.
- **Komplette Suite (E-Mail, Kalender & Kontakte):** Multi-Konto IMAP/SMTP (inkl. **Gmail & Microsoft 365 XOAUTH2**) mit Unterhaltungen (neueste oben), **Offline-Postausgang & Zeitgesteuertes Senden**, **Desktop-HTML-WYSIWYG-Editor** (Bilder per `⌘V` als `cid:`-Inline-Bild, bearbeitbare HTML-Tabellen, Drag & Drop / Zwischenablage-Anhänge), `.ics`-RSVP mit Zielkalender-Auswahl, **24h-Tages-/Wochen-/Monats-/Agenda-Kalender** (merkt sich letzte Ansicht, Tagesansicht als Standard) mit **Google-Kalender & CalDAV**-Writeback + direkten Teams/Meet/Zoom-Links sowie lokale, Google- & **CardDAV**-Kontakte.
- **Einfaches Setup (Thunderbird-Import & LAN-QR):** Rein lesender 1-Klick-Import aus Thunderbird übernimmt Konten, Signaturen, Kalender, Kontakte & Filterregeln automatisch – plus vollständiger Regel-Editor und einmaliger AES-256-GCM-QR-Code für die schnelle Einrichtung auf weiteren Geräten oder Android.
- **Plattformunabhängig:** Native Builds für **macOS** (Apple Silicon & Intel `.app`), **Linux** (`x64`/`arm64`, Omarchy-Live-Theme), **Windows** (`x64`/`arm64`) und **Android** (`mygo`, Wischgesten, Long-Press-Mehrfachauswahl & Hintergrund-Sync).

---

## Screenshots

| Conversations & Reading Pane | Calendar (Light Theme) |
|---|---|
| ![Conversations](docs/screenshots/app-conversation.png) | ![Calendar](docs/screenshots/app-calendar-light.png) |

| Quick-Rule Wizard & Filter Rules | Flexible AI & Privacy Settings |
|---|---|
| ![Quick Rules](docs/screenshots/app-quick-rule.png) | ![Settings](docs/screenshots/app-settings.png) |

---

## Quick Start / Installation

### Homebrew (macOS & Linux)

```bash
# macOS (Universal PostDive.app + postdive CLI)
brew install --cask diver80/tap/postdive

# Linux / CLI binary
brew install diver80/tap/postdive
```

### Arch Linux / Omarchy (PKGBUILD)

```bash
git clone --depth=1 https://github.com/diver80/postdive.git /tmp/postdive-aur \
  && cd /tmp/postdive-aur/packaging/aur/postdive-bin \
  && makepkg -si
```

*(The Arch `PKGBUILD` and `.SRCINFO` live in [`packaging/aur/postdive-bin/`](packaging/aur/postdive-bin). PostDive follows Omarchy themes live via `~/.local/state/omarchy/current/theme/colors.toml`.)*

### Local MCP Server (Claude Code, Claude Desktop, Cursor, Antigravity)

```bash
# Claude Code (read-only by default; add --allow-drafts or --allow-writes optionally)
claude mcp add postdive -- postdive --mcp
```

Full setup snippets, tool list, and security guarantees: **[docs/MCP.md](docs/MCP.md)** (also available with 1-click copy inside **PostDive → Settings → AI & Sync**).

### GitHub Releases or Build from Source

Download the latest prebuilt release from [GitHub Releases](https://github.com/diver80/postdive/releases/latest), or build from source (Go 1.24+ / 1.27+):

```bash
git clone https://github.com/diver80/postdive.git
cd postdive
./build.sh        # tests, macOS PostDive.app, Linux/Windows/macOS binaries + SHA256SUMS
./install.sh      # installs postdive to ~/.local/bin and PostDive.app to ~/Applications
```

Additional build targets:

```bash
./build.sh --packages                                                    # .dmg / .deb / .rpm / PKGBUILD / Windows installer
./build.sh --android                                                     # Android APK/AAB (requires Android NDK)
POSTDIVE_RENDER=docs/screenshots go test ./internal/nativeui -run TestRenderScreens
```

## Contributing & License

Testers (especially on Linux, Windows, and Android), bug reports, feature discussions, and Pull Requests are warmly welcome!

Licensed under the **MIT License** — see [LICENSE](LICENSE).
