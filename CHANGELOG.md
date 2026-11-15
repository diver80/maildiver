# Changelog

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
