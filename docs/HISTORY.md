# PostDive – Milestones

A rough timeline of when the major features arrived. Smaller steps in between are grouped as a
version range under “Polish & fixes”. Every detail is in the [CHANGELOG](../CHANGELOG.md).

| Version | Date | Milestone |
|---|---|---|
| **0.2.0 – 0.2.4** | Nov 13–15, 2026 | **Started as “OmaThunder”**: a Thunderbird add-on with a Go helper. AI sorting of the inbox, assistant in the compose window, summarize, translate, ask questions about mails, Homebrew install |
| **0.2.5 – 0.2.9** | Nov 15–16 | **Spaces & cleanup**: topic spaces instead of customers, Kanban per mailbox, Thunderbird filters underneath, archive & cleanup with verified `.eml` export |
| **0.3.0** | Nov 18 | **Own app instead of a Kanban board**: native UI with mail cockpit, calendar and contacts; settings move into the app |
| **0.5.0** | Nov 20 | **Standalone, without Thunderbird**: own IMAP/SMTP engine with SQLite and full-text search, conversations, Thunderbird import, S/MIME, Gmail & Microsoft 365 sign-in (OAuth), CalDAV/CardDAV, offline outbox, account transfer by QR code |
| **0.6.0** | Nov 20 | **Android app**: phone layout with swipe gestures, accounts by QR code from the computer, new mail in the background, passwords in the Android Keystore |
| **0.7.1 – 0.7.5** | Nov 21 | **Encryption & writing**: native OpenPGP (no `gpg`), security hardening, WYSIWYG editor with inline images and tables, rule editor |
| 0.7.6 – 0.7.16 | Nov 22–23 | *Polish & fixes*: compact folder bar, settings in tabs, “Select all”, empty spam, Android background sync, focus counters, local AI guide |
| **0.7.17 – 0.7.19** | Nov 23 | **Calendar & invitations**: day view with a 24h timeline, visible response status, target calendar when accepting (Google & CalDAV too) |
| **0.7.21** | Nov 23 | **AI help while writing**: “✨ AI help” right in the reply and compose window |
| 0.7.20 – 0.7.27 | Nov 23–24 | *Polish & fixes*: correct “Sent” folder, S/MIME decryption & team import, invitations from encrypted mails, Jira preview, source & header view, accounts/calendars/rules in the settings |
| **0.7.28** | Nov 24 | **AI assistant with tools**: searches mails, conversations, contacts and events, only proposes actions; personal AI instructions, rule suggestions from mail headers |
| **0.7.29 – 0.7.30** | Nov 24 | **MCP server**: `postdive --mcp` connects Claude Code, Claude Desktop, Cursor & co. locally to mailbox, calendar and contacts; read-only by default |
| **0.7.31** | Nov 24 | **Renamed to PostDive**, plus Linux support for Wayland/Hyprland (Omarchy) |
| **0.7.32 – 0.7.35** | Nov 25 | **UI redesign**: calmer interface in the style of Apple Mail, simple account setup, clearly separated areas, dialogs without double frames, better theme contrast |
| 0.7.36 – 0.7.40 | Nov 25 | *Polish & fixes*: calendar weeks, diagnostic log for sign-in problems (without passwords), umlauts in passwords, S/MIME signatures with intermediate certificates |
| **0.7.41** | Nov 25 | **Tidy contacts**: merge duplicates side by side, complete contact details from mails with AI; week view on the day view's timeline |
| 0.7.42 – 0.7.44 | Nov 25 | *Polish & fixes*: all contacts instead of 500, Outlook quotes in names, clearer Google Contacts errors, status bar shows sync errors ([#1](https://github.com/diver80/postdive/issues/1)) |
| **0.7.45** | Nov 25 | **Activity**: a log of what happened to mails on this device (by you, rules or agents), with a jump to the mail and undo |
| **0.7.46 – 0.7.47** | Nov 26 | **Events with attendees**: attendees in invitations and the calendar, send invitations yourself (Google with a Meet link, otherwise by calendar mail), changes & cancellations to everyone, answers are recorded; “Plan in Teams” opens the Teams form filled in |
| 0.7.48 – 0.7.55 | Nov 26–27 | *Polish & fixes*: [setup guide](SETUP.md), quoted text in replies to encrypted mails, search everywhere with a scope bar, decline invitations when deleting, default event reminder (15 min), macOS notification warning, Antigravity CLI (`agy`) instead of the Gemini CLI, shorter AI provider list, search entry in the sidebar, security/privacy hardening & hotfix so accepting invites never triggers re-invites to attendees |
