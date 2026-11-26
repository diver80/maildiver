# PostDive – Meilensteine

Grober Überblick, wann welche großen Funktionen dazukamen. Kleinere Schritte dazwischen sind als
Versionsbereich unter „Feinschliff & Fehlerbehebungen“ zusammengefasst. Alle Details stehen im
[CHANGELOG](../CHANGELOG.md).

*A rough timeline of PostDive's major features; see the [CHANGELOG](../CHANGELOG.md) (German) for every release.*

| Version | Datum | Meilenstein |
|---|---|---|
| **0.2.0 – 0.2.4** | 13.–15.11.2026 | **Start als „OmaThunder“** – Thunderbird-Add-on mit Go-Hilfsprogramm: KI-Sortierung des Posteingangs, Assistent im Schreibfenster, Zusammenfassen, Übersetzen, Fragen an Mails, Homebrew-Installation |
| **0.2.5 – 0.2.9** | 15.–16.11. | **Spaces & Aufräumen** – Themenbereiche statt Kunden, Kanban pro Postfach, Thunderbird-Filter als Unterbau, Archiv & Cleanup mit geprüftem `.eml`-Export |
| **0.3.0** | 18.11. | **Eigene App statt Kanban-Board** – native Oberfläche mit Mail-Cockpit, Kalender und Kontakten; Einstellungen wandern in die App |
| **0.5.0** | 20.11. | **Eigenständig, ohne Thunderbird** – eigene IMAP/SMTP-Engine mit SQLite und Volltextsuche, Unterhaltungen, Thunderbird-Import, S/MIME, Gmail- & Microsoft-365-Anmeldung (OAuth), CalDAV/CardDAV, Offline-Postausgang, Konto-Übertragung per QR-Code |
| **0.6.0** | 20.11. | **Android-App** – Telefon-Ansicht mit Wischgesten, Konten per QR-Code vom Computer, neue Mails im Hintergrund, Passwörter im Android-Keystore |
| **0.7.1 – 0.7.5** | 21.11. | **Verschlüsselung & Schreiben** – natives OpenPGP (ohne `gpg`), Sicherheits-Härtung, WYSIWYG-Editor mit Inline-Bildern und Tabellen, Regeleditor |
| 0.7.6 – 0.7.16 | 22.–23.11. | *Feinschliff & Fehlerbehebungen* – kompakte Ordnerleiste, Einstellungen in Tabs, „Alle auswählen“, Spam leeren, Android-Hintergrund-Sync, Fokus-Zähler, Anleitung für lokale KI |
| **0.7.17 – 0.7.19** | 23.11. | **Kalender & Einladungen** – Tagesansicht mit 24h-Zeitleiste, sichtbarer Zusage-Status, Zielkalender beim Annehmen (auch Google & CalDAV) |
| **0.7.21** | 23.11. | **KI-Hilfe beim Schreiben** – „✨ KI-Hilfe“ direkt im Antwort- und Verfassen-Fenster |
| 0.7.20 – 0.7.27 | 23.–24.11. | *Feinschliff & Fehlerbehebungen* – richtiger „Gesendet“-Ordner, S/MIME-Entschlüsselung & Team-Import, Einladungen aus verschlüsselten Mails, Jira-Vorschau, Quelltext- & Header-Ansicht, Konten/Kalender/Regeln in den Einstellungen |
| **0.7.28** | 24.11. | **KI-Assistent mit Werkzeugen** – sucht Mails, Unterhaltungen, Kontakte und Termine, schlägt Aktionen nur vor; persönliche KI-Hinweise, Regelvorschläge aus Mail-Headern |
| **0.7.29 – 0.7.30** | 24.11. | **MCP-Server** – `postdive --mcp` verbindet Claude Code, Claude Desktop, Cursor & Co. lokal mit Postfach, Kalender und Kontakten; standardmäßig nur lesend |
| **0.7.31** | 24.11. | **Umbenennung zu PostDive** – plus Linux-Unterstützung für Wayland/Hyprland (Omarchy) |
| **0.7.32 – 0.7.35** | 25.11. | **UI-Redesign** – ruhigere Oberfläche im Stil von Apple Mail, einfaches Konto-Setup, klar getrennte Bereiche, Dialoge ohne Doppelrahmen, besserer Kontrast der Themes |
| 0.7.36 – 0.7.40 | 25.11. | *Feinschliff & Fehlerbehebungen* – Kalenderwochen, Diagnose-Log für Anmeldeprobleme (ohne Passwörter), Umlaute im Passwort, S/MIME-Signatur mit Zwischenzertifikaten |
| **0.7.41** | 25.11. | **Kontakte aufräumen** – Duplikate per Vergleich zusammenführen, Kontaktdaten per KI aus Mails vervollständigen; Wochenansicht auf der Zeitleiste der Tagesansicht |
| 0.7.42 – 0.7.44 | 25.11. | *Feinschliff & Fehlerbehebungen* – alle Kontakte statt 500, Outlook-Anführungszeichen in Namen, klarere Google-Kontakte-Fehler, Statusleiste zeigt Sync-Fehler ([#1](https://github.com/diver80/postdive/issues/1)) |
| **0.7.45** | 25.11. | **Aktivität** – Protokoll, was auf diesem Gerät mit Mails passiert ist (von dir, Regeln oder Agenten), mit Sprung zur Mail und Rückgängig |
| **0.7.46 – 0.7.47** | 26.11. | **Termine mit Teilnehmern** – Teilnehmer in Einladungen und Kalender, selbst einladen (Google mit Meet-Link, sonst per Kalender-Mail), Änderungen & Absagen an alle, Antworten werden eingetragen; „In Teams planen“ öffnet das Teams-Formular vorausgefüllt |
