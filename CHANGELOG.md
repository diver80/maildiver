# Changelog

## 0.7.6 (2026-11-22)

### Kompakte Ordner-Seitenleiste (Desktop & Handy)
- **Weniger genutzte Ordner im „Weitere Ordner“-Block einklappen:** Pro Postfach werden standardmäßig nur **INBOX** (mit Unterordnern) sowie Ordner angezeigt, in die aktive Filterregeln eingehende Mails verschieben oder kopieren. Alle übrigen Ordner (*Archiv, Entwürfe, Gesendet, Papierkorb, Spam, Vorlagen* usw.) liegen platzsparend eingeklappt unter **„Weitere Ordner (N)“** (mit Ungelesen-Zähler ohne Papierkorb/Spam) und lassen sich bei Bedarf per 1-Klick aufklappen — so sind auf Handy und Desktop alle Accounts ohne ständiges Scrollen im Blick.
- **Einstellungen & Handy-Konten:** In den Einstellungen (*Ordner in der Seitenleiste*) lässt sich die kompakte Ansicht ein-/ausschalten sowie pro Postfach manuell festlegen, welche Ordner immer sichtbar bleiben (`app.compactFolders`, `app.pinnedFolders`). Auf dem Handy sind zudem die Konto-Überschriften bei mehreren Konten direkt ein-/ausklappbar.
- **Dokumentation aktualisiert:** Kompakte Ordneransicht sowie natives OpenPGP & Regeleditor in `README.md`, `docs/ARCHITECTURE.md` und `docs/index.html` aufgenommen.

### Star/Unstar View-Refresh-Fix
- **Sofortige Aktualisierung bei Einzel- & Bulk-Star/Unstar (+ Undo):** `PerformMessageAction`, `PerformBatchAction` und `PerformUndo` aktualisieren den Markierungs- und Gelesen-Status jetzt sofort im Speicher, laden eine geöffnete Mail nach Bulk-Aktionen nach, leeren die Mehrfachauswahl nach Bulk-Star/Unstar und entfernen eine gelesene Mail beim Entsternen in **Fokus (Ungelesen & ★)** sofort aus der Fokus-Liste (kein Hängenbleiben über `keepOpenMail`).

## 0.7.5 (2026-11-21)

### Security-Hardening & OpenPGP / S/MIME
- **WKD/VKS SSRF-, Redirect- & Key-Poisoning-Schutz (`internal/pgp`):** Strikte E-Mail-/Domain-Validierung, URL-Escaping, HTTPS-only-Redirects, Blockierung privater/lokaler IPs (`newSafePGPHTTPClient`) sowie Prüfung, dass online geladene Keys keine Privatschlüssel enthalten, weder abgelaufen noch widerrufen sind und exakt zur angefragten E-Mail-Adresse passen.
- **Krypto Fail-Closed (`applyOutgoingSecurity` & `resolveOutgoingCryptoTech`):** Verschlüsselung oder explizites Signieren fällt niemals still auf Klartext oder eine andere Technologie zurück; sinnvolle Auto-Priorisierung anhand eigener Identitäten und Empfänger-Schlüssel.
- **OpenPGP-Compliance & Passphrasen:** Passphrase-Verschlüsselung privater Schlüssel im `secretstore` (inkl. optionalem Passwortfeld bei Schlüsselerzeugung), Key-Expiration-/Revocation-/Subkey-Prüfung, deterministische UserID-Reihenfolge, vollständiges Löschen aller Alias-Keychain-Einträge, `Trusted=false` bei Absender-Mismatch, MIME-dekodiertes Inline-PGP (Base64/Quoted-Printable), Sign-then-Encrypt-Prüfung (inneres `multipart/signed`), Schutz vor HTML-Spoofing bei Inline-PGP und asynchrone Key-/Zertifikatsimporte ohne UI-Blockade.

### Zuverlässigkeit, Filter-Engine & IMAP/SQLite
- **Undo Send & Draft Auto-Save:** Mehrfache gleichzeitige Undo-Send-Queue (kein Überschreiben), automatischer Flush/Draft-Backup beim Beenden der App, Wiederherstellung im Editor (oder Entwurfs-Backup) bei Sendefehlern, Session-Guard (`composeSessionSeq`) gegen Auto-Save-Races, Idle-Auto-Save-Ticker sowie nicht-blockierendes Auslesen des WebView-Editors.
- **Filter-Engine & Regeleditor:** Atomares In-Place-Umbenennen mit Erhalt der Regel-Priorität und Duplikatschutz, SQLite-Transaktionen (`BeginTx`) für `UpsertFilter`/`ReorderFilter`, Live-Filter-Unterstützung für `copyToFolder`, `junk` und Body-/Header-Prefetch bei neuen Mails, paginierter Filterlauf (>500 Mails) mit mehreren Kopierzielen, sicheres `uidExpungeIMAP` ohne blindes Löschen fremder `\Deleted`-Mails, HTML-Body-Fallback, Regex-Cache, `BeginsGrouping`/`EndsGrouping`-Auswertung, per-Account ↑/↓-Sortierung sowie Vorschau-Hinweis bei Body-/Header-/Anhang-Regeln.
- **Anhänge, `.eml` & Preview:** `UIDVALIDITY`- & 50-MB-Größenprüfung beim Body-Abruf/EML-Export, expliziter Fehler statt stiller 32-MB-Kürzung und striktes `partName`-Matching in `ExtractAttachment`, UTF-8-sicheres Kürzen von Dateinamen & Textvorschauen, Request-`seq`-Guard & Texture-Cleanup sowie Fehleranzeige und Mobil-Header-Fix im Vorschau-Modal.

### Compose WYSIWYG Polish
- **Gehärteter Paste-Sanitizer & Clipboard:** Entfernt `on*`-Handler, `javascript:`-URLs, `iframe`/`svg`/`object`/`form` und `data-mdv-*`, bevorzugt strukturierte HTML-Tabellen (Excel/Confluence) vor Begleit-PNGs und erkennt Finder-Datei-Pastes zuverlässig.
- **Editor- & Signatur-Feinschliff:** Bild-Selektion (`selImg`) wird beim Tippen/Cursorwechsel sauber aufgehoben, `⌘A`+Backspace schützt die Signatur, eigene Tabellen-Styles in Signaturen/Forwards bleiben erhalten und helle Dark-Mode-Schriftfarben werden für helle Empfänger-Postfächer bereinigt.

## 0.7.4 (2026-11-21)

### Natives OpenPGP (PGP/MIME & Inline PGP)
- **Neues reines Go-OpenPGP-Paket ([internal/pgp](internal/pgp)):** Komplette OpenPGP-Unterstützung ohne externes `gpg` (RFC 3156 `multipart/signed` & `multipart/encrypted` sowie Inline-PGP & Clearsigned-Nachrichten).
- **Schlüsselverwaltung & OS-Schlüsselbund:** Eigene Schlüssel erzeugen (RSA-3072), `.asc`/`.gpg`-Schlüssel (privat & öffentlich, inkl. Passphrase-Schutz) per Datei oder Textblock importieren, Public Keys per 1-Klick kopieren sowie Empfänger-Keys direkt über **WKD / `keys.openpgp.org`** nachladen. Private Keys liegen ausschließlich im System-Schlüsselbund (`secretstore`).
- **Lesen & Verfassen:** Automatische PGP/MIME- und Inline-PGP-Entschlüsselung & Signaturprüfung (inkl. Absender-Adressabgleich) in der Leseansicht, Compose-Auswahl (`Auto` / `S/MIME` / `OpenPGP`) sowie automatische PGP-Vorauswahl beim Antworten auf PGP-verschlüsselte Mails.

### Regeleditor & Filter-Engine
- **Filter-Matching-Fix (`matchFilterAgainstMessage`):** Übergibt jetzt auch Nachrichtentext (`body`), `cc`/`toOrCc`, Mail-Header (`header`) und Anhänge (`attachment`) an die Filter-Engine.
- **Erweiterter Regeleditor:** Lokalisierte Feld- & Operator-Dropdowns (inkl. `cc`, `toOrCc`, beliebiger Mail-Header, Anhang-Name, `isEmpty`/`isNotEmpty`, Regex `regex`/`notRegex`), Account-Ordner-Picker für *Verschieben* und *Kopieren*, Aktionen für *Als Spam markieren* & *Weitere Regeln stoppen*, Live-Treffervorschau auf geladene Mails, Anzeige erkannter Regelkonflikte (`FindFilterIssues`) sowie ↑/↓-Sortierung der Regel-Priorität.

## 0.7.3 (2026-11-21)

### Verfassen, Anhänge & Konten-Komfort
- **Draft Auto-Save:** Geänderte Entwürfe werden automatisch im konfigurierbaren Intervall (Standard 25 s; 15 s / 25 s / 60 s / Aus) still in den Entwürfe-Ordner gesichert, ohne das Compose-Fenster zu schließen.
- **Senden rückgängig machen (Undo Send):** Konfigurierbare Sendeverzögerung (Standard 5 s; Sofort / 5 s / 10 s / 20 s) mit Countdown-Leiste inkl. *„Senden abbrechen“* (stellt den vollständigen Entwurf wieder im Editor her) und *„Jetzt senden“*.
- **„Anhang vergessen?“-Warnung:** Prüft beim Senden den neu geschriebenen Text (ohne Zitate und Signatur) auf Stichworte wie *Anhang / anbei / attached*, solange keine Datei angehängt ist, und bietet direkt *„Datei wählen…“* oder *„Trotzdem senden“*.
- **Textbausteine / Vorlagen:** Wiederverwendbare Snippets (optional mit Betreff) in den Einstellungen verwalten sowie im Compose-Fenster direkt aus aktuellem Text speichern und am Cursor einfügen.
- **HTML-Signatur & Position:** Optionale eigene HTML-Signatur pro Konto (inkl. Button *„Aus Text erzeugen“* & Sanitizer) sowie Wahl der Signatur-Position bei Antworten (*Über Zitat* vs. *Unter Zitat*).
- **In-App-Anhang-Vorschau & `.eml`-Export:** Bilder, Text-/Code-/CSV-/JSON-/`.ics`-Dateien und eingebettete `.eml`-Nachrichten (`message/rfc822`) direkt in der App ansehen, PDFs schnell öffnen und komplette Mails in Leseansicht (Desktop & Phone) als `.eml` in `~/Downloads` sichern.

## 0.7.2 (2026-11-21)

### Desktop WYSIWYG Compose-Editor
- **Toolbar & Aktive Formatierung:** Optisch abgesetzte Werkzeugleiste mit deutlich erkennbaren Button-Flächen, Hover-/Aktiv-Hervorhebung (`updateFormatStates` + Absatz-Dropdown-Sync), Rückgängig/Wiederholen-Buttons (`↶` / `↷`), Trennlinie (`―`) sowie Vorausfüllung bestehender Link-URLs.
- **Bilder selektieren, skalieren & löschen:** Klick auf eingebettete Bilder im Text markiert diese mit Akzentrahmen, Drag-Handle an der unteren rechten Ecke sowie kontextueller Bild-Leiste (`25%`, `50%`, `75%`, `100%`, `−`/`+`, Pixelgröße, Löschen-Button und `Entf`/`Backspace`). Temporäre Editor-Klassen werden beim Versand sauber bereinigt.
- **Signatur- & Paste-Schutz:** Signaturen (`[data-mdv-sig="1"]`) erhalten im Dark Mode lesbaren `--muted`-Kontrast, sind im Editor gegen versehentliches Überschreiben/Hineinfallen von Bildern geschützt (`contenteditable="false"`, beim Versand entfernt) und eingefügtes HTML übernimmt durch `sanitizePastedHTML` sauber die Editor-Farben statt fremder Hintergrund-/Textfarben.

## 0.7.1 (2026-11-21)

### Zuverlässigkeit, Protokolle & Sicherheit
- **Offline-Postausgang (Outbox) UI & Lifecycle:** Postausgangs-Status (`queued` / `failed` mit Fehlermeldung, Draft-Bereinigung nach Versand, dauerhafte SMTP-Fehlererkennung), Warn-Toast „Offline – in Postausgang eingereiht“ sowie Desktop- & Phone-Postausgangs-Modal mit *Alle jetzt senden* und *Verwerfen*.
- **CalDAV & CardDAV 2-Wege-Sync:** Automatische `/.well-known/{caldav,carddav}` + `PROPFIND`-Sammlungserkennung, CalDAV Event-Writeback (`PUT` / `DELETE`), CardDAV-Adressbücher inkl. Kontakt-`PUT`/`DELETE`, Entfernung remote gelöschter Kontakte sowie UI-Event nach Sync.
- **IMAP-Stabilität:** Socket-Deadlines (`context.AfterFunc`) gegen hängende Verbindungen, Ordner umbenennen & löschen (unter Beachtung von Server-Trennern und Special-Use-Schutz), `OR (Subject, From)` + ASCII-Suchfallback bei `BADCHARSET`, verlässlicher `lastFull`-Retry nach Sync-Fehlern.
- **Verschlüsselte Secrets auf Linux & Windows:** Linux (`secret-tool`/libsecret + AES-256-GCM) und Windows (DPAPI `CryptProtectData` + AES-256-GCM) statt unverschlüsseltem `secrets.json`.
- **S/MIME & OpenPGP-Hinweis:** Signatur-Zertifikat-vs-Absender-Abgleich (`SignerAddressMismatch`), automatische S/MIME-Verschlüsselung beim Antworten falls Empfängerzertifikat vorliegt, Identitätsbereinigung beim Kontolöschen sowie sauberer PGP/MIME Phase-2-Hinweis ohne irreführenden S/MIME-Importdialog.
- **KI, Archiv & Konfig:** `gemini-2.5-flash` Standardmodell, `<maildiver_instructions>` Prompt-Wrapper, `.maildiver-index.json` (mit Legacy-Fallback).

### Desktop, Compose & Mobile Parität
- **1-Klick Newsletter-Abmeldung:** `List-Unsubscribe` (`https://`, RFC 8058 One-Click POST, `mailto:`) direkt in Desktop- & Phone-Leseansicht.
- **Compose-Feinschliff:** Kontakte-Auswahl (`Kontakte…`) direkt am *An*-Feld, `cid:`-Inline-Bilder in HTML-Weiterleitungen & Entwürfen im WYSIWYG-Editor, Live-Theme-Aktualisierung im offenen Editor, quoted-Komma-Fix in Autocomplete (`"Nachname, Vorname"`).
- **Kalender-Erinnerungen & Layout:** Erinnerungen ignorieren ausgeblendete Kalender; Seitenleiste, Copilot, Benachrichtigungen und Splitter-Breiten bleiben persistent gespeichert.
- **Phone / Android Parität:** Sprache, Design & HTML-Einfärbung in den Phone-Einstellungen, „Auf Server suchen“, „Alle gelesen“, „Papierkorb leeren“, „Neuer Ordner…“, Kalender-Suche & -Filter, Postausgang-Zugriff, Stern/Quick-Rule/Abmelden im Aktionsmenü sowie Outbox-Flush & Kalender-Sync im Android-WorkManager.

## 0.7.0 (2026-11-21)

- **Android & Phone:** Long-Press Mehrfachauswahl (Stern, Gelesen, Archiv, Löschen), Mobile-Fixes für Kontakte, Kalender, Anhänge und Verfassen.
- **Fokus & Markieren:** Ansicht *Fokus (Ungelesen & ★)* hält gesternte Mails oben angeheftet; Stern-Aktionen in Liste, Leiste und Batch-Menü.
- **Desktop HTML-WYSIWYG-Editor:** Rich-Text-Editor mit Copy & Paste von Inline-Bildern (`cid:` `multipart/related`), bearbeitbaren HTML-Tabellen, Drag & Drop sowie Zwischenablage-Anhängen.
- **Lokale KI & Ordner-Persistenz:** Live-Modell-Dropdown in Einstellungen & Copilot (u. a. Ollama mit `inkling-2-mini`), dauerhaft gespeicherte Ordner- & Konto-Klappzustände.

## 0.6.1 (2026-11-21)

- **Keine falschen Sync-Fehler mehr:** Ein manueller Abgleich (Ziehen zum Aktualisieren, Sync-Knopf, erster Abgleich eines neuen Kontos) läuft im Hintergrund zu Ende, auch wenn er länger dauert als die Anzeige wartet; dann erscheint „Synchronisierung läuft im Hintergrund weiter“. Ein abgebrochenes Warten – oder eine von Android beendete Hintergrund-Aufgabe – markiert das Konto nicht mehr mit „context deadline exceeded“.
- **Android:** Die Einstellungen zeigen keinen Thunderbird-Import-Reiter mehr; die Reiter passen in die Bildschirmbreite.

## 0.6.0 (2026-11-20)

### Desktop
- **Tabellen in Mails:** „▦ Tabelle“ im Verfassen-Fenster übernimmt eine kopierte Tabelle (Confluence, Excel, Webseite) mit ihren Links, sonst eine leere Vorlage. Gesendet wird eine echte HTML-Tabelle; Links `[Text](https://…)` werden klickbar. Empfangene Datentabellen erscheinen in der Leseansicht als Tabelle, Layout-Tabellen von Newslettern bleiben Text.
- **Unterhaltung öffnen in…** (Knopf an der Mail): Claude Code oder der Antigravity-Agent starten im Terminal mit der Unterhaltung als Markdown-Datei und fassen sie zusammen; Antigravity (App) öffnet die Datei; für Claude (App) und alles andere liegt sie als Markdown in der Zwischenablage. Älteste Mail zuerst, Zitate früherer Mails weggelassen, verschlüsselte Mails ohne Inhalt. Die Dateien liegen nur für dich lesbar im Zustandsordner und werden nach einem Tag gelöscht. Auf dem Telefon: „Unterhaltung teilen…“.

### Android
- **Telefon-Ansicht:** ein Bildschirm nach dem anderen – Postfächer, Mail-Liste, Mail – mit der Zurück-Geste; Wischen zum Archivieren, Löschen und Gelesen-Markieren; Ziehen zum Aktualisieren; Verfassen im Vollbild. Erscheint auf Android und in Desktop-Fenstern unter 640 px Breite; Dialoge passen sich der Bildschirmbreite an.
- **Konten per QR-Code übernehmen:** „Vom Computer übernehmen (QR-Code)“ öffnet die Kamera des Systems (ohne Kamera-Berechtigung) und liest den Übertragungscode, den der Computer unter *Einstellungen → Geräte-Übertragung* zeigt.
- **Neue Mails im Hintergrund:** Android weckt MailDiver etwa alle 15 Minuten (mit Netz); neue Mails im Posteingang erscheinen als Benachrichtigung – jede einmal, und keine, die du in der geöffneten App schon gesehen hast.
- **Passwörter im Android-Keystore:** Zugangsdaten liegen in einer Datei, die mit einem vom Android-Keystore geschützten Schlüssel (AES-256-GCM) verschlüsselt ist; eine unverschlüsselte Datei früherer Builds wird übernommen und gelöscht.
- Links und Anhänge öffnen über das System.

## 0.5.0 (2026-11-20)

### Umbau: MailDiver – eigenständig, ohne Thunderbird
OmaThunder heißt jetzt **MailDiver** und ist ein eigener Mail-Client: Er spricht IMAP und SMTP direkt und hält eine lokale SQLite-Kopie mit Volltextsuche. Thunderbird wird nicht mehr gebraucht.
- **Entfernt:** Thunderbird-Add-on, Native-Messaging-Host, lokale HTTP-Bridge (`bridge.json`, Token, SSE) und `docs/BRIDGE.md`. Mail-Engine, KI und Oberfläche laufen in einem Prozess (`maildiver` / `MailDiver.app`) und reden direkt miteinander – schneller und deutlich weniger Code.
- **Konten:** IMAP/SMTP nur über TLS oder erzwungenes STARTTLS, Anmeldung per SASL PLAIN (Umlaute im Passwort funktionieren) mit Rückfall auf LOGIN. Passwörter liegen im macOS-Schlüsselbund bzw. unter Linux in einer nur für dich lesbaren Datei. Neue Mails kommen per IMAP IDLE sofort an, ohne IDLE alle 45 Sekunden.
- **Thunderbird-Import** liest ein Profil nur lesend und übernimmt Konten, Signaturen, Kalender, Kontakte und Filterregeln (ohne Passwörter).
- **Konfiguration:** `bridge.*` entfällt; ältere Dateien laden weiter. Einstellungen liegen unter `~/Library/Application Support/maildiver` bzw. `~/.config/maildiver`.

### Neu
- **Unterhaltungen, neueste Mail oben:** Eine Mail öffnet sich in ihrer Unterhaltung – neuere Mails darüber, ältere darunter, zugeklappt auf Absender, Datum und Textanfang, bis du sie anklickst. Listenzeilen zeigen, wie viele Mails ihre Unterhaltung hat. Gruppiert wird über `References`/`In-Reply-To`, auch wenn Antworten vor der Mail ankommen, auf die sie antworten; deine gesendeten Antworten gehören dazu, Kopien im Papierkorb nicht. Bestehende Mails werden beim ersten Start einmal neu gruppiert.
- **KI sieht die ganze Unterhaltung:** *Zusammenfassen* und *Entwurf* bekommen bis zu 20 Mails der Unterhaltung in Lesereihenfolge statt nur der geöffneten Mail.
- **Eigene Desktop-Fenster für Verfassen & Termine:** *Neue E-Mail / Antworten / Weiterleiten* und *Neuer Termin / Termin bearbeiten* öffnen sich auf dem Desktop als eigene native Fenster mit gemerkter Position/Größe, damit das Hauptfenster beim Schreiben weiter lesbar bleibt.
- **Kalender-Verbesserungen:** Kontextbezogene Kalendersuche, Mehrfach-Kalenderfilter (persistent), Google-Kalender-OAuth2-PKCE inkl. Cloud-Console-Hilfslinks, aufklappbare Agenda-Termine mit voller Beschreibung & direkten 1-Klick-Beitreten-Buttons (Teams, Meet, Zoom, Webex, Jitsi) sowie Detail-Modal in Monats-/Wochenansicht.
- **Datenschutz & Komfort:** Dauerhafte Absender-Allowlist für externe Bilder (pro Absender im Lesebereich & in den Einstellungen), vollständiger Regel-Editor für bestehende und neue Filterregeln (inkl. 1-Klick-Vorlagen), Start-Ordner-Collapse pro Postfach, globaler Web-Vorschau-Schalter (standardmäßig aus), moderne `multipart/alternative`-HTML-Signaturen mit Rich-Text-Formatierungsleiste (`B`, `I`, `</>`, Liste) sowie klar sichtbare Text-Selektion in allen Themes.
- **S/MIME (Signieren, Prüfen, Verschlüsseln & Entschlüsseln):** `.p12`/`.pfx`-Import (private Schlüssel ausschließlich im OS-Schlüsselbund), Empfänger-Zertifikatsverwaltung (`.cer`/`.pem`/`.crt`), automatische Signaturprüfung & Entschlüsselung beim Lesen sowie Signieren & AES-256-CBC-Verschlüsselung beim Senden.
- **Gmail & Microsoft 365 XOAUTH2 + CalDAV / CardDAV:** SASL `XOAUTH2` für IMAP & SMTP mit Browser-PKCE-Flow und automatischem Token-Refresh; CalDAV-Kalender- und CardDAV-Kontakt-Sync (z. B. Nextcloud, Fastmail, Radicale).
- **Offline-Postausgang & verschlüsselter LAN-QR-Code-Transfer:** Bei Netzwerkausfall werden gesendete Mails in einer lokalen SQLite-Outbox gepuffert und alle 45 s automatisch erneut zugestellt. In den Einstellungen überträgt ein einmaliger, 3 Minuten gültiger AES-256-GCM-QR-Code Konten, Zugangsdaten & Einstellungen direkt im WLAN auf ein weiteres Gerät oder Android.
- **Archiv & Triage-Scan gehärtet:** Archiv-Kandidatensuche nutzt direkte SQL-Datums- und Statusfilter (findet alte Mails auch bei >1.000 Mails im Ordner, ignoriert Mails ohne Datum, schützt vor Symlinks); Triage-Scan sammelt ungelesene Mails zuerst, übergibt `List-Unsubscribe`-Header für 0-Token-Stage-0-Erkennung und hält verschlüsselte S/MIME/PGP-Inhalte strikt von KI-Aufrufen fern.
- **Projektseite (EN & DE):** Zweisprachige GitHub-Pages-Seite unter `docs/index.html`.

### Alltagstauglich
- **Alle Mails erreichbar:** Ordner und Suche reichen bis zur ersten Mail zurück, nicht mehr nur 30 Tage. Die Liste lädt beim Scrollen jeweils 100 weitere Mails nach, und neue Mail setzt sie nicht auf die erste Seite zurück.
- **Ganzes Postfach statt der neuesten 250:** MailDiver holt ältere Mails im Hintergrund nach, 1000 pro Ordner und Durchgang, bis das Postfach vollständig ist; auf dem Server gelöschte ältere Mails verschwinden auch lokal. Flags werden für die neuesten 500 Mails bei jedem Abgleich verglichen.
- **Änderungen von anderen Geräten sofort:** gelesen/Stern vom Handy kommen per IDLE an statt nach bis zu 15 Minuten. Server ohne IDLE gleichen alle Ordner regelmäßig ab, nicht nur den Posteingang.
- **„Neue Mail“ nur für neue Mails:** alte Mails, die nach einem Löschen ins Abgleichsfenster rutschen, lösen keine Meldung und keine Filter mehr aus.
- **Nichts geht beim Schreiben verloren:** Esc, ein Klick neben das Fenster oder ✕ schließen eine geänderte Mail nicht mehr, sondern fragen: *Als Entwurf speichern*, *Verwerfen* oder *Weiter schreiben*.
- **Absendername:** Neues Feld *Dein Name* im Konto – er steht als Absender in deinen Mails. Das bisherige Feld heißt jetzt *Kontoname*.
- **Antworten** zitieren die Originalmail („Am … schrieb …:“) unter deinem Text, gehen an die `Reply-To`-Adresse, falls die Mail eine nennt, und machen aus „AW:“ kein „Re: AW:“.
- **Empfänger wie „Müller, Hans <h@x.de>“** (Komma im Namen, ohne Anführungszeichen) werden richtig erkannt; eine ungültige Adresse verhindert das Senden mit Hinweis, statt still wegzufallen.
- **Löschen:** Mails im Papierkorb werden nur nach einem zweiten Druck endgültig gelöscht, und die Meldung sagt „Endgültig gelöscht“. Ohne bekannten Papierkorb wird nichts mehr endgültig gelöscht. Fehlt ein Archiv-Ordner, legt das erste Archivieren ihn an.
- **Fehler sichtbar:** Ein Abgleich-Fehler (falsches Passwort, TLS) steht in der Statusleiste („Sync-Fehler: Konto“, Klick öffnet die Konten); kann die gesendete Mail nicht in *Gesendet* abgelegt werden, sagt die Meldung das.
- **Entwürfe vollständig:** Erneutes Speichern ersetzt den bisherigen Entwurf statt Kopien anzuhäufen; nach dem Senden wird der Entwurf automatisch entfernt; beim Wiederöffnen bleiben BCC und Anhänge erhalten.
- **Weiterleiten mit Anhängen & HTML:** Original-Anhänge werden beim Weiterleiten automatisch übernommen (einzeln abwählbar); bei HTML-Mails bleibt optional die Original-Formatierung als `multipart/alternative` (Text + HTML) erhalten.
- **Bilder im Lesebereich:** Eingebettete Bilder (`cid:` / `data:image/`) erscheinen direkt und ohne Netzwerkanfrage; für externe Bilder gibt es pro Mail den Schalter *Bilder anzeigen* / *Ausblenden* (standardmäßig blockiert).
- **Suche auf dem IMAP-Server:** Über *Auf Server suchen* durchsucht MailDiver per IMAP `SEARCH TEXT` auch ältere Mails, deren Text noch nicht lokal geladen ist.
- **Schnelle Sammelaktionen & Rückgängig (`⌘Z`):** Mehrere Mails (Löschen, Archivieren, Verschieben, Gelesen, Stern, Filterlauf) werden pro Konto über eine einzige IMAP-Verbindung gebündelt; rückgängig machbare Aktionen lassen sich per Leiste oder `⌘Z` direkt zurücknehmen.
- **Komfort:** Ungelesen-Zähler pro Ordner in der Seitenleiste, *Alle als gelesen markieren*, `⌘↩` zum Senden im Verfassen-Dialog, *Mail drucken* (`⌘P`), `mailto:`-Links direkt in MailDiver öffnen (inkl. macOS URL-Handler) sowie Absender-Aliase pro Konto.
- **Sicherer Datenbank-Speicherort:** `maildiver.db` (inkl. lokalem Kalender, Kontakten & Filtern) liegt jetzt im persistenten Datenordner (`~/Library/Application Support/maildiver` unter macOS bzw. `~/.local/share/maildiver` unter Linux) statt im Caches-Ordner; vorhandene Datenbanken werden beim Start automatisch migriert.

### Schneller
- Mail-Listen laden nur noch Kopfzeilen; Texte werden erst beim Öffnen gelesen.

### Sicherheit
- „Nur lesen“ wird jetzt auch unterhalb der Oberfläche durchgesetzt: Mail-Aktionen, Entwürfe, Senden und Filterregeln werden abgelehnt, auch wenn sie vom Copilot oder einem Job kommen.

## 0.3.1 (2026-11-18)

### Fixes
- **Geöffnete Mails werden als gelesen markiert**, wie in Thunderbird. Einstellbar unter ☰ → *Einstellungen* → *Lesen*: beim Öffnen (Standard), nach 3 Sekunden oder nie. „Als ungelesen markieren“ (`u`) bleibt bestehen, auch wenn die Mail danach neu geladen wird. In der Ansicht *Fokus (Ungelesen)* bleibt die offene Mail an ihrer Stelle, damit `j`/`k` von dort weitergehen. Bei „Nur lesen“ oder nicht freigegebenen Konten bleibt die Mail ungelesen, ohne Fehlermeldung.

## 0.3.0 (2026-11-18)

### Umbau: App statt Kanban-Board
Die OmaThunder-App ist jetzt die Oberfläche; das Thunderbird-Add-on ist nur noch Bridge und Omarchy-Theme, der Hilfsprozess erledigt die automatische Arbeit.
- **Entfernt:** Kanban-Board (Space-Tab), Spaces-Regeln mit Auto-Verschieben, KI-Popup an der Mail und im Schreibfenster, Standalone-KI ohne Hilfsprogramm, Kontextmenüs. Verschieben nach Regeln übernehmen Thunderbird-Filter (*Quick-Regel* in der App). Ein Board in der App ist für nach 1.0 vorgemerkt.
- **Einstellungen aufgeteilt:** In der App (☰ → *Einstellungen*, `⌘,`) liegen KI-Anbieter, Modelle, API-Schlüssel, Triage-Scan- und Archiv-Optionen. Im Add-on bleiben die Rechte (Nur lesen, freigegebene Konten, Bridge, Senden, Module) sowie Theme und Sprache – so kann sich die App ihre Rechte nicht selbst geben.
- **API-Schlüssel** trägst du in der App ein; sie liegen weiter in der `env`-Datei (`0600`) und gelten sofort, ohne Thunderbird-Neustart. In Thunderbird gespeicherte Standalone-Schlüssel werden nicht übernommen.
- **Konfiguration:** `rules`, `mailbox.kanbanAccountIds`, `mailbox.kanbanFolders` und `mailbox.autoMoveOnlyUnread` entfallen; ältere Dateien laden weiter und verlieren diese Schlüssel beim nächsten Speichern. Triage-Scans umfassen die Posteingänge der freigegebenen Konten.
- **Protokoll 4:** Add-on und Hilfsprogramm müssen dieselbe Version haben – nach dem Update die neue `.xpi` installieren.

### Neu
- **Native App `omathunder ui` / `OmaThunder.app`** (eigenes Programm `cmd/omathunder-ui`): Mail-Cockpit, Kalender, Kontakte, Quick-Regeln und KI-Copilot, verbunden über die lokale Bridge. Der Native-Messaging-Host enthält keinen GUI-Code mehr.
- **Lokale Bridge** (HTTP + SSE auf `127.0.0.1`, Token in `bridge.json`): standardmäßig **aus**; einschalten per App-Button, Add-on-Einstellungen oder `omathunder bridge enable`. Der Host öffnet/schließt den Port ohne Thunderbird-Neustart. Doku: `docs/BRIDGE.md`.
- **Kalender-Experiment `omaCalendar`** und Filter-Upsert für Quick-Regeln.
- **App-Layout aufgeräumt:** schlanke Toolbar mit ☰-Menü (Sprache, Design, Meldungen, Seitenleiste, Hilfe), Fußleiste mit Verbindung, Auswahl, Konto, Sync-Zeit und den gerade passenden Tastenkürzeln; Seitenleiste ein-/ausblendbar.
- **Tastatur-Bedienung** wie in Mail-Clients üblich: `j`/`k` nächste/vorige Mail, `shift+j`/`k` scrollen, `x` markieren, `e` archivieren, `d` Papierkorb, `v` verschieben, `s` Stern, `u` gelesen, `r` antworten, `q` Quick-Regel, `esc` zurück, `/` Suche, `c` neue Mail, `n` neuer Termin, `h` Hilfe. Einzeltasten wirken nie in Eingabefeldern oder hinter Dialogen; `d` auf mehrere markierte Mails braucht einen zweiten Druck. Nach Archivieren/Löschen/Verschieben öffnet sich die nächste Mail.
- **Ordnerbaum** in der Seitenleiste: ein Abschnitt pro Konto, Unterordner eingerückt und einklappbar, Symbole für Spezialordner; keine Begrenzung auf 18 Ordner mehr.
- **Kompaktere Mail-Liste:** zwei Zeilen pro Mail (Absender + Zeit, Betreff + kleine Tags); der Ordner-Tag erscheint nur in ordnerübergreifenden Ansichten.
- **Archivieren und „Verschieben nach…“** in App und Bridge (`messages.actions` mit `archive: true`; Ordnerauswahl mit Filter, ↑/↓ und Enter, nur innerhalb des Kontos).
- **Verschlüsselte und signierte Mails:** Der Lesebereich entschlüsselt die geöffnete Mail und sagt es („Verschlüsselt. KI-Aktionen auf diese Mail senden den entschlüsselten Text an den KI-Anbieter.“). Kann Thunderbird sie nicht entschlüsseln, steht der Grund da statt „(Kein Textinhalt)“, mit „In Thunderbird öffnen“. Signierte Mails sind als signiert markiert – die Signatur wird nicht geprüft, „In Thunderbird prüfen“ zeigt, ob sie gültig ist. Durchgestrichener Text bleibt durchgestrichen, ausgelassene Bilder zwischen Wörtern lassen ein Leerzeichen, und Links direkt hintereinander („Manage Notifications · Give feedback“) sind getrennt.
- **Signatur beim Senden:** Der Schreib-Dialog hat „Signatur: Standard / Signieren / Ohne“ (Standard = Einstellung der Identität in Thunderbird); die Erfolgsmeldung nennt, ob signiert/verschlüsselt wurde. Antworten auf verschlüsselte Mails werden immer verschlüsselt; ein stiller Entwurf davon öffnet stattdessen das Thunderbird-Fenster.
- **Triage-Scan und Archiv laufen im Hilfsprozess:** Der Go-Hilfsprozess (läuft, solange Thunderbird läuft) scannt die Posteingänge der freigegebenen Konten – auf Knopfdruck in der App (☰ → *Triage-Scan starten*) oder bei neuer Mail (`mailbox.scanOnNewMail`) – und führt *Archiv & Aufräumen* aus (☰ → *Archiv & Aufräumen…*: Vorschau, Simulieren, Backup, Backup + Lokale Ordner, Backup + Löschen beim Anbieter mit zweitem Klick; gelöscht/verschoben wird nur nach SHA-256-geprüftem Backup). Bridge: `POST /v1/triage/scan`, `/v1/archive/preview`, `/v1/archive/run` und Ereignisse `triage.scan.*`, `archive.*`. Die Add-on-Seite liefert dafür nur noch Mails über eigene Host-Methoden, die Bridge-Clients nicht aufrufen können.
- **Formatierte Mail-Ansicht:** HTML-Mails erscheinen mit Überschriften, Absätzen, Listen, Zitaten, Fettschrift und Links, die ihren Titel statt der Tracking-Adresse zeigen (die Adresse steht im Tooltip). Versteckter Vorschautext, Skripte und Bilder fallen weg; externe Inhalte werden nie geladen, die Zahl ausgelassener Bilder steht über der Mail. Nur-Text-Mails bekommen kurze Links statt langer `[https://…]`-Blöcke und Zitatblöcke. Umschalter „Formatiert / Text“; die KI-Funktionen nutzen weiter die Textfassung.

### Sicherheit
- **Kein Klartext verschlüsselter Mails in automatischen Wegen:** Thunderbird entschlüsselt für Add-ons standardmäßig. Die Bridge entschlüsselt jetzt nur auf ausdrückliche Anforderung (`?decrypt=1`, nur Lesebereich und KI-Aktionen auf die geöffnete Mail). Triage-Scans, der Scan bei neuer Mail und das `get_message`-Werkzeug des Copilots bekommen verschlüsselte Mails ohne Text und Anhänge (die KI sieht „[encrypted email: content not available]“); Thunderbird fragt dabei auch nicht nach Passwort oder PIN.
- Zentrale Policy-Tabelle in `extension/lib/core.js` für alle Bridge-Aufrufe: Lesen optional auf `bridge.accountIds` begrenzt, Schreiben nur mit Read-only aus und freigegebenem Konto, Senden zusätzlich mit `bridge.allowSend`; geprüft wird das Konto der tatsächlich verwendeten Absender-Identität.
- Quick-Regeln überschreiben keine bestehenden Filter mehr, werden hinten angehängt, wenden die Aktion nur auf echte Treffer an, behandeln Freemail-Domains als volle Adressen und verlangen bei breiten Mustern eine zweite Bestätigung. Bridge-Filter können nie löschen.
- Copilot schlägt Entwürfe, Termine und „gelesen“ nur vor; ausgeführt wird erst nach Klick. Mailinhalte gehen als nicht vertrauenswürdige Daten an das Modell, bei CLI-Backends nur per stdin.
- KI-Endpoints kommen nur aus `config.json`; API-Keys gehen nur an den Host des jeweiligen Providers (`openaicompat` nutzt `OPENAICOMPAT_API_KEY`). Unbekannte Backend-Namen werden abgelehnt statt still auf Gemini umzuleiten.
- Mail-Aktionen prüfen `expectedMessageId`; mehrdeutige Message-IDs werden abgelehnt. Senden/Entwürfe sind per `idempotencyKey` gegen Doppelausführung geschützt; Timeouts schreibender Aufrufe melden `outcome_unknown`.

### Fixes
- Löschen/Ändern eines einzelnen Serientermins betrifft nicht mehr die ganze Serie (`recurrenceId` + `scope`).
- Desktop-Mitteilungen bei neuer Mail funktionieren; die App verbindet sich nach einem Thunderbird-Neustart selbst neu.
- Data Races und möglicher Absturz in der App behoben; lange KI-Aufrufe brechen nicht mehr nach 35 s ab.
- `OmaThunder.app` meldet die richtige Version; `bridge.json` wird nur vom eigenen Host gelöscht.
- Ein KI-Stack (Provider-Registry auf any-llm-go) für Klassifikation, Analyse und Chat; robusteres JSON-Parsing.

## 0.2.9 (2026-11-16)

### Features & Improvements
- **Ebene-1-Unterordner im Kanban (Einstellungen + Sidebar + Scan)**:
  - Unter *Konten & Triage* lassen sich pro Konto `Inbox` sowie alle direkten Ebene-1-Unterordner (inkl. *Alle* / *Nur Inbox*) gezielt für das Kanban-Board aktivieren (`mailbox.kanbanFolders`).
  - Aktivierte Ebene-1-Ordner erscheinen verschachtelt mit Open-Count unter dem Konto in der Sidebar: Klick auf das Konto zeigt alle aktivierten Ordner gemeinsam, Klick auf einen Ordner filtert gezielt auf diesen Ordner (inkl. Unterordner).
- **Alle ungelesenen Mails laden & rekursiver Ebene-2-Scan**:
  - Ungelesene Mails in aktivierten Ordnern werden ab sofort vollständig ohne `scanLimit`/`scanDays`-Deckelung geladen (`selectScanMessages`); die Grenzen gelten nur noch für gelesene Mails.
  - Ebene-2+-Unterordner werden rekursiv gescannt und Ordner wie `todo`/`action`, `waiting`/`wartet` sowie `done`/`erledigt` automatisch in die passende Kanban-Spalte einsortiert (`FolderKanbanColumn`), während neutrale Unterordner (z. B. `Reise`) regulär klassifiziert werden. Das Board-Limit wurde auf 800 Karten erweitert.
- **Volle Spalten-Dropzone & Direkt-Multiselect per `Shift+Click`**:
  - Alle 5 Kanban-Spalten füllen die gesamte Board-Höhe und reagieren ohne Flackern über die volle Höhe als Drag-and-Drop-Zielzone.
  - `Shift+Click` (Bereich) und `Cmd/Ctrl+Click` (einzeln) funktionieren direkt auf den Karten ohne Textselektion und ohne die Checkbox treffen zu müssen (die zudem auf `15px` vergrößert wurde).

## 0.2.8 (2026-11-16)

### Features
- **Kanban-Multiselect & Batch-Aktionen**:
  - **Mehrfachauswahl im Board**: Karten lassen sich einzeln über die Karten-Checkbox oder `Cmd/Ctrl + Klick`, als Bereich innerhalb einer Spalte per `Shift + Klick` sowie spaltenweise über die Header-Checkbox auswählen (`Esc` hebt die Auswahl auf).
  - **Multi-Drag & Drop**: Zieht man eine ausgewählte Karte in eine andere Spalte (z. B. *Erledigt*), werden alle ausgewählten Karten gemeinsam per atomarem Batch-RPC (`move_kanban_batch` / `omathunder:moveKanbanBatch`) verschoben.
  - **Batch-Leiste für ausgewählte Karten**: Direkte Spalten-Buttons sowie **`✓ Erledigt & Gelesen`** (verschiebt nach *Erledigt*, markiert in Thunderbird als gelesen und räumt direkt vom Board) und **`✓ Gelesen`** (`omathunder:markCardsRead`).

## 0.2.7 (2026-11-15)

### Fixes & Improvements
- **Seitenleisten-Zähler zählen nur offene Spalten**: *Kanban Board* und die Postfach-Untereinträge zeigen ab sofort ausschließlich offene Karten (`Incoming + Action Needed + Waiting + Invoice / Event`) ohne *Erledigt* (`isOpenKanbanCard`).
- **Gelesene „Erledigt“-Mails automatisch aufräumen**:
  - Scans überspringen bereits gelesene *Erledigt*-Karten und entfernen ehemals ungelesene *Erledigt*-Karten, sobald sie in Thunderbird gelesen wurden (`SaveScanBatch`, `pruneStaleReadDone`). Manuell in offene Spalten gepinnte Karten bleiben erhalten.
  - Der Button **✓ Gelesen / ✓ Read** markiert Mails in Thunderbird als gelesen und räumt gelesene *Erledigt*-Karten über `remove_classifications` direkt vom Board.
- **Live-Aktualisierung des Boards & Auto-Sync-Schalter**:
  - Neue Mails lösen nach kurzem Debounce (3,5 s) automatisch einen gezielten Konto-Scan aus (`scanOnNewMail`, neuerdings standardmäßig aktiv und direkt als **Auto-Sync: AN/AUS**-Schalter in der Kanban-Filterleiste umschaltbar).
  - Gelesen-Markierungen (`onUpdated`) sowie Löschen / Verschieben in Papierkorb oder Junk (`onDeleted`, `onMoved`) aktualisieren das geöffnete Board sofort live über `omathunder:stateChanged` (plus `visibilitychange`-Refresh beim Zurückwechseln).

## 0.2.6 (2026-11-15)

### Features
- **Standalone-Modus (AI & Themes ohne Go-Host)**: Die Thunderbird-Extension funktioniert jetzt direkt ohne installierten Go-Host (Store-taugliche Basis):
  - **KI-Assistent direkt per HTTP/REST**: Zusammenfassen (einzeln & Multi-Mail), Rückfragen, Antwort formulieren, Entwurf verbessern/prüfen/übersetzen, Termine als `.ics` extrahieren und Filterregeln vorschlagen über **Gemini API**, **Anthropic Claude API** oder **lokales Ollama** (`extension/lib/standalone.js`).
  - **Lokale API-Keys in den Einstellungen**: Gemini- und Anthropic-Keys lassen sich direkt in Thunderbird (`storage.local`) unter *KI & Backends* hinterlegen.
  - **Alle 22 Omarchy-Themes live**: Vollständige Theme-Gallery, Popup-Schnellwahl sowie Chrome-/Body-/Compose-Styling laufen auch im Standalone-Modus ohne Host; Kanban-Board, Stage-0-Triage, CLI-Backends und `.eml`-Archiv schalten sich automatisch hinzu, sobald der optionale Go-Host verbunden ist.
- **Kanban-Untereinträge pro Postfach**: In der Seitenleiste erscheinen unter *Kanban-Board* nun eigene Untereinträge pro aktiviertem Kanban-Konto (mit Karten-Zähler) zum gezielten Filtern nach Postfach sowie gezielten Scannen des ausgewählten Kontos.
- **Einklappbarer KI-Drawer im Kanban & Proposal-Filterung**: Der KI-Assistent ist auf das Kanban-Board fokussiert und standardmäßig eingeklappt (öffnet sich beim Klick auf eine Karte oder den Topbar-Button); Proposals werden nach aktivem Board/Konto gefiltert und lassen sich einzeln oder komplett verwerfen (`dismiss_proposals`). Popup-Aktionen erzeugen keine verwaisten Board-Proposals mehr (`ephemeral: true`).
- **Bulk „✓ Gelesen“ in der Erledigt-Spalte**: In der *Erledigt*-Spalte lassen sich alle dort gelisteten Mails per Klick in Thunderbird als gelesen markieren (beachtet den Read-only-Schutz).

## 0.2.5 (2026-11-15)

### Breaking Changes
- **Kunden -> Spaces**: Die bisherige Kunden-Zuordnung (`customer`) wurde generisch in **Spaces** (`space`) umbenannt, damit neben Kunden auch allgemeine Themenbereiche (z. B. *Rechnungen*, *Mobilfunk*, *Stromanbieter*, *Private Postfächer*) sauber abgebildet werden können.
  - Betroffene JSON-Felder in `config.json` und `triage-state.json`: `rules[].customer` → `rules[].space`, `classifications[].customer` → `classifications[].space`.
  - Betroffene Native-Messaging-Methode: `suggest_customer_imports` → `suggest_space_imports`.
  - Bestehende Regeln oder gecachte Klassifikationen mit dem alten Feldnamen müssen ggf. einmalig neu angelegt oder gescannt werden.
- **Optionales Kanban pro Postfach (`mailbox.kanbanAccountIds`)**: Konten für Spaces/Regeln (`mailbox.triageAccountIds`) und Konten für das Kanban-Board (`mailbox.kanbanAccountIds`) werden jetzt getrennt konfiguriert. Nach dem Update muss in den Einstellungen explizit angehakt werden, welche aktivierten Konten zusätzlich im Kanban-Board erscheinen sollen.

### Features
- **Thunderbird-Filter-Engine als Unterbau**: Diagnose von nativen Thunderbird-Filtern (inkl. UND/ODER- und Feld-Checks), 1-Klick-Reparatur, Übernahme bestehender Filter als Space-Overlay und Sync aus OmaThunder-Regeln.
- **Archiv & Cleanup-Modul**: Go-gestützter, deduplizierter und SHA256-verifizierter `.eml`-Export (`YYYY/MM`-Ordnerstruktur für `Incoming` bzw. sortierte Ordner), wahlweise als Dry-Run (`Simulate`), reines Backup, Backup + Verschiebung in *Lokale Ordner* oder Backup + Bereinigung beim Anbieter.
