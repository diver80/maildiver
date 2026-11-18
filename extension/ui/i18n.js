/* global browser, messenger */
// Texts of the add-on's settings page (de, en).
(function () {
  const api = typeof messenger !== "undefined" ? messenger : typeof browser !== "undefined" ? browser : null;

  const DICT = {
    de: {
      opts_title: "OmaThunder",
      opts_subtitle: "Bridge zur OmaThunder-App und Omarchy-Theme für Thunderbird",
      opts_save: "Speichern",
      opts_saved: "Gespeichert.",
      opts_sec_status: "Status",
      opts_app_hint: "Mails, Kalender, Kontakte, KI-Einstellungen, Triage-Scan und Archiv nutzt du in der OmaThunder-App: <code>omathunder ui</code>",
      opts_sec_bridge: "OmaThunder-App",
      opts_bridge_enabled: "Lokale Bridge für die OmaThunder-App",
      opts_bridge_desc: "Startet im Hilfsprogramm eine nur auf diesem Rechner erreichbare, per Token geschützte Schnittstelle (127.0.0.1). Darüber liest die OmaThunder-App Mails, Ordner, Kalender und Kontakte. Änderungen an Mails sind nur möglich, wenn „Nur lesen“ aus ist und das Konto unten freigegeben ist.",
      opts_bridge_send: "App darf Mails direkt senden",
      opts_bridge_send_desc: "Achtung: Die App kann dann ohne weitere Rückfrage in Thunderbird Mails versenden. Nur einschalten, wenn du der App und jedem Programm auf diesem Rechner vertraust, das den Bridge-Token lesen kann. Zusätzlich muss „Nur lesen“ aus und das Absenderkonto freigegeben sein.",
      opts_sec_rights: "Konten & Rechte",
      opts_readonly: "Nur lesen: keine Änderungen an Mails, Ordnern, Schlagwörtern und Filtern",
      opts_readonly_desc: "Archivieren, Verschieben, Löschen, Filter und das Aufräumen alter Mails sind dann gesperrt. Antwort-Entwürfe öffnen sich weiterhin als Fenster in Thunderbird – gesendet wird nie automatisch.",
      opts_accounts: "Für OmaThunder freigegebene Konten",
      opts_accounts_desc: "Nur diese Konten darf OmaThunder ändern (wenn „Nur lesen“ aus ist), per Triage-Scan einordnen und archivieren. Lesen kann die App alle Konten.",
      opts_no_accounts: "Keine Konten gefunden.",
      opts_feat_assistant: "KI-Assistent erlauben",
      opts_feat_assistant_desc: "Die App darf Mails zusammenfassen, Antworten entwerfen und den Copilot nutzen. Anbieter und Schlüssel stellst du in der App ein.",
      opts_feat_triage: "Triage-Scans erlauben",
      opts_feat_triage_desc: "Das Hilfsprogramm darf die Posteingänge der freigegebenen Konten einordnen (auf Knopfdruck oder bei neuer Mail). Verschlüsselte Mails werden dabei nie entschlüsselt.",
      opts_sec_theme: "Theme & Sprache",
      opts_feat_theme: "Omarchy-Farben in Thunderbird",
      opts_lang_lbl: "Sprache",
      opts_lang_auto: "Auto (Thunderbird / System)",
      opts_theme_mode: "Theme",
      opts_theme_auto: "Auto (Omarchy live auf Linux, Dark/Light-Preset auf macOS)",
      opts_theme_omarchy: "Immer Omarchy colors.toml",
      opts_theme_preset: "Festes Preset",
      opts_preset: "Preset",
      opts_style_body: "Auch Mail-Inhalt und Editor einfärben",
      opts_style_body_desc: "Aus: Nur die Thunderbird-Oberfläche bekommt die Theme-Farben. An: HTML-Mails mit fest eingestellten Textfarben können schwer lesbar werden.",
      st_host: "Hilfsprogramm",
      st_host_on: "verbunden (Version {version})",
      st_host_off: "nicht verbunden",
      st_host_off_desc: "Installiere OmaThunder (brew install diver80/tap/omathunder) und führe einmal „omathunder setup“ aus, dann Thunderbird neu starten.",
      st_versions: "Versionen",
      st_versions_ok: "Add-on {ext} und Hilfsprogramm passen zusammen",
      st_versions_bad: "Add-on und Hilfsprogramm passen nicht zusammen",
      st_versions_bad_desc: "Hilfsprogramm {host}, Add-on {ext}: Installiere die .xpi aus derselben Version neu.",
      st_bridge: "Bridge",
      st_bridge_on: "an",
      st_bridge_off: "aus – die App kann sich nicht verbinden",
      st_keys: "API-Schlüssel",
      st_keys_desc: "Schlüssel trägst du in der App ein (☰ → Einstellungen); sie liegen in dieser Datei.",
      err_read_only: "„Nur lesen“ ist an: OmaThunder ändert keine Mails, Ordner oder Schlagwörter.",
      err_feature_disabled_bridge: "Die lokale Bridge für die OmaThunder-App ist ausgeschaltet.",
      err_host_config_unavailable: "Das Hilfsprogramm antwortet nicht.",
    },
    en: {
      opts_title: "OmaThunder",
      opts_subtitle: "Bridge to the OmaThunder app and Omarchy theme for Thunderbird",
      opts_save: "Save",
      opts_saved: "Saved.",
      opts_sec_status: "Status",
      opts_app_hint: "Use mail, calendar, contacts, AI settings, triage scans and the archive in the OmaThunder app: <code>omathunder ui</code>",
      opts_sec_bridge: "OmaThunder app",
      opts_bridge_enabled: "Local bridge for the OmaThunder app",
      opts_bridge_desc: "Starts a token-protected interface in the helper program that is only reachable from this computer (127.0.0.1). The OmaThunder app uses it to read mails, folders, calendars and contacts. Changes to mails are only possible when read-only is off and the account is enabled below.",
      opts_bridge_send: "Allow the app to send mails directly",
      opts_bridge_send_desc: "Warning: the app can then send mails through Thunderbird without asking again. Only turn this on if you trust the app and every program on this computer that can read the bridge token. Read-only must also be off and the sender account enabled.",
      opts_sec_rights: "Accounts & permissions",
      opts_readonly: "Read-only: no changes to mails, folders, tags and filters",
      opts_readonly_desc: "Archiving, moving, deleting, filters and cleaning up old mails are blocked then. Reply drafts still open as windows in Thunderbird – nothing is ever sent automatically.",
      opts_accounts: "Accounts enabled for OmaThunder",
      opts_accounts_desc: "OmaThunder may only change these accounts (when read-only is off), sort them by triage scan and archive them. The app can read all accounts.",
      opts_no_accounts: "No accounts found.",
      opts_feat_assistant: "Allow the AI assistant",
      opts_feat_assistant_desc: "The app may summarize mails, draft replies and use the copilot. Set providers and keys in the app.",
      opts_feat_triage: "Allow triage scans",
      opts_feat_triage_desc: "The helper program may sort the inboxes of the enabled accounts (on request or when new mail arrives). Encrypted mails are never decrypted for it.",
      opts_sec_theme: "Theme & language",
      opts_feat_theme: "Omarchy colors in Thunderbird",
      opts_lang_lbl: "Language",
      opts_lang_auto: "Auto (Thunderbird / system)",
      opts_theme_mode: "Theme",
      opts_theme_auto: "Auto (live Omarchy on Linux, dark/light preset on macOS)",
      opts_theme_omarchy: "Always Omarchy colors.toml",
      opts_theme_preset: "Fixed preset",
      opts_preset: "Preset",
      opts_style_body: "Also recolor message bodies and the editor",
      opts_style_body_desc: "Off: only the Thunderbird interface gets the theme colors. On: HTML mails with fixed text colors can become hard to read.",
      st_host: "Helper program",
      st_host_on: "connected (version {version})",
      st_host_off: "not connected",
      st_host_off_desc: "Install OmaThunder (brew install diver80/tap/omathunder), run “omathunder setup” once, then restart Thunderbird.",
      st_versions: "Versions",
      st_versions_ok: "Add-on {ext} and helper program match",
      st_versions_bad: "Add-on and helper program do not match",
      st_versions_bad_desc: "Helper program {host}, add-on {ext}: install the .xpi of the same version again.",
      st_bridge: "Bridge",
      st_bridge_on: "on",
      st_bridge_off: "off – the app cannot connect",
      st_keys: "API keys",
      st_keys_desc: "Enter keys in the app (☰ → Settings); they are stored in this file.",
      err_read_only: "Read-only is on: OmaThunder does not change mails, folders or tags.",
      err_feature_disabled_bridge: "The local bridge for the OmaThunder app is turned off.",
      err_host_config_unavailable: "The helper program does not answer.",
    },
  };

  function detectLang(pref) {
    try {
      const qsLang = new URLSearchParams(window.location.search).get("lang");
      if (qsLang === "de" || qsLang === "en") return qsLang;
    } catch (_) {}
    if (pref === "de" || pref === "en") return pref;
    try {
      const stored = localStorage.getItem("omathunder_lang");
      if (stored === "de" || stored === "en") return stored;
    } catch (_) {}
    const ui = (api?.i18n?.getUILanguage?.() || navigator.language || "en").toLowerCase();
    return ui.startsWith("de") ? "de" : "en";
  }

  let activeLang = detectLang("auto");

  function setLang(lang) {
    activeLang = detectLang(lang);
    try {
      if (lang === "de" || lang === "en") localStorage.setItem("omathunder_lang", lang);
      else if (lang === "auto") localStorage.removeItem("omathunder_lang");
    } catch (_) {}
    document.documentElement.lang = activeLang;
    return activeLang;
  }

  function getLang() {
    return activeLang;
  }

  function t(key, vars = {}) {
    const dict = DICT[activeLang] || DICT.en;
    let str = dict[key] ?? DICT.en[key] ?? key;
    for (const [k, v] of Object.entries(vars)) {
      str = str.replaceAll(`{${k}}`, String(v));
    }
    return str;
  }

  function applyDom(root = document) {
    root.querySelectorAll("[data-i18n]").forEach((el) => {
      el.textContent = t(el.dataset.i18n);
    });
    root.querySelectorAll("[data-i18n-html]").forEach((el) => {
      el.innerHTML = t(el.dataset.i18nHtml);
    });
    root.querySelectorAll("[data-i18n-ph]").forEach((el) => {
      el.placeholder = t(el.dataset.i18nPh);
    });
  }

  // Translates host/background error codes such as "read_only" or "feature_disabled:triage".
  function errText(err) {
    const code = String(err?.message || err || "");
    const key = `err_${code.replace(/[^a-z_]/gi, "_")}`;
    const translated = t(key);
    if (translated !== key) return translated;
    // "code: detail" (bridge / experiment errors): translate the code, keep the detail.
    const m = code.match(/^([a-z_]+(?::[a-z_]+)?): (.+)$/);
    if (m) {
      const baseKey = `err_${m[1].replace(/[^a-z_]/gi, "_")}`;
      const base = t(baseKey);
      if (base !== baseKey) return `${base} (${m[2]})`;
    }
    return code;
  }

  window.OMA_I18N = { setLang, getLang, detectLang, t, applyDom, errText };
})();
