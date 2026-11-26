# PostDive MCP Server (`postdive --mcp`)

Connect external AI agents (**Claude Code**, **Claude Desktop**, **Antigravity**, **T3 Code**, **Cursor**, **Windsurf**, etc.) directly to your local PostDive mailbox, calendar, contacts, and triage state via the **Model Context Protocol (MCP)** over local **stdio** — zero open network ports, zero separate daemon, and your IMAP passwords / OAuth tokens never leave PostDive's OS Keychain.

---

## Quick Setup

### 1. Claude Code (CLI)

```bash
# Read-only (recommended default)
claude mcp add postdive -- postdive --mcp

# Optional: allow saving drafts (never sends)
claude mcp add postdive -- postdive --mcp --allow-drafts

# Optional: allow drafts + calendar events + mark-read/move/star (never sends or deletes)
claude mcp add postdive -- postdive --mcp --allow-writes
```

### 2. Claude Desktop / Cursor / Antigravity / T3 Code (`mcpServers` JSON)

You can also copy this snippet directly inside **PostDive → Settings (`⌘,`) → AI & Sync → Local MCP Server**:

```json
{
  "mcpServers": {
    "postdive": {
      "command": "postdive",
      "args": ["--mcp"]
    }
  }
}
```

*(If `postdive` is not on your GUI app's `PATH`, use the full path such as `/opt/homebrew/bin/postdive` on Apple Silicon macOS or `~/.local/bin/postdive` on Linux.)*

---

## Available MCP Tools

| Mode | Tool | Description |
|---|---|---|
| **Default (Read-Only)** | `list_accounts` | List configured email accounts & identities |
| **Default (Read-Only)** | `list_folders` | List folders with exact paths & unread counts |
| **Default (Read-Only)** | `search_messages` | Local SQLite FTS5 search (`from:`, `to:`, `subject:`, `unread`, `accountId`, optional `days`; newest first, max 50) |
| **Default (Read-Only)** | `get_message` | Read plain-text body, headers & attachment list by ID *(never decrypts S/MIME/PGP)* |
| **Default (Read-Only)** | `get_thread` | Read chronological conversation thread by message ID |
| **Default (Read-Only)** | `list_calendars` | List local, Google, CalDAV & iCal calendars |
| **Default (Read-Only)** | `list_calendar_events` | List events for a time range (default: today + 7 days) |
| **Default (Read-Only)** | `search_contacts` | Search unified local, Google & CardDAV contacts |
| **Default (Read-Only)** | `triage_summary` | Read PostDive local triage bucket counts |
| **`--allow-drafts`** *(or `--allow-writes`)* | `create_draft` | Save a new email / reply / forward into Drafts *(never sends)*. A reply uses the original's account, recipients, `Re:` subject and quote; encrypted originals are never quoted or forwarded |
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
- **Safe coexistence with the desktop app:** `postdive --mcp` does not start a GUI or duplicate IMAP IDLE loops and shares `postdive.db` cleanly via SQLite WAL + `busy_timeout`.
