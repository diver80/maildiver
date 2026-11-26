# Konten, Kalender & Kontakte einbinden

Kurzer Überblick, wie du die gängigen Mail-Anbieter, Google und Microsoft/Outlook in PostDive
einbindest. Alles liegt unter **Einstellungen (`⌘,`) → Konten**. Passwörter und Anmelde-Tokens
speichert PostDive im Schlüsselbund des Systems.

*A short setup guide (German) for mail providers, Google and Microsoft/Outlook in PostDive.*

## Auf einen Blick

| Was | Weg in PostDive | Lesen | Schreiben | Aufwand |
|---|---|---|---|---|
| Mail bei IONOS, 1&1, GMX, WEB.DE, STRATO | Konto mit Anbieter-Vorlage, normales Passwort | ✓ | ✓ | gering |
| Gmail | Konto „Gmail (App-Passwort)“ | ✓ | ✓ | gering |
| iCloud Mail, Fastmail | Konto mit Vorlage, App-Passwort | ✓ | ✓ | gering |
| Microsoft 365 / Outlook.com Mail | Konto „Microsoft 365 / Outlook (OAuth2)“ mit eigener App-Registrierung | ✓ | ✓ | hoch – oft nur mit IT |
| Google Kalender & Kontakte | Einstellungen → Konten → Kalender → „Mit Google verbinden“ | ✓ | ✓ | mittel (einmalig) |
| Google Kalender ohne Anmeldung | Geheime iCal-Adresse als Kalender-Abo | ✓ | – | gering |
| Outlook-/Microsoft-365-Kalender | Veröffentlichter Kalender als Kalender-Abo | ✓ | – | gering, falls erlaubt |
| Teams-Besprechung planen | Termin-Editor → „In Teams planen“ | – | über Teams | keiner |
| Nextcloud, Fastmail, Radicale … | CalDAV & CardDAV | ✓ | ✓ | gering |

## Mail-Konten

**Einstellungen → Konten → E-Mail → „Neues IMAP/SMTP-Konto hinzufügen“**, dann unter *Anbieter* die passende
Vorlage wählen. Sie füllt Server, Ports und Verschlüsselung aus; du gibst nur Adresse und Passwort
ein. Benutzername ist immer die vollständige E-Mail-Adresse.

| Anbieter | Vorlage | IMAP | SMTP | Passwort |
|---|---|---|---|---|
| **IONOS** | IONOS | `imap.ionos.de` 993 (TLS) | `smtp.ionos.de` 465 (TLS) – 587 (STARTTLS) geht auch | Postfach-Passwort |
| **1&1** | 1&1 | `imap.1und1.de` 993 (TLS) | `smtp.1und1.de` 587 (STARTTLS) | Postfach-Passwort |
| **GMX** | GMX | `imap.gmx.net` 993 (TLS) | `mail.gmx.net` 587 (STARTTLS) | Konto-Passwort; IMAP-Zugriff vorher im GMX-Webmail freischalten |
| **WEB.DE** | WEB.DE | `imap.web.de` 993 (TLS) | `smtp.web.de` 587 (STARTTLS) | Konto-Passwort; IMAP-Zugriff vorher im WEB.DE-Webmail freischalten |
| **STRATO** | STRATO | `imap.strato.de` 993 (TLS) | `smtp.strato.de` 465 (TLS) | Postfach-Passwort |
| **Gmail** | Gmail (App-Passwort) | `imap.gmail.com` 993 (TLS) | `smtp.gmail.com` 465 (TLS) | App-Passwort (Knopf „App-Passwort erstellen ↗“; braucht die Bestätigung in zwei Schritten) |
| **iCloud Mail** | iCloud Mail | `imap.mail.me.com` 993 (TLS) | `smtp.mail.me.com` 587 (STARTTLS) | App-spezifisches Passwort aus dem Apple-Account |
| **Fastmail** | Fastmail | `imap.fastmail.com` 993 (TLS) | `smtp.fastmail.com` 465 (TLS) | App-Passwort aus den Fastmail-Einstellungen |
| **Microsoft 365 / Outlook.com** | Microsoft 365 / Outlook (OAuth2) | `outlook.office365.com` 993 (TLS) | `smtp.office365.com` 587 (STARTTLS) | kein Passwort – Anmeldung im Browser, siehe unten |
| Andere | Benutzerdefiniert | laut Anbieter | laut Anbieter | – |

PostDive verbindet sich nur verschlüsselt (TLS oder STARTTLS). Umlaute im Passwort funktionieren.
Klappt die Anmeldung nicht, schreibt PostDive ein Diagnose-Log ohne Passwörter, das du dem Support
oder in einem Issue mitschicken kannst.

**Weitere Adressen (Aliase):** Unter „▸ Erweiterte Einstellungen (Server, Signatur, Aliase…)“ eines
Kontos lassen sich zusätzliche Absenderadressen mit eigenem Namen und eigener Signatur anlegen.

### Microsoft 365 / Outlook.com

Microsoft lässt Mail-Programme nur noch per OAuth-Anmeldung zu. Dafür braucht PostDive eine
**eigene App-Registrierung** (Client-ID) in Microsoft Entra ID mit den Berechtigungen
`IMAP.AccessAsUser.All`, `SMTP.Send` und `offline_access`. In Firmen darf das meist nur die IT; ohne
Registrierung geht Microsoft-Mail in PostDive nicht. Kalender und Teams gehen trotzdem – siehe
unten.

## Google Kalender & Kontakte

**Einstellungen → Konten → Kalender → Google Kalender (Bidirektional).** Einmalig richtest du ein
eigenes Google-Cloud-Projekt ein; die Knöpfe in PostDive führen direkt zu den Seiten:

1. **Calendar API aktivieren** (und **People API**, wenn du auch Google Kontakte willst).
2. **OAuth-Client vom Typ „Desktop-App“** anlegen und Client-ID (und Client-Secret) in PostDive
   eintragen.
3. **Mit Google verbinden** – der Browser öffnet sich, du bestätigst den Zugriff.

Danach synchronisiert PostDive alle Google-Kalender in beide Richtungen; Termine mit Teilnehmern
verschickt Google selbst, auf Wunsch mit Google-Meet-Link. Google Kontakte laufen über dieselbe
Anmeldung (Kontakte → „Google Sync“).

> **Tipp:** Steht das Projekt bei Google im Status „Testing“, laufen Anmeldungen nach 7 Tagen ab.
> Für den eigenen Gebrauch das Projekt auf „In Produktion“ stellen (die Warnung „nicht verifiziert“
> beim Anmelden ist dann normal) oder sich nach Ablauf neu verbinden.

**Ohne Google-Cloud-Projekt, nur lesend:** In Google Kalender unter *Einstellungen → Kalender →
Kalender einbinden* die **„Privatadresse im iCal-Format“** kopieren und in PostDive als
Kalender-Abo eintragen (siehe unten).

## Outlook- / Microsoft-365-Kalender

Ohne App-Registrierung geht das **nur lesend** über einen veröffentlichten Kalender:

1. Outlook im Web → **Einstellungen → Kalender → Freigegebene Kalender → Kalender veröffentlichen**.
2. Kalender wählen, **„Kann alle Details anzeigen“** einstellen, veröffentlichen.
3. Den **ICS-Link** kopieren und in PostDive als Kalender-Abo eintragen.

Microsoft aktualisiert veröffentlichte Kalender mit Verzögerung (oft einige Stunden). Fehlt der
Punkt „Kalender veröffentlichen“, hat die Firma ihn abgeschaltet.

**Teams-Besprechungen** planst du im Termin-Editor mit **„In Teams planen“**: Teams öffnet sich mit
Titel, Zeit, Notizen und Teilnehmern, gesendet wird in Teams. Mit **„Mich selbst einladen“** kommt
die Einladung auch in dein PostDive-Postfach – „Zusagen“ legt die Besprechung in deinen
PostDive-Kalender.

## Kalender-Abos (ICS / webcal)

**Einstellungen → Konten → Kalender → „Kalender-Abo hinzufügen (https:// oder webcal://)“.**
Geeignet für Google (Privatadresse), Outlook (veröffentlichter Kalender), Feiertage, Schulferien,
Sport-Spielpläne. Abos sind schreibgeschützt; PostDive holt sie regelmäßig neu.

## CalDAV & CardDAV

Für Kalender und Adressbücher von Nextcloud, Fastmail, Radicale und anderen CalDAV-/CardDAV-
Servern: **Einstellungen → Konten → Kalender → CalDAV & CardDAV**. Server-Adresse, Benutzername und
(App-)Passwort eintragen; PostDive findet die Kalender und Adressbücher selbst und schreibt
Änderungen zurück.

## Einladungen verschicken

| Kalender | Wer verschickt die Einladung? |
|---|---|
| Google Kalender | Google selbst (auf Wunsch mit Meet-Link) |
| Lokaler Kalender, CalDAV | PostDive als Kalender-Mail über dein Mail-Konto (z. B. IONOS) |
| Teams | Teams, nach „In Teams planen“ |

Antworten der Teilnehmer trägt PostDive beim Termin ein, sobald du die Antwort-Mail öffnest.

## Umzug von Thunderbird & auf weitere Geräte

- **Thunderbird-Import** (Einstellungen → Konten → TB-Import): übernimmt Konten, Signaturen,
  Kalender, Kontakte und Filterregeln, nur lesend.
- **Weiteres Gerät / Android:** Konten per verschlüsseltem QR-Code vom Computer übernehmen.
