# Changelog

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
