# MailDiver MCP Server (`maildiver --mcp`)

Connect external AI agents (**Claude Code**, **Claude Desktop**, **Antigravity**, **T3 Code**, **Cursor**, **Windsurf**, etc.) directly to your local MailDiver mailbox, calendar, contacts, and triage state via the **Model Context Protocol (MCP)** over local **stdio** — zero open network ports, zero separate daemon, and your IMAP passwords / OAuth tokens never leave MailDiver's OS Keychain.

*(Deutsche Version weiter unten / German guide below.)*

---

## Quick Setup (EN)

### 1. Claude Code (CLI)

```bash
# Read-only (recommended default)
claude mcp add maildiver -- maildiver --mcp

# Optional: allow saving drafts (never sends)
claude mcp add maildiver -- maildiver --mcp --allow-drafts

# Optional: allow drafts + calendar events + mark-read/move/star (never sends or deletes)
claude mcp add maildiver -- maildiver --mcp --allow-writes
```

### 2. Claude Desktop / Cursor / Antigravity / T3 Code (`mcpServers` JSON)

You can also copy this snippet directly inside **MailDiver → Settings (`⌘,`) → AI & Sync → Local MCP Server**:

```json
{
  "mcpServers": {
    "maildiver": {
      "command": "maildiver",
      "args": ["--mcp"]
    }
  }
}
```

*(If `maildiver` is not on your GUI app's `PATH`, use the full path such as `/opt/homebrew/bin/maildiver` on Apple Silicon macOS or `~/.local/bin/maildiver` on Linux.)*

---

## Available MCP Tools

| Mode | Tool | Description |
|---|---|---|
| **Default (Read-Only)** | `list_accounts` | List configured email accounts & identities |
| **Default (Read-Only)** | `list_folders` | List folders with exact paths & unread counts |
| **Default (Read-Only)** | `search_messages` | Local SQLite FTS5 search (`from:`, `to:`, `subject:`, `unread`, `days`, `accountId`) |
| **Default (Read-Only)** | `get_message` | Read plain-text body, headers & attachment list by ID *(never decrypts S/MIME/PGP)* |
| **Default (Read-Only)** | `get_thread` | Read chronological conversation thread by message ID |
| **Default (Read-Only)** | `list_calendars` | List local, Google, CalDAV & iCal calendars |
| **Default (Read-Only)** | `list_calendar_events` | List events for a time range (default: today + 7 days) |
| **Default (Read-Only)** | `search_contacts` | Search unified local, Google & CardDAV contacts |
| **Default (Read-Only)** | `triage_summary` | Read MailDiver local triage bucket counts |
| **`--allow-drafts`** *(or `--allow-writes`)* | `create_draft` | Save a new email / reply / forward into Drafts *(never sends)* |
| **`--allow-writes`** | `create_calendar_event` | Create a calendar event in a writable calendar |
| **`--allow-writes`** | `mark_message_read` | Mark emails as read or unread (`ExpectedMessageID`-guarded) |
| **`--allow-writes`** | `move_message` | Move or archive emails to a folder *(never permanently deletes)* |
| **`--allow-writes`** | `flag_message` | Star or unstar emails |

---

## Security Design

- **Local stdio only:** No listening TCP socket, no bearer tokens to leak.
- **No direct sending or permanent deletion:** `send_email` and permanent delete tools intentionally do not exist in the MCP server.
- **Encrypted emails stay protected:** `get_message` and `get_thread` never decrypt S/MIME or OpenPGP messages over MCP, preventing prompt injections from exfiltrating encrypted mail.
- **Respects `mailbox.readOnly`:** If `mailbox.readOnly` is `true` in `config.json`, write tools are blocked even if `--allow-drafts` / `--allow-writes` is passed.
- **Prompt-injection boundary:** Every mail, calendar, contact, and triage payload is wrapped in `<untrusted_...>` tags and size-capped (UTF-8 safe).
- **Safe coexistence with the desktop app:** `maildiver --mcp` does not start a GUI or duplicate IMAP IDLE loops and shares `maildiver.db` cleanly via SQLite WAL + `busy_timeout`.

---

## Kurzanleitung (DE)

Mit `maildiver --mcp` stellt MailDiver einen lokalen **Model-Context-Protocol-(MCP)-Server über stdio** bereit – direkt aus demselben Binary, ohne offenen Port und ohne dass externe Agenten Zugriff auf deine IMAP-Passwörter oder OAuth-Tokens bekommen.

### Einrichtung

1. **In MailDiver:** Öffne **Einstellungen (`⌘,`) → KI & Sync → Lokaler MCP-Server**, wähle den gewünschten Modus (**Nur Lesen**, **+ Entwürfe** oder **+ Kalender & Sortieren**) und klicke auf **MCP-Config kopieren** oder **Claude-CLI-Befehl kopieren**.
2. **Per Claude Code CLI:**
   ```bash
   claude mcp add maildiver -- maildiver --mcp
   # oder mit Entwurfserlaubnis (sendet niemals):
   claude mcp add maildiver -- maildiver --mcp --allow-drafts
   ```

### Sicherheits-Leitplanken
- **Standardmäßig strikt Read-Only** (`--mcp`), Schreibfunktionen nur per explizitem Opt-in (`--allow-drafts` / `--allow-writes`).
- **Niemals Senden oder endgültig Löschen** per MCP.
- **Verschlüsselte S/MIME- & OpenPGP-Mails werden über MCP niemals entschlüsselt.**
- Alle Mail-, Kalender- und Kontaktdaten werden zum Schutz vor Prompt-Injection in `<untrusted_...>`-Blöcken gekapselt.
