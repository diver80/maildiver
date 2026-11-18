# Changelog

## Unreleased

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
