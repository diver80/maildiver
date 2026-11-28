# Connecting Mail, Calendars & Contacts

A short overview of how to connect common mail providers, Google and Microsoft/Outlook to
PostDive. Everything lives under **Settings (`⌘,`) → Accounts**. PostDive keeps passwords and
sign-in tokens in the system keychain.

## At a glance

| What | How in PostDive | Read | Write | Effort |
|---|---|---|---|---|
| Mail at IONOS, 1&1, GMX, WEB.DE, STRATO | Account from a provider preset, regular password | ✓ | ✓ | low |
| Gmail | Account with preset “Gmail (App-Passwort)” | ✓ | ✓ | low |
| iCloud Mail, Fastmail | Account from a preset, app password | ✓ | ✓ | low |
| Microsoft 365 / Outlook.com mail | Account “Microsoft 365 / Outlook (OAuth2)” with your own app registration | ✓ | ✓ | high, often needs IT |
| Google Calendar & Contacts | Settings → Accounts → Calendar → “Connect Google Calendar” | ✓ | ✓ | medium (once) |
| Google Calendar without signing in | Secret iCal address as a calendar subscription | ✓ | – | low |
| Outlook / Microsoft 365 calendar | Published calendar as a calendar subscription | ✓ | – | low, if allowed |
| Scheduling a Teams meeting | Event editor → “Plan in Teams” | – | in Teams | none |
| Nextcloud, Fastmail, Radicale … | CalDAV & CardDAV | ✓ | ✓ | low |

## Mail accounts

**Settings → Accounts → Email → “Add New IMAP/SMTP Account”**, then pick the matching preset under
*Provider*. It fills in servers, ports and encryption; you only enter address and password. The
username is always the full email address.

| Provider | Preset | IMAP | SMTP | Password |
|---|---|---|---|---|
| **IONOS** | IONOS | `imap.ionos.de` 993 (TLS) | `smtp.ionos.de` 465 (TLS); 587 (STARTTLS) works too | Mailbox password |
| **1&1** | 1&1 | `imap.1und1.de` 993 (TLS) | `smtp.1und1.de` 587 (STARTTLS) | Mailbox password |
| **GMX** | GMX | `imap.gmx.net` 993 (TLS) | `mail.gmx.net` 587 (STARTTLS) | Account password; enable IMAP access in GMX webmail first |
| **WEB.DE** | WEB.DE | `imap.web.de` 993 (TLS) | `smtp.web.de` 587 (STARTTLS) | Account password; enable IMAP access in WEB.DE webmail first |
| **STRATO** | STRATO | `imap.strato.de` 993 (TLS) | `smtp.strato.de` 465 (TLS) | Mailbox password |
| **Gmail** | Gmail (App-Passwort) | `imap.gmail.com` 993 (TLS) | `smtp.gmail.com` 465 (TLS) | App password (button “Create App Password ↗”; needs 2-Step Verification) |
| **iCloud Mail** | iCloud Mail | `imap.mail.me.com` 993 (TLS) | `smtp.mail.me.com` 587 (STARTTLS) | App-specific password from your Apple Account |
| **Fastmail** | Fastmail | `imap.fastmail.com` 993 (TLS) | `smtp.fastmail.com` 465 (TLS) | App password from the Fastmail settings |
| **Microsoft 365 / Outlook.com** | Microsoft 365 / Outlook (OAuth2) | `outlook.office365.com` 993 (TLS) | `smtp.office365.com` 587 (STARTTLS) | No password: sign-in in the browser, see below |
| Others | Benutzerdefiniert / Custom | per provider | per provider | – |

PostDive only connects encrypted (TLS or STARTTLS). Passwords with umlauts work. If signing in
fails, PostDive writes a diagnostic log without passwords that you can attach to a support request
or an issue.

**More addresses (aliases):** under “▸ Advanced settings (servers, signature, aliases…)” of an
account you can add further sender addresses with their own name and signature.

### Microsoft 365 / Outlook.com

Microsoft only lets mail apps in with an OAuth sign-in. For that, PostDive needs **its own app
registration** (client ID) in Microsoft Entra ID with the permissions `IMAP.AccessAsUser.All`,
`SMTP.Send` and `offline_access`. In companies usually only IT may create one; without a
registration, Microsoft mail does not work in PostDive. Calendars and Teams still do, see below.

## Google Calendar & Contacts

**Settings → Accounts → Calendar → Google Calendar (Bidirectional).** You set up your own Google
Cloud project once; the buttons in PostDive lead straight to the pages:

1. **Enable the Calendar API** (and the **People API** if you also want Google Contacts).
2. **Create an OAuth client of type “Desktop app”** and enter its client ID (and client secret) in
   PostDive.
3. **Connect Google Calendar**: the browser opens and you confirm the access.

PostDive then syncs all Google calendars both ways; Google itself sends the invitations for events
with attendees, with a Google Meet link if you like. Google Contacts use the same sign-in
(Contacts → “Google Sync”).

> **Tip:** while the Google project is in the “Testing” state, sign-ins expire after 7 days. For your
> own use, switch the project to “In production” (the “unverified app” warning when signing in is
> then expected), or reconnect when it expires.

**Read-only, without a Google Cloud project:** in Google Calendar under *Settings → your calendar →
Integrate calendar*, copy the **“Secret address in iCal format”** and add it to PostDive as a
calendar subscription (see below).

## Outlook / Microsoft 365 calendar

Without an app registration this works **read-only**, through a published calendar:

1. Outlook on the web → **Settings → Calendar → Shared calendars → Publish a calendar**.
2. Choose the calendar, set **“Can view all details”**, publish.
3. Copy the **ICS link** and add it to PostDive as a calendar subscription.

Microsoft refreshes published calendars with a delay (often a few hours). If “Publish a calendar”
is missing, your organization has switched it off.

**Teams meetings** are planned in the event editor with **“Plan in Teams”**: Teams opens with
title, time, notes and attendees filled in, and sends from there. With **“Invite myself”** the
invitation also reaches your PostDive inbox; “Accept” then puts the meeting into your PostDive
calendar.

## Calendar subscriptions (ICS / webcal)

**Settings → Accounts → Calendar → “Add Calendar Subscription (https:// or webcal://)”.**
For Google (secret address), Outlook (published calendar), public holidays, school holidays or
sports fixtures. Subscriptions are read-only; PostDive fetches them again regularly.

## CalDAV & CardDAV

For calendars and address books on Nextcloud, Fastmail, Radicale and other CalDAV/CardDAV servers:
**Settings → Accounts → Calendar → CalDAV & CardDAV**. Enter server address, username and (app)
password; PostDive finds the calendars and address books itself and writes changes back.

## Sending invitations

| Calendar | Who sends the invitation? |
|---|---|
| Google Calendar | Google itself (with a Meet link if you like) |
| Local calendar, CalDAV | PostDive, as a calendar mail through your mail account (e.g. IONOS) |
| Teams | Teams, after “Plan in Teams” |

PostDive records the attendees' answers with the event once you open the reply mail.

## Notifications & reminders (macOS)

Event reminders and new mail show as system notifications. macOS decides how they appear: under
**System Settings → Notifications → PostDive**, choose the style **“Alerts”** so reminders stay on
screen until dismissed (banners disappear after a few seconds). While a **Focus** is on, macOS only
shows notifications of the apps that Focus allows. **Settings → General → Notifications** in
PostDive warns when macOS hides its notifications and opens both settings.

## Moving from Thunderbird & to more devices

- **Thunderbird import** (Settings → Accounts → TB Import): takes over accounts, signatures,
  calendars, contacts and filter rules, read-only.
- **Another device / Android:** open **Settings → AI & Sync → Transfer to another device (LAN QR)**
  to copy accounts and settings from the computer over a one-shot encrypted AES-256-GCM QR code.
