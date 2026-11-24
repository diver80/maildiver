# Changelog

## 0.7.27 (2026-11-24)

### Konten, Kalender, Regeln & S/MIME direkt in die Einstellungen integriert
- **Aufgeräumte Toolbar (Desktop & Handy):** Der separate **Konten**-Button in der oberen Desktop-Toolbar (und das zweite Konten-Icon im Handy-Header) wurde entfernt, sodass oben nur noch die täglichen Aktionen bleiben.
- **Ein zentrales Einstellungsfenster (`⌘,`):** **Konten** (inkl. der Unter-Tabs *E-Mail*, *Kalender*, *Regeln*, *TB-Import* und *S/MIME & PGP*) ist jetzt ein eigener Tab direkt in **Einstellungen**. Alle Direkteinstiege (*Konten & Kalender…* und *Filterregeln verwalten…* im ☰ Menü, Sync-Fehler-Banner, Onboarding, Regel-Assistent) springen direkt in den passenden Unter-Tab der Einstellungen.

## 0.7.26 (2026-11-24)

### Quelltext- & Voll-Header-Ansicht im Mail-Reader (+ alle Header für Filterregeln)
- **Neuer Modus „Quelltext“ (`Source`) direkt im Reader:** Direkt neben *Formatiert / Text* (und auch bei reinen Text-Mails neben *Text*) gibt es jetzt **Quelltext** – mit Umschalter zwischen **Header + Quelltext** (kompletter RFC-5322-Rohtext inkl. aller Header, ab 128 KB für flüssige Darstellung sauber gekürzt) und **Nur Header** (alle Header in Originalreihenfolge, RFC-2047-dekodiert), Live-Suchfilter (`X-`, `List-Id`, `Received`, …) und 1-Klick-**Kopieren**-Button (Desktop & Handy).
- **Alle benutzerdefinierten Header in Filterregeln nutzbar:** `Header-Feld`-Filterregeln prüfen jetzt neben den Standard-Headern auch alle übrigen RFC-5322-Header direkt aus `RawEML` (z. B. `X-Jira-FingerPrint`, `Return-Path`, `Delivered-To`, `Authentication-Results`, mehrzeilige `Received`-Header), sodass jeder in der Quelltext-Ansicht gefundene Header direkt als Regel funktioniert.

## 0.7.25 (2026-11-24)

### Atlassian-/Jira-Web-Vorschau öffnet Work Items direkt per Key
- **Direkt zum Work Item statt aufs Board:** Jira-Cloud-Benachrichtigungen führen zuerst den Projekt-Breadcrumb (`/browse/PROJ`, der aufs Kanban-Board umleitet) und erst danach den Work-Item-Link (`/browse/PROJ-123`) auf. `MatchPreviewLink` und `PickPrimaryLink` priorisieren jetzt direkte Work-Item-URLs (mit Key-Match zum Betreff, `/browse/KEY-123`, `/issues/KEY-123`, `selectedIssue=KEY-123`, JSM-Portal-/Request-Links sowie `?focusedCommentId=...`) vor generischen Projekt-/Board-/Startseiten-Links und promoten reine `/browse/PROJ`-Links bei passendem Betreff-Key.
- **Weniger Noise in der Auto-Vorschau:** Jira-/Confluence-Einstellungs- und Profil-Links (`emailPreferences`, `viewmyemailsettings`, `ViewProfile.jspa`, `Manage Notifications`, `/banner/opt-out`) werden als Noise übersprungen.

## 0.7.24 (2026-11-24)

### `.ics`-Einladungen & Anhänge aus verschlüsselten S/MIME-/PGP-Mails + gespeicherte Kalenderansicht
- **Fix: `.ics`-Einladungen in S/MIME- & OpenPGP-verschlüsselten Mails annehmen/importieren:** `ImportMessageICS` und `RespondMessageICS` entschlüsseln die Original-MIME-Nachricht (`RawEML`) jetzt on-the-fly im Speicher (ohne Klartext in SQLite zu speichern), sodass *Zusagen*, *Vielleicht*, *Absagen* und *Nur übernehmen* auch bei verschlüsselten Einladungen zuverlässig funktionieren.
- **Anhänge & Inline-Bilder aus verschlüsselten Mails:** Download, Vorschau, Weiterleiten von Anhängen und Inline-Bilder (`cid:`) greifen jetzt ebenfalls auf den im Speicher entschlüsselten S/MIME-/PGP-Inhalt zu.
- **Kalenderansicht gemerkt & Tagesansicht als Standard:** Die zuletzt gewählte Kalenderansicht (`Monat` / `Woche` / `Tag` / `Agenda`) wird jetzt in `config.json` (`calendarView`) gespeichert und beim Neustart wiederhergestellt; ohne gespeicherte Auswahl startet der Kalender standardmäßig in der **Tagesansicht**.

## 0.7.23 (2026-11-24)

### S/MIME-Entschlüsselung (macOS-Keychain-Chunking) & S/MIME-Bulk-Import für Teams
- **Fix: S/MIME-Entschlüsselung auf macOS bei großen Zertifikatsketten (z. B. SwissSign):** `/usr/bin/security -i` schneidet Eingabezeilen auf macOS still bei 4.096 Bytes ab, wodurch komplette S/MIME-Schlüsselpakete (~13 KB mit Zertifikatskette) beim Speichern im Schlüsselbund abgeschnitten wurden. `keychainStore` ([internal/secretstore/store.go](internal/secretstore/store.go)) speichert große Secrets jetzt automatisch in geprüften Chunks (`≤ 2.800 Bytes`) mit SHA-256-Prüfsumme, erkennt früher abgeschnittene Keychain-Einträge und repariert sie beim ersten Lesen selbstständig aus dem Fallback – ohne erneuten `.p12`-Import.
- **Klarere S/MIME-Fehlermeldungen:** Wenn ein eigenes Zertifikat zwar registriert ist, sein privater Schlüssel aber nicht geladen werden kann, zeigt MailDiver jetzt den konkreten Ladefehler statt fälschlich *„kein S/MIME-Zertifikat zum Entschlüsseln installiert“*.
- **Bulk-Import für S/MIME-Empfänger-Zertifikate:** Unter *Konten → S/MIME & PGP* lassen sich jetzt ganze Mitarbeiter-Verzeichnisse auf einmal importieren – per **Dateien wählen…** (Mehrfachauswahl), **Ordner wählen…** (rekursiver Ordner-Scan), Multi-Zertifikat-`.pem`-Dateien, PKCS#7-Bundles (`.p7b`/`.p7c`, inkl. automatischem Überspringen von CA-Zertifikaten) oder `.zip`-Archiven. Die Kontakt-Zertifikatsliste ist ab mehreren Einträgen scrollbar und durchsuchbar.

## 0.7.22 (2026-11-24)

- **„Bei neuer Mail automatisch scannen“ funktioniert wieder:** Neu eingetroffene Inbox-Mails (kein Nachladen älterer Mails) starten wieder einen Triage-Scan, wenn die Einstellung aktiv ist; verschlüsselte Mails werden dabei weiterhin nie entschlüsselt.
- **Handy: Tippen auf eine Benachrichtigung öffnet die Mail:** Neue-Mail-Benachrichtigungen (auch aus dem Hintergrund-Abruf) öffnen direkt die Mail, Sammelbenachrichtigungen die Liste *Fokus* und Termin-Erinnerungen den Kalender; *Zurück* führt wie gewohnt zur Liste und zu den Postfächern.
- **„Verwerfen“ löscht den automatisch gespeicherten Entwurf:** Wird eine neue Mail oder Antwort verworfen, verschwindet auch ihr per Auto-Save angelegter Entwurf aus dem Entwürfe-Ordner (auch wenn das Auto-Save gerade noch lief). Aus *Entwürfe* geöffnete Entwürfe bleiben beim Verwerfen erhalten.
- **Antwort auf eigene gesendete Mails geht an die Empfänger:** *Antworten* auf eine selbst gesendete Mail (Absender ist eine eigene Adresse oder ein Alias) adressiert die ursprünglichen Empfänger statt dich selbst; *Allen antworten* übernimmt An und CC ohne eigene Adressen.
- **Handy: „Senden rückgängig“ und „Rückgängig“ auf jedem Bildschirm:** Die Leisten erscheinen jetzt unter Postfächern, Liste und geöffneter Mail. Geht die App in den Hintergrund, werden wartende Mails sofort gesendet (bei Fehlern als Entwurf gesichert), damit Android sie beim Beenden der App nicht verliert.
- **Eingefügter Text landet an der richtigen Stelle:** KI-*Einfügen*, Vorlagen, **B**/*I*/Liste und *▦ Tabelle* fügen im Text-Editor an der Cursorposition ein, sonst oberhalb von Signatur und zitiertem Original statt darunter.
- **KI-Ergebnisse robuster:** Der Antwortentwurf wird aus dem `draft_reply`-Vorschlag gelesen, egal an welcher Stelle die KI ihn liefert; ein vorgeschlagenes Verschieben erscheint als Button *Verschieben nach …* und wird erst per Klick ausgeführt.
- **KI-Filtervorschläge reparieren bestehende Regeln:** Die KI sieht jetzt, warum vorhandene Regeln eine Mail verpasst haben (Filter-Diagnose). Schlägt sie *ODER ergänzen* oder *Ersetzen* für eine bestehende Regel vor, aktualisiert der Quick-Regel-Assistent genau diese Regel (mit Hinweis und Button *Stattdessen neue Regel*), statt immer eine neue anzulegen. Bedingungen, die nur zusammen gelten (UND), werden nie als ODER angehängt, und die Logik einer bestehenden UND-Regel bleibt unangetastet – sonst würde die Regel weit mehr Mails erfassen, als die Vorschau zeigt.
- **Benachrichtigungen in den Einstellungen:** Der Schalter für System-Benachrichtigungen steht jetzt auch unter *Einstellungen → Allgemein* (auch auf dem Handy) und gilt ebenso für Benachrichtigungen des Hintergrund-Abrufs.
- **Android: Anhänge und `.eml` über den System-Speicherdialog:** Riskante Anhänge, nicht öffnbare Dateien, *Speichern…* in der Anhang-Vorschau und *Als .eml speichern…* fragen nach dem Speicherort, statt Dateien im unerreichbaren App-Cache abzulegen. *Als .eml speichern* erscheint auf dem Handy nur noch im Direktmodus.
- **Handy: Vorherige/nächste Mail:** In der geöffneten Mail blättern ˄ und ˅ in der oberen Leiste durch die Liste (am Ende wird die nächste Seite nachgeladen). Nach *Archivieren*, *Löschen*, *Verschieben* oder *Spam* öffnet sich die nächste Mail statt der Liste – die Liste nur, wenn keine mehr da ist.
- **Handy: KI-Assistent und Tagesbriefing:** Auf der Postfach-Übersicht führen *KI-Assistent* und *Tagesbriefing* zum Assistenten-Chat in Handybreite (mit Anbieter-/Modellwahl, Schnellfragen und Aktionsvorschlägen).
- **Handy: „KI: Kontakte extrahieren“** steht jetzt auch im Menü der geöffneten Mail.
- **Handy: Ordner umbenennen und löschen:** Langes Tippen auf einen eigenen Ordner öffnet *Umbenennen…* (mit Namensdialog) und *Löschen…* (mit Rückfrage).
- **Termin-Erinnerungen pro Termin und im Hintergrund:** Im Terminformular lässt sich die Erinnerung wählen (keine, 5/10/15/30 Minuten, 1 Stunde, 1 Tag vorher; Standard 10 Minuten) – für lokale und CalDAV-Kalender (dort als `VALARM`), Google-Kalender nutzen den Standard. Auf Android erinnert der Hintergrund-Abruf auch bei geschlossener App an anstehende Termine (je Termin nur einmal, mit dem Benachrichtigungs-Schalter).
- **Terminformular mit Datums- und Zeitauswahl:** Beginn und Ende werden per Datums- und Uhrzeitfeld gewählt statt als `JJJJ-MM-TTTHH:MM` getippt; liegt das Ende vor dem Beginn, erscheint ein Hinweis und nichts wird gespeichert. Auf dem Handy ist die Wiederholung eine Auswahlliste (*Jährlich* wird nicht mehr abgeschnitten).
- **Handy: Dialoge in voller Breite:** Dialoge nutzen die ganze Bildschirmbreite. Die Konten-Liste zeigt Name und Adresse in der ersten, Status und Knöpfe in der zweiten Zeile; im Regel-Editor stehen Feld, Bedingung und Wert untereinander.
- **Handy: Zurück-Geste in Kontakten und Mehrfachauswahl:** *Zurück* schließt einen geöffneten Kontakt (zurück zur Kontaktliste) bzw. hebt zuerst die Auswahl markierter Mails auf, statt die Liste zu verlassen.
- **Rechtsklick-Menü in der Mail-Liste (Desktop):** *Antworten*, *Allen antworten*, *Weiterleiten*, *Gelesen/Ungelesen*, *Stern*, *Archivieren*, *Verschieben nach…*, *Als Spam markieren*, *Löschen* und *Quick-Regel…* direkt an der Zeile. Gehört die Zeile zur Mehrfachauswahl, gilt die Aktion für alle markierten Mails, sonst nur für diese Zeile.
- **Mails per Drag & Drop in Ordner verschieben (Desktop):** Eine Zeile – oder die Mehrfachauswahl, wenn die gezogene Zeile markiert ist – auf einen Ordner in der Seitenleiste ziehen verschiebt sie (mit *Rückgängig*); der Zielordner wird beim Darüberziehen hervorgehoben. Verschoben wird nur innerhalb desselben Kontos.
- **Entf und ⌫ löschen wie `d`:** In Liste und geöffneter Mail verschieben die Entf- und Rückschritt-Taste in den Papierkorb, mit derselben Rückfrage bei mehreren Mails und beim endgültigen Löschen – nie, während ein Eingabefeld aktiv ist. Gedrückt gehaltene Tasten zählen nicht als zweiter Druck.
- **Spam für die Mehrfachauswahl:** Die Auswahlleiste (Desktop) und das neue *Mehr*-Menü der Auswahlleiste (Handy) markieren alle gewählten Mails als Spam – je Konto in dessen Spam-Ordner, mit einem gemeinsamen *Rückgängig*.
- **„KI: Auswahl zusammenfassen“:** Fasst die markierten Mails (bis zu 30) als Briefing zusammen; das Ergebnis erscheint oben im Lesebereich bzw. auf dem Handy als Dialog. Verschlüsselte Mails werden dabei nicht entschlüsselt – die KI sieht nur Betreff und Absender, ein Hinweis nennt ihre Anzahl.
- **Vorschauzeile in der Mail-Liste:** Unter Betreff zeigt jede Mail den Anfang ihres Textes (Desktop & Handy), sobald ihr Inhalt geladen ist (die neuesten Posteingangs-Mails lädt die Synchronisation ohnehin vor). Verschlüsselte Mails bekommen nie eine Vorschauzeile. Abschaltbar unter *Einstellungen → Allgemein → Vorschauzeile in der Liste*.
- **Später senden:** Neben *Senden* bietet *Später senden…* „In 1 Stunde“, „Morgen 8:00“, „Montag 8:00“ oder eine frei gewählte Zeit (Datums- und Uhrzeitfeld). Die Mail wartet bis dahin im Postausgang (auch über Neustarts hinweg), wird zur geplanten Zeit gesendet – bei geöffneter App und auf Android im Hintergrund-Abruf – und trägt dann die tatsächliche Sendezeit. Im *Postausgang* stehen geplante Mails mit ihrer Zeit, *Jetzt senden* und *Zurück zu Entwürfen* (inkl. BCC); *Alle jetzt senden* lässt geplante Mails warten.
- **Synchronisierte Mails pro Ordner einstellbar:** Im Kontoformular legt *Mails pro Ordner synchronisieren* fest, wie viele der neuesten Mails jeder Abgleich prüft (50–2000, leer = 500). Der Wert gilt für den Posteingang, andere Ordner höchstens 200; ältere Mails werden nach und nach nachgeladen.
- **Konten pausieren:** Beim Bearbeiten eines Kontos schaltet *Dieses Konto synchronisieren* den Abruf ab. Pausierte Konten stehen in der Kontenliste als *Pausiert*, ihre bereits geladenen Mails bleiben sichtbar, und Antworten aus diesem Konto sind weiter möglich. Speichern schaltet ein pausiertes Konto nicht mehr von selbst wieder ein (nur ein aus Thunderbird importiertes Konto startet mit dem ersten eingegebenen Passwort).
- **Aliase mit eigener Signatur:** Aliase werden zeilenweise mit Name, Adresse und optionaler Signatur bearbeitet (*+ Alias hinzufügen*, Papierkorb zum Entfernen). Die Alias-Signatur ersetzt beim Schreiben von diesem Alias die Konto-Signatur; eine geleerte Signatur bleibt leer.
- **Konten aus der KI-Sortierung nehmen:** Unter *Einstellungen → KI & Sync → Triage-Scan* bestimmt *In KI-Sortierung einbeziehen* je Konto, ob Scans und Archiv-Aufträge es berücksichtigen. Neue Konten sind einbezogen; ein ausgenommenes Konto bleibt ausgenommen, normale Mail-Aktionen (z. B. als gelesen markieren) bleiben erlaubt.
- **Archivieren aus einem bestimmten Ordner:** *Archiv & Aufräumen* bietet neben *Posteingang* und *Alle Ordner* den Bereich *Ordner* mit Ordnerauswahl und *Unterordner einbeziehen* sowie das Feld *Höchstens Mails* (Standard 200, bis 1000).
- **Kalender-Abos und CalDAV/CardDAV pausieren und einfärben:** In den Kalender-Einstellungen hat jedes iCal-Abo, jeder CalDAV-Kalender und jedes CardDAV-Adressbuch einen Schalter *Synchronisieren*; Abos und CalDAV-Kalender bekommen zusätzlich eine Farbauswahl. Pausierte Quellen behalten ihre Termine bzw. Kontakte, werden aber nicht mehr aktualisiert.
- **KI-Antwortentwürfe kennen deine Termine:** *Antwort entwerfen* gibt der KI deine belegten Zeiten der nächsten 14 Tage mit (nur Datum und Uhrzeit – keine Titel, Teilnehmer, Orte oder Beschreibungen; ausgeblendete und abgelehnte Termine bleiben weg), damit vorgeschlagene Termine nicht kollidieren.
- **Aufgeräumt:** Die nie wählbare Archiv-Option „Backup + Lokale Ordner“ und ungenutzte Einstellungen (`syncDays`, `googleCalendar.calendarIds`) sind entfernt; ältere `config.json`-Dateien laden weiterhin.

## 0.7.21 (2026-11-23)

### KI-Hilfe direkt im Antwort- & Verfassen-Fenster (`✨ KI-Hilfe`)
- **Direkter KI-Assistent im separaten Verfassen-/Antwort-Fenster (Desktop & Handy):** Neuer **`✨ KI-Hilfe`**-Button direkt in der Betreff-Zeile öffnet eine kompakte KI-Leiste mit freiem Anweisungsfeld (z. B. *„kurz zusagen, ab 15 Uhr passt“*) und Anzeige des aktiven lokalen/Cloud-Modells.
- **1-Klick-Schnellaktionen:** *Antwort entwerfen* (nutzt bei Reply automatisch Originalmail + gesamten Konversations-Thread), *Verbessern*, *Kürzer*, *Formeller*, *DE ⇄ EN* (Übersetzung inkl. Betreff) und *Prüfen* (Prüfhinweise zu offenen Fragen, Tonfall & vergessenen Anhängen).
- **Saubere Übernahme & 1-Klick-Rückgängig:** *Übernehmen* ersetzt ausschließlich den eigenen Antworttext (Signatur und zitiertes Original bleiben im WYSIWYG- und Text-Editor vollständig erhalten), *Einfügen* ergänzt an der Cursorposition, und *↶ KI-Text rückgängig* stellt den vorherigen Entwurf jederzeit wieder her.

## 0.7.20 (2026-11-23)

### Korrekter `Sent`-Ordner (RFC 6154 Special-Use) & sofortige Anzeige gesendeter Mails
- **RFC-6154-Special-Use hat Vorrang (`FindFolderByType`):** Wenn ein Postfach mehrere gleichartig benannte Ordner besitzt (z. B. altes `Gesendet` neben dem offiziellen Server-Ordner `Sent` bzw. `Gesendete Objekte` mit `\Sent`-Flag), wählt MailDiver jetzt zuverlässig den vom IMAP-Server deklarierten Special-Use-Ordner (`\Sent`, `\Drafts`, `\Trash`, `\Junk`, `\Archive`) statt den alphabetisch ersten Namen.
- **Gesendete Mails sofort sichtbar (`appendToSentFolder`):** Nach dem Versand (direkt und aus dem Offline-Postausgang) wird die per `IMAP APPEND` im `Sent`-Ordner abgelegte Kopie direkt im lokalen SQLite-Cache gespeichert, sodass sie ohne Warten auf den nächsten Hintergrund-Sync sofort in *Gesendet* und im Konversations-Thread erscheint.

## 0.7.19 (2026-11-23)

### Zielkalender-Auswahl beim Annehmen von `.ics`-Einladungen (inkl. Google Calendar & CalDAV)
- **Kalender-Auswahl direkt im `.ics`-Einladungsbanner (Desktop & Handy):** Bei mehreren schreibbaren Kalendern zeigt das Einladungsbanner jetzt ein `Kalender:`-Dropdown für *Zusagen*, *Vielleicht* und *Nur übernehmen*.
- **Intelligente Vorauswahl:** MailDiver wählt automatisch den passenden schreibbaren Kalender vor (1. Kalender, in dem der Termin bereits liegt, 2. passender Kalender zur Empfänger-Mail/Domain wie `avono.de` → `Frank Hess (avono AG)`, 3. zuletzt gewählter Kalender, 4. erster externer Kalender statt lokalem `Persönlich`).
- **Direkter Sync in Google Calendar & CalDAV:** `ImportICSBytes` und `RespondMessageICS` schreiben Einladungen jetzt direkt in den gewählten Google-Kalender (via `events.import` mit `iCalUID` zur Duplikat-Vermeidung) oder per CalDAV-PUT statt immer nur in den lokalen Kalender `Persönlich`.

## 0.7.18 (2026-11-23)

### Kalender-Tagesansicht (24h-Zeitleiste) mit roter Jetzt-Linie
- **Neue Ansicht `Tag` / `Day` (`Monat | Woche | Tag | Agenda`):** Echte 24-Stunden-Zeitleiste (`00:00–24:00`, `1 px = 1 Min.`) mit proportionalen Terminblöcken, automatischer Nebeneinander-Anordnung bei überschneidenden Terminen, Ganztags-Leiste oben, direktem Klick in freie Stunden zum Anlegen neuer Termine sowie automatischem Scroll zur aktuellen Uhrzeit (heute) bzw. zum ersten Termin.
- **Minutengenaue rote Jetzt-Linie:** Bei Ansicht des heutigen Tages zeigt eine rote horizontale Linie mit Punkt und rotem Uhrzeit-Badge (`HH:MM`) die aktuelle Uhrzeit an und wandert jede Minute live mit (ohne Klicks auf darunterliegende Termine zu blockieren). Auch die Wochenansicht markiert die aktuelle Stunde des heutigen Tages mit einer roten Linie.
- **Schnellwechsel in die Tagesansicht:** Klick auf `+N weitere` im Monatsraster oder auf einen Tageskopf in der Wochenansicht springt direkt in die Tagesansicht des jeweiligen Tages.

## 0.7.17 (2026-11-23)

### Sichtbarer RSVP-Status bei Termineinladungen & keine doppelten Kalendereinträge
- **Status-Anzeige im `.ics`-Einladungsbanner (Desktop & Handy):** Einladungen zeigen jetzt direkt ein farbiges Status-Badge (z. B. `✓ Zugesagt · Frank Hess (avono AG)`, `? Vorläufig`, `✕ Abgesagt`, `Antwort offen` oder `✓ Im Kalender`) sowie einen hervorgehobenen aktiven Antwort-Button.
- **Automatischer Abgleich mit synchronisierten Kalendern:** Der RSVP-Status wird aus Google Calendar (`attendees[].responseStatus`), CalDAV (`PARTSTAT`), direkt in MailDiver gegebenen Antworten sowie dem `.ics`-Anhang ermittelt; bei bestehenden Datenbanken wird der Google-Calendar-Sync einmalig zurückgesetzt, um bestehende Einladungs-Antworten nachzuladen.
- **Keine doppelten Termine mehr in `Persönlich`:** Wenn ein Termin bereits in einem synchronisierten externen Kalender (z. B. Google Calendar) vorhanden ist, dupliziert *Zusagen* / *Vielleicht* ihn nicht mehr zusätzlich in den lokalen Kalender `Persönlich`, und der Button *Nur übernehmen* wird ausgeblendet. Bei *Absagen* wird eine eventuell vorhandene lokale Kopie aus `Persönlich` entfernt.

## 0.7.16 (2026-11-23)

### Schneller Pull-to-Refresh & genauer Fokus-Zähler
- **Deutlich schnelleres Aktualisieren (`SyncQuick` + paralleler Multi-Account-Sync):**
  - Pull-to-Refresh und `↻` synchronisieren jetzt zuerst parallel alle Posteingänge (bzw. den gerade geöffneten Ordner + INBOX) ohne redundantes `IMAP LIST` und ohne 20-Mail-Body-Prefetch bei unverändertem Postfach — der Lade-Spinner beendet sich damit nach ~1–2 Sekunden statt 20–45 Sekunden.
  - Sekundärordner & Backfill laufen direkt danach nicht-blockierend im Hintergrund weiter und aktualisieren Badges live.
  - `SyncNow` und `SyncInboxes` synchronisieren mehrere Konten jetzt parallel statt nacheinander.
- **Genauer Fokus-Badge (`focusCount` vs. `unreadCount`):** Der Badge neben **Fokus (Ungelesen & ★)** zeigt jetzt exakt die Anzahl der Mails, die beim Öffnen tatsächlich in Fokus liegen (Ungelesene der letzten 14 Tage + ★-Mails — z. B. `1` bei einer markierten Mail), während Dock-/Tray-Zähler weiterhin nur ungelesene Mails zählen.

## 0.7.15 (2026-11-23)

### Korrekter Fokus-Ungelesen-Zähler (14-Tage-Fenster)
- **Fokus-Badge stimmt jetzt mit Fokus überein ([internal/nativeui/state.go](internal/nativeui/state.go)):** `unreadCount()` zählt wieder gezielt die ungelesenen Mails in **Fokus (Ungelesen & ★)** (letzte 14 Tage, ohne Papierkorb/Spam) und wird auch beim Wechsel in andere Ordner laufend im Hintergrund aktualisiert — statt ältere (> 14 Tage) ungelesene Ordner-Zähler aus `INBOX` & Unterordnern auf den Fokus-Badge zu addieren.

## 0.7.14 (2026-11-23)

### Dezentere Versionsanzeige (Handy & Desktop)
- **Kleine, zurückhaltende Versionsnummer:** Auf dem Handy ([internal/nativeui/phone.go](internal/nativeui/phone.go)) wird die Versionsnummer jetzt nicht mehr im großen 22pt-Fett-Titel gerendert, sondern klein (`10.5pt`) und gedimmt hinter **MailDiver** (zugleich entfällt der leere linke Platzhalter auf dem Startbildschirm). Auf dem Desktop ([internal/nativeui/views_chrome.go](internal/nativeui/views_chrome.go)) sowie in Einstellungen und Hilfe ist die Versionsnummer ebenfalls kleiner (`9.5–10.5pt`) und dezenter gestaltet.

## 0.7.13 (2026-11-22)

### UI-Versionsanzeige, Lokale-KI-Doku (MTPLX / Inkling 2 Mini) & Komplett-Review-Fixes
- **Versionsnummer direkt in der UI:** MailDiver zeigt jetzt die aktuelle Version (`v0.7.13`) dezent direkt hinter **MailDiver** in der Desktop-Toolbar, im Handy-Header, im `☰`-Menü sowie im Einstellungs- und Hilfe-Dialog.
- **Lokale KI mit MTPLX & Inkling 2 Mini dokumentiert (`docs/LOCAL_AI.md`):** Neue deutsch-/englischsprachige Anleitung zu `brew install youssofal/mtplx/mtplx`, `mtplx --version`, `mtplx serve --model Youssofal/Inkling-2-Mini-MTPLX-Optimized-Speed`, MailDiver-Konfiguration auf `http://127.0.0.1:8000/v1`, Ollama sowie Hinweis zu `mtplx connect opencode` für OpenCode / T3 Code.
- **Lokale KI & Copilot-Synchronisierung:**
  - *Zusammenfassen / Entwurf / Termin / Kontakte* im Inspector übernehmen jetzt den in der Copilot-Seitenleiste gewählten Provider & das gewählte Modell (`AnalyzeRequest.Backend`/`Model`).
  - Streaming-Chat (`ChatStream`) beherrscht jetzt ebenfalls den automatischen Retry ohne `response_format=json_object` (inkl. `guided decoding`/`llguidance`-Erkennung), und der Copilot fällt automatisch ohne `tools` zurück, wenn ein lokales Modell kein Function-Calling unterstützt.
  - `openaicompat` unterstützt jetzt `OPENAICOMPAT_API_KEY` direkt in den Einstellungen, lokale `openaicompat`-Endpoints werden in der Hilfe korrekt als *Lokal (0 Cloud-Tokens)* erkannt, und die Copilot-Seitenleiste erhält einen `↻`-Button zum Nachladen von Modellen.
  - In den Einstellungen bleiben Endpoints pro Provider erhalten, `ListModelsFor` nutzt gespeicherte API-Keys und zeigt Modell-Ladefehler inline an, und beim Speichern synchronisiert sich die Copilot-Seitenleiste sofort.
- **Mail-Engine, MIME & IMAP-Robustheit:**
  - RFC-2231-Fallback-Parser (`parseLooseMIMEParams`) berücksichtigt jetzt Semikolons in Anführungszeichen, `charset'lang'value`-Präfixe, numerisch sortierte `*0*`/`*1*`-Fortsetzungen und gibt erweiterten Dateinamen Vorrang; auch `ExtractInlineImage(s)` nutzt den toleranten `Content-Type`-Parser.
  - `EmptyTrash` & `EmptySpam` nutzen jetzt den pro-Account-Sync-Lock; `MarkSpam`, Spam-Filterregeln und Host-Archivierung setzen vor dem Verschieben konsistent `\Seen`; Rückgängig (`⌘Z`) stellt bei zuvor ungelesenen Mails im Papierkorb/Spam das `\Unseen`-Flag wieder her (`RestoreUnread`).
  - `watchIMAPCtx` schließt blockierende IMAP-Sockets bei `SetWorkersPaused(true)` sofort, `io.EOF` wird als transienter Netzabbruch erkannt, und `Junk E-Mail` / `Bulk Mail` werden zuverlässig als Spam-Ordner klassifiziert.
  - Büroklammer-Symbol in der Mailliste (Desktop & Handy) für Mails mit Anhängen (`HasAttachments`), scrollbare Anhang-Leiste bei vielen Anhängen, CSV/TSV-Tabellenvorschau & Monospace-Textvorschau in der Anhang-Vorschau.
- **Editor, Mobil-UX & Feinschliff:**
  - WYSIWYG-Editor: `Return`-`<p>`-Normalisierung läuft nach der Browser-Mutation mit exakter Cursor-Positionierung; `Shift+Return` sorgt am Absatzende verlässlich für den Sentinel-`<br>`; Signatur-Schutz beschneidet Markierungen sauber statt den gesamten Mailtext zu leeren (erhält `⌘Z` Undo); Paste-Sanitizer nutzt `DOMParser` ohne spekulative Bild-Requests; `⌘↩` und `⌘S` (Entwurf speichern) funktionieren auch mit aktivem WYSIWYG-Editor; `a`/`Shift+R` (Allen antworten) und `f` (Weiterleiten) als Tastenkürzel ergänzt.
  - Android/Mobil-Lifecycle: Race-freie Worker-Pause/Resume-Serialisierung (`pauseMu` + Timer-Generation), WLAN↔5G-Handoff-Reconnect, thread-sicheres `uiLang()` für WorkManager-Benachrichtigungen, automatisches Zurückspringen aus dem Handy-Reader nach Entfernen einer Mail, *Verschieben*-Button in der Handy-Auswahlleiste und `✕`-Button in der Undo-Leiste.
  - Ungelesen-Zähler & Ordner: Fokus zählt nur noch tatsächlich ungelesene Mails, Papierkorb & Spam zeigen keine Ungelesen-Badges mehr, globale Ungelesen-Zahl kommt aus den Ordnerzählern ohne Papierkorb/Spam/Entwürfe/Gesendet, und *Weitere Ordner* summiert auch eingeklappte Unterordner.

## 0.7.12 (2026-11-22)

### KI-Fallback bei Servern ohne `response_format: json_object` (z. B. MTPLX)
- **Automatischer Retry ohne `response_format` (`internal/ai/anyllm.go`):** Lehnt ein lokaler OpenAI-kompatibler Server (wie `mtplx serve` ohne optionales `llguidance`) `response_format: {"type": "json_object"}` mit `400 Bad Request` ab, wiederholt MailDiver die Anfrage automatisch einmal ohne `response_format` und wertet die JSON-Antwort des Modells normal aus.

## 0.7.11 (2026-11-22)

### Gelöschte Mails automatisch als gelesen markieren
- **Kein Ungelesen-Zähler mehr im Papierkorb:** Beim Verschieben von Mails in den Papierkorb (Einzel-Löschen, Mehrfachauswahl/Batch, Wischen auf dem Handy sowie Filterregeln mit Ziel Papierkorb) setzt MailDiver jetzt vor dem IMAP-`MOVE`/`COPY` automatisch das `\Seen`-Flag (`Read: true`), sodass gelöschte ungelesene Mails nicht mehr als ungelesen im Papierkorb liegen bleiben.

## 0.7.10 (2026-11-22)

### Fixierte Anhang-Leiste, robusteres MIME-Parsing & Return vs. Shift+Return
- **Fixierte Anhang-Leiste am unteren Rand (wie Thunderbird):** Auf dem Desktop werden Anhänge jetzt außerhalb des Scroll-Bereichs fest am unteren Rand der Leseansicht angezeigt (inkl. Büroklammer-Badge im Mail-Header, Gesamtgröße sowie direktem Vorschau- und Download-Button) — damit verschwinden Anhänge nie mehr unter langen Mails oder Signaturen. Auf dem Handy erscheinen Anhänge direkt über dem Mailtext.
- **Robusteres MIME-Anhang-Parsing & RFC-2231-Ausgabe (`internal/mailengine/mime.go`):** Fallback-Parser für ungewöhnlich formatierte `Content-Disposition`/`Content-Type`-Header (inkl. unquotierte Dateinamen mit Leerzeichen & RFC-2231-`filename*0`/`filename*1`-Fortsetzungen) sowie saubere `mime.FormatMediaType`-Ausgabe bei ausgehenden Anhängen.
- **Klarer Unterschied zwischen `Return` und `Shift+Return` im Editor:** `Return` erzeugt jetzt im WYSIWYG-Editor zuverlässig einen neuen Absatz (`<p>` mit sichtbarem Absatzabstand `0.75em` und sauberem `margin:0 0 10px 0` im ausgehenden HTML), während `Shift+Return` gezielt einen weichen Zeilenumbruch (`<br>` ohne Absatzabstand) einfügt.

## 0.7.9 (2026-11-22)

### Android-Hintergrund-, Standby- & Offline-Sync-Fix (`no such host`)
- **IMAP IDLE bei Hintergrund / Suspend / Offline sauber pausieren (`SetWorkersPaused`):** Auf Android blockiert die System-Firewall (`netd` / App Standby / Doze) Netzwerk & DNS, sobald die App im Hintergrund liegt — bisher liefen die IMAP-IDLE-Worker trotzdem weiter, liefen in `dial tcp: lookup …: no such host`, zeigten einen roten Sync-Fehler-Banner und schliefen bis zu 60 s im Backoff. Jetzt pausiert MailDiver alle IDLE- und Perioden-Worker sofort bei `OnSuspend` sowie im Offline-Zustand (und startet beim Headless-WorkManager-Wake keine dauerhaften IDLE-Sockets).
- **Sofortiger Reconnect bei Rückkehr in den Vordergrund & Netzwechsel:** Bei `OnResume` / `OnDidBecomeActive` und `mygo.Network.OnChanged` wartet MailDiver kurz (500 ms auf Mobilgeräten), bis Android Netzwerk & DNS wieder freigibt, räumt alte transiente Netz-/DNS-Fehler auf und verbindet IMAP IDLE sofort ohne Backoff neu.
- **Transiente Mobil-DNS-Wiederholung & Offline-Anzeige:** `dialIMAP` wiederholt auf Android/iOS einen einmaligen transienten DNS-/Netz-Fehler nach 1,5 s automatisch (z. B. beim WLAN↔5G-Wechsel), und bei fehlendem Netz zeigt die App einen ruhigen *Offline — warte auf Netzwerkverbindung*-Hinweis statt roter `no such host`-Fehlermeldungen.

## 0.7.8 (2026-11-22)

### „Alle auswählen“ (Mark All), verbesserte Auswahlleiste & Spam leeren
- **Select-All-Checkbox & `⌘A`:** In der Kopfzeile der Mailliste sitzt jetzt linksbündig über den Zeilen-Checkboxen eine 3-Zustands-Auswahlbox (leer / teilweise / alle), mit der alle sichtbaren Mails per 1-Klick oder `⌘A` ausgewählt und wieder abgewählt werden können.
- **Unabgeschnittene Batch-Aktionsleiste:** Die Auswahlleiste bei markierten Mails ist jetzt kompakt zweizeilig aufgebaut (*Zeile 1:* `X / N markiert` + `Alle`/`Keine` + `✕`; *Zeile 2:* Quick-Regel, Gelesen, Stern, Archivieren, Verschieben, Papierkorb inkl. Bestätigungshervorhebung), sodass auch in schmalen Maillisten nichts mehr abgeschnitten wird.
- **„Spam leeren“ (Desktop & Handy):** Im Spam-/Junk-Ordner gibt es analog zu *Papierkorb leeren* jetzt direkt im Header eine 2-Klick-Aktion **Spam leeren** (`EmptySpam`), die den gesamten Spam-Ordner sicher nach Rückbestätigung auf dem IMAP-Server und lokal leert.

## 0.7.7 (2026-11-22)

### Einstellungen in fachliche Tabs gruppiert (Desktop & Handy)
- **5 übersichtliche Bereiche statt langer Scroll-Liste:** Der Einstellungsdialog ([internal/nativeui/settings_view.go](internal/nativeui/settings_view.go)) ist jetzt über eine fixierte Tab-Leiste unter dem Titel in fünf fachliche Tabs gegliedert:
  1. **Allgemein** (*Darstellung & Sprache, Lesen, Archiv*)
  2. **Verfassen** (*Undo-Send, Entwurf Auto-Save, Textbausteine / Vorlagen*)
  3. **Ordner** (*Kompakte Ordneransicht, Postfach-Auswahl, immer sichtbare Ordner & aufgeklappte Unterordner beim Start*)
  4. **Web & Bilder** (*Erlaubte Bild-Absender, Browser-Vorschau Split-Panel & Auto-Link-Regeln*)
  5. **KI & Sync** (*KI-Assistent, Triage-Scan, API-Schlüssel & verschlüsselter LAN-QR-Geräte-Transfer*)
- **Desktop & Mobil:** Auf dem Desktop als kompakte Segmented-Tab-Leiste, auf dem Handy als umbrechende Pill-Tabs; jeder Tab startet sauber am Anfang gescrollt, und bei einem Validierungsfehler im Triage-Bereich springt der Dialog beim Speichern automatisch in den Tab *KI & Sync*.

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
