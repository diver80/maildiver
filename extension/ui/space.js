/* global browser, messenger */
const api = typeof messenger !== "undefined" ? messenger : typeof browser !== "undefined" ? browser : null;
const inExtension = Boolean(api?.runtime?.sendMessage);

const t = (k, vars) => (window.OMA_I18N ? window.OMA_I18N.t(k, vars) : k);

function getKanbanColumns() {
  return [
    { id: "incoming", label: t("col_incoming"), badge: "accent" },
    { id: "action", label: t("col_action"), badge: "err" },
    { id: "waiting", label: t("col_waiting"), badge: "warn" },
    { id: "invoice", label: t("col_invoice"), badge: "accent" },
    { id: "done", label: t("col_done"), badge: "ok" },
  ];
}

const DEMO_THEMES = [
  { name: "tokyo-night", label: "Tokyo Night", isLight: false, background: "#1a1b26", darkBackground: "#16161e", darkerBackground: "#13131a", lighterBackground: "#24283b", foreground: "#c0caf5", darkForeground: "#7aa2f7", accent: "#7aa2f7", selection: "#292e42", border: "#292e42", green: "#9ece6a", yellow: "#e0af68", red: "#f7768e" },
  { name: "catppuccin", label: "Catppuccin Mocha", isLight: false, background: "#1e1e2e", darkBackground: "#181825", darkerBackground: "#11111b", lighterBackground: "#313244", foreground: "#cdd6f4", darkForeground: "#a6adc8", accent: "#cba6f7", selection: "#45475a", border: "#313244", green: "#a6e3a1", yellow: "#f9e2af", red: "#f38ba8" },
  { name: "nord", label: "Nord", isLight: false, background: "#2e3440", darkBackground: "#292e39", darkerBackground: "#242933", lighterBackground: "#3b4252", foreground: "#eceff4", darkForeground: "#d8dee9", accent: "#88c0d0", selection: "#434c5e", border: "#434c5e", green: "#a3be8c", yellow: "#ebcb8b", red: "#bf616a" },
  { name: "gruvbox", label: "Gruvbox Dark", isLight: false, background: "#282828", darkBackground: "#1d2021", darkerBackground: "#17191a", lighterBackground: "#3c3836", foreground: "#ebdbb2", darkForeground: "#a89984", accent: "#fe8019", selection: "#504945", border: "#3c3836", green: "#b8bb26", yellow: "#fabd2f", red: "#fb4934" },
  { name: "flexoki-light", label: "Flexoki Light", isLight: true, background: "#fffcf0", darkBackground: "#f2f0e5", darkerBackground: "#e6e4d9", lighterBackground: "#ffffff", foreground: "#100f0f", darkForeground: "#6f6e69", accent: "#205ea6", selection: "#dad8ce", border: "#cecdc3", green: "#66800b", yellow: "#ad8301", red: "#af3029" },
  { name: "osaka-jade", label: "Osaka Jade", isLight: false, background: "#111c18", darkBackground: "#0c1512", darkerBackground: "#09100e", lighterBackground: "#1b2b25", foreground: "#d7e4dc", darkForeground: "#759586", accent: "#2dd4bf", selection: "#233931", border: "#1f332c", green: "#34d399", yellow: "#f59e0b", red: "#f87171" },
];

const DEMO_FOLDERS = [
  { id: "f-inbox", path: "INBOX" },
  { id: "f-acme-in", path: "Spaces/Acme/Incoming" },
  { id: "f-acme-done", path: "Spaces/Acme/Erledigt" },
  { id: "f-beta", path: "Spaces/Rechnungen" },
  { id: "f-inv", path: "Buchhaltung/Rechnungen" },
];

const DEMO_RULES = [
  { id: "r-acme", space: "Acme GmbH", folderPath: "Spaces/Acme/Incoming", domains: ["acme.de", "acme-cloud.io"], autoMove: true, includeInScan: true, columnFolders: { done: "Spaces/Acme/Erledigt" } },
  { id: "r-beta", space: "Rechnungen", folderPath: "Spaces/Rechnungen", domains: ["beta-systems.com"], autoMove: true, includeInScan: true },
];

const DEMO_STATE = {
  classifications: {
    "demo-1": {
      messageId: "demo-1", space: "Acme GmbH", folder: "Spaces/Acme/Incoming", kanbanColumn: "action",
      subject: "Freigabe Q4-Architektur & NDA bis Freitag", from: "Laura Weber <laura@acme.de>",
      date: "2026-11-12T14:20:00Z", bucket: "action", urgency: "high",
      summary: "Wartet auf NDA-Freigabe & Terminvorschlag bis 15.11.", deadline: "2026-11-15",
      stage: "stage1-llm", model: "gemini-4-hydrogen", needsAttention: true,
    },
    "demo-2": {
      messageId: "demo-2", space: "Rechnungen", folder: "Spaces/Rechnungen", kanbanColumn: "incoming",
      subject: "Neue API-Zugänge für Staging-Test", from: "Jonas Meyer <jonas@beta-systems.com>",
      date: "2026-11-12T17:05:00Z", bucket: "incoming", urgency: "medium",
      summary: "Fragt nach SSH-Key und Testfenster nächste Woche.",
      stage: "stage1-llm", model: "gemini-4-hydrogen", needsAttention: true,
    },
    "demo-3": {
      messageId: "demo-3", space: "Acme GmbH", folder: "Spaces/Acme/Incoming", kanbanColumn: "invoice",
      subject: "Rechnung #2026-1184 — Cloud-Cluster", from: "billing@acme-cloud.io",
      date: "2026-11-12T07:40:00Z", bucket: "invoice", urgency: "medium",
      summary: "Rechnung erkannt (Stage 0, 0 Tokens) — € 480,00.", amount: "€ 480,00",
      stage: "stage0-rule", model: "deterministic", needsAttention: true,
    },
    "demo-4": {
      messageId: "demo-4", space: "Rechnungen", folder: "Spaces/Rechnungen", kanbanColumn: "waiting",
      subject: "Rückfrage Liefertermin Server-Rack", from: "Max <einkauf@beta-systems.com>",
      date: "2026-11-10T16:12:00Z", bucket: "waiting", urgency: "low",
      summary: "Du hast vor 2 Tagen geantwortet — wartet auf Rückmeldung.",
      stage: "stage0-rule", model: "deterministic", needsAttention: false,
    },
    "demo-5": {
      messageId: "demo-5", space: "", folder: "INBOX", kanbanColumn: "done",
      subject: "Weekly Go & Linux Digest #412", from: "digest@weekly.dev",
      date: "2026-11-12T06:00:00Z", bucket: "newsletter", urgency: "low",
      summary: "Newsletter via Stage-0-Header-Regel gefiltert.",
      stage: "stage0-rule", model: "deterministic", needsAttention: false,
    },
  },
  proposals: [
    {
      id: "prop-demo-1", kind: "draft_reply", messageId: "demo-1",
      title: "Antwort-Entwurf für Laura (Acme)",
      summary: "Bestätigt NDA-Prüfung & schlägt morgen 10:00 Uhr vor.",
      subject: "Re: Freigabe Q4-Architektur & NDA bis Freitag",
      body: "Hallo Laura,\n\ndas NDA prüfe ich heute Abend und sende es dir bis morgen Mittag signiert zurück.\nPasst dir morgen um 10:00 Uhr für ein kurzes Go zur Q4-Architektur?\n\nViele Grüße",
      status: "pending",
    },
  ],
};

let currentView = "kanban";
let selectedAccountId = "ALL";
let selectedFolder = "ALL";
let selectedSpace = "ALL";
// Board card chosen for the assistant. null = use the mail currently open in Thunderbird.
let selectedMessageId = inExtension ? null : "demo-1";
// Cards selected via checkbox, Cmd/Ctrl+Click, or Shift+Click for batch drag/actions.
let selectedCardIds = new Set();
let lastSelectedAnchorId = null;
let draggingCardIds = [];
// In Thunderbird the Kanban gets full width by default; clicking a card or the topbar toggle opens the AI drawer.
let assistantDrawerOpen = !inExtension;
let lastReport = null;
let aiMode = "draft_reply";
let aiTone = "freundlich";
let pendingDeleteRule = null;

let snapshot = {
  connected: false,
  themes: DEMO_THEMES,
  folders: DEMO_FOLDERS,
  state: DEMO_STATE,
  theme: { palette: DEMO_THEMES[0], source: "preview" },
  accounts: [{ id: "demo-account", name: "Firma (IMAP)" }],
  config: {
    features: { theme: true, assistant: true, triage: true },
    mailbox: { readOnly: true, triageAccountIds: ["demo-account"], kanbanAccountIds: ["demo-account"], autoMoveOnlyUnread: true },
    rules: DEMO_RULES,
    ai: { classifier: { backend: "gemini-api", model: "gemini-4-hydrogen" }, agent: { backend: "claude-cli", model: "" } },
  },
};

// Inside Thunderbird never show the preview's sample data: start empty until the host answers.
if (inExtension) {
  snapshot = {
    connected: false,
    themes: [],
    folders: [],
    accounts: [],
    state: { classifications: {}, proposals: [] },
    theme: null,
    config: { features: {}, mailbox: {}, rules: [] },
  };
}
let editingRuleId = null;
let showImportPanel = false;
let archiveStatsData = inExtension
  ? { archiveDir: "", exists: false, fileCount: 0, totalBytes: 0 }
  : { archiveDir: "~/OmaThunder-Archiv", exists: true, fileCount: 142, totalBytes: 48200000 };
let archivePreviewData = null;
let archiveReportData = null;
let archiveBusy = false;
let archiveForm = {
  accountId: "",
  scope: "inbox",
  folderPath: "",
  includeSubfolders: true,
  olderThanDays: 365,
  onlyRead: true,
  excludeStarred: true,
  maxMessages: 200,
  mode: "simulate",
  skipTrash: false,
  archiveDir: "",
};
let nativeFiltersData = inExtension
  ? { supported: false, accounts: [], filters: [], issues: [], candidates: [] }
  : {
      supported: true,
      accounts: [{ accountId: "demo-account", accountName: "Firma (IMAP)", count: 2 }],
      filters: [
        {
          accountId: "demo-account",
          accountName: "Firma (IMAP)",
          name: "Acme GmbH",
          enabled: true,
          matchAll: false,
          conditions: [{ field: "from", op: "endsWith", value: "@acme.de" }],
          actions: [{ type: "moveToFolder", targetFolderPath: "Spaces/Acme/Incoming" }],
        },
        {
          accountId: "demo-account",
          accountName: "Firma (IMAP)",
          name: "Systemmails (Beispiel-Bug)",
          enabled: true,
          matchAll: true,
          conditions: [
            { field: "from", op: "contains", value: "notify@service.de", booleanAnd: true },
            { field: "from", op: "contains", value: "alert@service.de", booleanAnd: true },
          ],
          actions: [{ type: "moveToFolder", targetFolderPath: "System" }],
        },
      ],
      issues: [
        {
          accountId: "demo-account",
          filterName: "Systemmails (Beispiel-Bug)",
          severity: "warn",
          code: "and_mutually_exclusive",
          detail: "2 verschiedene Absender mit UND verknüpft — sollte ODER sein.",
          quickFix: "set_or",
        },
      ],
      candidates: [
        {
          accountId: "demo-account",
          accountName: "Firma (IMAP)",
          space: "System",
          folderPath: "System",
          domains: ["service.de"],
          nativeFilterNames: ["Systemmails (Beispiel-Bug)"],
          alreadyImported: false,
        },
      ],
    };

function formatBytes(bytes) {
  const b = Number(bytes || 0);
  if (b < 1024) return `${b} B`;
  if (b < 1024 * 1024) return `${(b / 1024).toFixed(1)} KB`;
  if (b < 1024 * 1024 * 1024) return `${(b / (1024 * 1024)).toFixed(1)} MB`;
  return `${(b / (1024 * 1024 * 1024)).toFixed(2)} GB`;
}

async function refreshArchiveStats() {
  if (!inExtension || !snapshot.connected) return archiveStatsData;
  const res = await api.runtime.sendMessage({
    type: "omathunder:archiveStats",
    archiveDir: archiveForm.archiveDir || snapshot.config?.mailbox?.archiveDir || "",
  }).catch(() => null);
  if (res) archiveStatsData = res;
  return archiveStatsData;
}

async function refreshNativeFilters() {
  if (!inExtension || !snapshot.connected) return nativeFiltersData;
  const res = await api.runtime.sendMessage({ type: "omathunder:listNativeFilters" }).catch(() => null);
  if (res) {
    nativeFiltersData = {
      supported: Boolean(res.supported),
      accounts: res.accounts || [],
      filters: res.filters || [],
      issues: res.issues || [],
      candidates: res.candidates || [],
    };
  }
  return nativeFiltersData;
}

function hostOffline() {
  return inExtension && !snapshot.connected;
}

function standaloneMode() {
  return inExtension && !snapshot.connected;
}

function optionalHostHtml() {
  return `<div class="notice oma-card">
    <strong>${escapeHtml(t("optional_host_title"))}</strong>
    <div>${escapeHtml(t("optional_host_body"))}</div>
    ${snapshot.error ? `<div class="oma-muted oma-mono" style="font-size:11px;">${escapeHtml(snapshot.error)}</div>` : ""}
    <div style="display:flex; gap:8px; flex-wrap:wrap; margin-top:4px;">
      <button class="oma-btn primary" data-go-themes>${escapeHtml(t("nav_themes"))}</button>
      <button class="oma-btn" data-open-settings>${escapeHtml(t("btn_open_settings"))}</button>
      <button class="oma-btn ghost" data-retry>${escapeHtml(t("host_retry"))}</button>
    </div>
  </div>`;
}

function summaryText(c) {
  if (c.summaryKey) {
    const local = t(c.summaryKey);
    if (local !== c.summaryKey) return local;
  }
  return c.summary;
}

function feature(name) {
  return Boolean(snapshot.config?.features?.[name]);
}

function isReadOnly() {
  return snapshot.config?.mailbox?.readOnly !== false;
}

function triageAccounts() {
  const ids = new Set(snapshot.config?.mailbox?.triageAccountIds || []);
  return (snapshot.accounts || []).filter((a) => ids.has(a.id));
}

function kanbanAccounts() {
  const triageSet = new Set(snapshot.config?.mailbox?.triageAccountIds || []);
  const kanbanSet = new Set(snapshot.config?.mailbox?.kanbanAccountIds || []);
  return (snapshot.accounts || []).filter((a) => triageSet.has(a.id) && kanbanSet.has(a.id));
}

function errText(err) {
  return window.OMA_I18N ? window.OMA_I18N.errText(err) : String(err?.message || err);
}

function openSettings() {
  if (inExtension && api.runtime.openOptionsPage) api.runtime.openOptionsPage();
  else window.location.href = "options.html";
}

function noticeHtml(text) {
  return `<div class="notice oma-card">
    <div>${escapeHtml(text)}</div>
    <button class="oma-btn primary" data-open-settings>${escapeHtml(t("btn_open_settings"))}</button>
  </div>`;
}

function bindSettingsButtons(root) {
  root.querySelectorAll("[data-open-settings]").forEach((b) => b.addEventListener("click", openSettings));
}

function applyPaletteLocally(themeOrPalette) {
  if (!themeOrPalette) return;
  const p = themeOrPalette.palette || themeOrPalette;
  if (!p) return;
  if (inExtension && !feature("theme")) {
    document.getElementById("oma-dynamic-vars").textContent = "";
    document.getElementById("theme-label").textContent = "";
    return;
  }
  const cssVars = themeOrPalette.uiCssVars
    || (window.OmaCore && window.OmaCore.buildThemeVars ? window.OmaCore.buildThemeVars(p) : "");
  if (cssVars) {
    document.getElementById("oma-dynamic-vars").textContent = cssVars;
  }
  document.getElementById("theme-label").textContent = `Theme: ${p.label || p.name || ""}`;
}

function escapeHtml(str) {
  return String(str || "").replace(/[&<>\"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

function accountNameById(id) {
  if (!id) return "";
  return (snapshot.accounts || []).find((a) => a.id === id)?.name || id;
}

function getAllItems() {
  const items = Object.values(snapshot.state?.classifications || {});
  if (!inExtension || !snapshot.connected) return items;
  // Only show mails of accounts that are currently enabled for Kanban.
  const triageIds = new Set(snapshot.config?.mailbox?.triageAccountIds || []);
  const kanbanIds = new Set(snapshot.config?.mailbox?.kanbanAccountIds || []);
  return items.filter((c) => triageIds.has(c.accountId) && kanbanIds.has(c.accountId));
}

function cleanFolderPath(p) {
  return window.OmaCore?.cleanPath ? window.OmaCore.cleanPath(p) : String(p || "").replace(/^\/+|\/+$/g, "");
}

function accountInboxFolder(accountId) {
  return window.OmaCore?.findAccountInbox
    ? window.OmaCore.findAccountInbox(snapshot.folders || [], accountId)
    : null;
}

function isInboxFolderPath(accountId, folderPath) {
  const clean = cleanFolderPath(folderPath);
  if (!clean) return false;
  if (clean.toLowerCase() === "inbox") return true;
  const inbox = accountInboxFolder(accountId);
  return Boolean(inbox && cleanFolderPath(inbox.path).toLowerCase() === clean.toLowerCase());
}

function matchesSelectedFolder(c, accountId, folderTarget) {
  if (!c) return false;
  if (!folderTarget || folderTarget === "ALL") return true;
  const folderPath = cleanFolderPath(c.folder);
  const targetClean = cleanFolderPath(folderTarget);
  if (isInboxFolderPath(accountId, targetClean)) {
    return window.OmaCore?.isInboxFolder
      ? window.OmaCore.isInboxFolder({ path: folderPath })
      : folderPath.toLowerCase() === "inbox";
  }
  return window.OmaCore?.folderMatchesSubtree
    ? window.OmaCore.folderMatchesSubtree(folderPath, targetClean)
    : folderPath.toLowerCase() === targetClean.toLowerCase() || folderPath.toLowerCase().startsWith(`${targetClean.toLowerCase()}/`);
}

function folderDisplayLabel(accountId, folderPath) {
  const clean = cleanFolderPath(folderPath);
  if (!clean) return "";
  if (isInboxFolderPath(accountId, clean)) {
    const inbox = accountInboxFolder(accountId);
    return inbox?.name || "Inbox";
  }
  const l1 = window.OmaCore?.level1FolderName ? window.OmaCore.level1FolderName(clean) : "";
  if (l1) return l1;
  const match = (snapshot.folders || []).find((f) => f.accountId === accountId && cleanFolderPath(f.path).toLowerCase() === clean.toLowerCase());
  return match?.name || clean;
}

function folderEntriesForAccount(accountId, openItems) {
  const inbox = accountInboxFolder(accountId);
  const inboxClean = cleanFolderPath(inbox?.path || "INBOX");
  const configured = snapshot.config?.mailbox?.kanbanFolders?.[accountId];
  const entriesByKey = new Map();
  const addEntry = (rawPath) => {
    const clean = cleanFolderPath(rawPath);
    if (!clean) return;
    const isInbox = isInboxFolderPath(accountId, clean);
    const key = isInbox ? inboxClean.toLowerCase() : clean.toLowerCase();
    if (!entriesByKey.has(key)) {
      entriesByKey.set(key, {
        path: isInbox ? inboxClean : clean,
        label: folderDisplayLabel(accountId, isInbox ? inboxClean : clean),
        isInbox,
      });
    }
  };

  if (Array.isArray(configured)) {
    for (const p of configured) addEntry(p);
  } else {
    addEntry(inboxClean);
  }
  for (const c of openItems || []) {
    if (c.accountId !== accountId) continue;
    const l1 = window.OmaCore?.level1FolderName ? window.OmaCore.level1FolderName(c.folder) : "";
    if (l1) addEntry(`${inboxClean}/${l1}`);
  }
  return [...entriesByKey.values()].sort((a, b) => {
    if (a.isInbox !== b.isInbox) return a.isInbox ? -1 : 1;
    return a.label.localeCompare(b.label, undefined, { sensitivity: "base" });
  });
}

function getAccountScopedItems() {
  const items = getAllItems();
  if (selectedAccountId === "ALL") return items;
  const byAccount = items.filter((c) => c.accountId === selectedAccountId);
  if (selectedFolder === "ALL") return byAccount;
  return byAccount.filter((c) => matchesSelectedFolder(c, selectedAccountId, selectedFolder));
}

function getFilteredItems() {
  const items = getAccountScopedItems();
  if (selectedSpace === "ALL") return items;
  if (selectedSpace === "UNASSIGNED") return items.filter((c) => !c.space);
  return items.filter((c) => c.space === selectedSpace);
}

function isOpenKanbanCard(c) {
  return window.OmaCore?.isOpenKanbanCard ? window.OmaCore.isOpenKanbanCard(c) : Boolean(c) && (c.kanbanColumn || "incoming") !== "done";
}

function renderKanbanAccountNav() {
  const nav = document.getElementById("kanban-account-nav");
  const parentBtn = document.querySelector('.nav-item[data-view="kanban"]');
  const accounts = kanbanAccounts();
  if (selectedAccountId !== "ALL" && !accounts.some((a) => a.id === selectedAccountId)) {
    selectedAccountId = "ALL";
    selectedFolder = "ALL";
  }
  if (selectedAccountId === "ALL") {
    selectedFolder = "ALL";
  }
  if (parentBtn) {
    parentBtn.classList.toggle("active", currentView === "kanban" && selectedAccountId === "ALL");
  }
  if (!nav) return;
  if (!feature("triage") || hostOffline() || accounts.length === 0) {
    nav.style.display = "none";
    nav.innerHTML = "";
    return;
  }
  const openAll = getAllItems().filter(isOpenKanbanCard);
  if (selectedAccountId !== "ALL" && selectedFolder !== "ALL") {
    const activeEntries = folderEntriesForAccount(selectedAccountId, openAll);
    const hasMatch = activeEntries.some((e) => e.path.toLowerCase() === cleanFolderPath(selectedFolder).toLowerCase());
    if (!hasMatch) selectedFolder = "ALL";
  }
  nav.style.display = "flex";
  nav.innerHTML = accounts.map((acc) => {
    const accOpen = inExtension && snapshot.connected
      ? openAll.filter((c) => c.accountId === acc.id)
      : openAll;
    const isAccActive = currentView === "kanban" && selectedAccountId === acc.id && selectedFolder === "ALL";
    const label = acc.name || acc.id;
    const entries = folderEntriesForAccount(acc.id, accOpen);
    const showFolders = entries.some((e) => !e.isInbox);
    const foldersHtml = showFolders
      ? `<div class="nav-sublist nested">${entries.map((entry) => {
          const folderCount = accOpen.filter((c) => matchesSelectedFolder(c, acc.id, entry.path)).length;
          const isFolderActive = currentView === "kanban"
            && selectedAccountId === acc.id
            && selectedFolder !== "ALL"
            && cleanFolderPath(selectedFolder).toLowerCase() === entry.path.toLowerCase();
          return `<button class="nav-subitem folder ${isFolderActive ? "active" : ""}" data-kanban-acc="${escapeHtml(acc.id)}" data-kanban-folder="${escapeHtml(entry.path)}" title="${escapeHtml(entry.path)}">
            <span class="nav-sub-label">${escapeHtml(entry.label)}</span>
            <span class="nav-count">${folderCount}</span>
          </button>`;
        }).join("")}</div>`
      : "";
    return `<button class="nav-subitem ${isAccActive ? "active" : ""}" data-kanban-acc="${escapeHtml(acc.id)}" title="${escapeHtml(label)}">
      <span class="nav-sub-label">${escapeHtml(label)}</span>
      <span class="nav-count">${accOpen.length}</span>
    </button>${foldersHtml}`;
  }).join("");

  nav.querySelectorAll("[data-kanban-acc]").forEach((btn) => {
    btn.addEventListener("click", () => {
      document.querySelectorAll(".nav-item").forEach((b) => b.classList.remove("active"));
      currentView = "kanban";
      selectedAccountId = btn.dataset.kanbanAcc;
      selectedFolder = btn.dataset.kanbanFolder || "ALL";
      selectedSpace = "ALL";
      selectedCardIds = new Set();
      lastSelectedAnchorId = null;
      if (selectedMessageId) {
        const cls = snapshot.state?.classifications?.[selectedMessageId];
        if (cls && ((cls.accountId && cls.accountId !== selectedAccountId) || (selectedFolder !== "ALL" && !matchesSelectedFolder(cls, selectedAccountId, selectedFolder)))) {
          selectedMessageId = null;
        }
      }
      renderCounts();
      renderSpaceFilterBar();
      renderMain();
      renderRightPane();
    });
  });
}

function renderCounts() {
  const all = getAllItems();
  const openAll = all.filter(isOpenKanbanCard);
  const scoped = getAccountScopedItems();
  const triageCounts = feature("triage") && !standaloneMode();
  const cntKanban = document.getElementById("cnt-kanban");
  cntKanban.textContent = triageCounts ? openAll.length : "–";
  cntKanban.title = t("cnt_kanban_open_title");
  document.getElementById("cnt-rules").textContent = triageCounts ? (snapshot.config?.rules || []).length : "–";
  const cntArchive = document.getElementById("cnt-archive");
  if (cntArchive) cntArchive.textContent = triageCounts ? (archiveStatsData.fileCount ?? 0) : "–";
  document.getElementById("cnt-themes").textContent = (snapshot.themes || []).length;

  const stage0 = scoped.filter((c) => c.stage === "stage0-rule").length;
  const pct = scoped.length ? Math.round((stage0 / scoped.length) * 100) : 0;
  document.getElementById("savings-badge").textContent = t("stage0_savings", { pct });

  const badge = document.getElementById("host-badge");
  if (snapshot.connected) { badge.className = "oma-badge ok"; badge.textContent = t("host_active"); }
  else if (!inExtension) { badge.className = "oma-badge accent"; badge.textContent = t("host_preview"); }
  else { badge.className = "oma-badge accent"; badge.textContent = t("host_standalone"); }

  const triageOn = feature("triage") && !standaloneMode();
  document.getElementById("readonly-badge").style.display = triageOn && isReadOnly() ? "" : "none";
  document.getElementById("readonly-badge").title = t("badge_readonly_title");
  const triageBtn = document.getElementById("btn-triage");
  triageBtn.style.display = triageOn && kanbanAccounts().length > 0 ? "" : "none";
  triageBtn.title = selectedAccountId !== "ALL" ? accountNameById(selectedAccountId) : "";
  document.getElementById("savings-badge").style.display = triageOn && kanbanAccounts().length > 0 ? "" : "none";

  const aiCfg = snapshot.config?.ai;
  if (aiCfg) {
    document.getElementById("lbl-classifier").textContent = `Stage 1: ${aiCfg.classifier?.model || aiCfg.classifier?.backend}`;
    document.getElementById("lbl-agent").textContent = `Stage 2: ${aiCfg.agent?.backend}`;
  }
  renderKanbanAccountNav();
}

function renderSpaceFilterBar() {
  const bar = document.getElementById("space-filter-bar");
  if (currentView !== "kanban") {
    bar.style.display = "none";
    return;
  }
  if (!feature("triage") || standaloneMode() || (inExtension && snapshot.connected && kanbanAccounts().length === 0)) {
    bar.style.display = "none";
    return;
  }
  bar.style.display = "flex";
  const spaces = new Set();
  for (const r of snapshot.config?.rules || []) {
    if (!r.space) continue;
    if (selectedAccountId !== "ALL" && r.accountId && r.accountId !== selectedAccountId) continue;
    spaces.add(r.space);
  }
  for (const c of getAccountScopedItems()) if (c.space) spaces.add(c.space);

  const pills = [
    { key: "ALL", label: t("filter_all") },
    ...[...spaces].sort().map((c) => ({ key: c, label: c })),
    { key: "UNASSIGNED", label: t("filter_unassigned") },
  ];
  const autoSyncOn = Boolean(snapshot.config?.mailbox?.scanOnNewMail);
  bar.innerHTML = `<span class="oma-muted" style="font-size:11px; margin-right:4px;">${escapeHtml(t("filter_space"))}</span>` +
    pills.map((p) => `<button class="filter-pill ${selectedSpace === p.key ? "active" : ""}" data-space="${escapeHtml(p.key)}">${escapeHtml(p.label)}</button>`).join("") +
    `<button class="filter-pill ${autoSyncOn ? "active" : ""}" id="btn-toggle-autosync" style="margin-left:auto;" title="${escapeHtml(t("kanban_autosync_title"))}">${escapeHtml(autoSyncOn ? t("kanban_autosync_on") : t("kanban_autosync_off"))}</button>`;

  bar.querySelectorAll("[data-space]").forEach((btn) => {
    btn.addEventListener("click", () => {
      selectedSpace = btn.dataset.space;
      renderMain();
      renderSpaceFilterBar();
      renderRightPane();
    });
  });

  document.getElementById("btn-toggle-autosync")?.addEventListener("click", async () => {
    const nextVal = !Boolean(snapshot.config?.mailbox?.scanOnNewMail);
    if (inExtension && snapshot.connected) {
      try {
        const res = await api.runtime.sendMessage({
          type: "omathunder:saveConfig",
          config: { mailbox: { scanOnNewMail: nextVal } },
        });
        if (res?.config) snapshot.config = res.config;
      } catch (err) {
        showMessage(errText(err), "err");
        return;
      }
    } else {
      snapshot.config = {
        ...(snapshot.config || {}),
        mailbox: { ...(snapshot.config?.mailbox || {}), scanOnNewMail: nextVal },
      };
    }
    renderSpaceFilterBar();
  });
}

function clearCardSelection() {
  selectedCardIds = new Set();
  lastSelectedAnchorId = null;
}

function pruneSelectedCards() {
  if (selectedCardIds.size === 0) return;
  const visible = new Set(getFilteredItems().map((c) => c.messageId));
  const next = new Set();
  for (const id of selectedCardIds) {
    if (visible.has(id)) next.add(id);
  }
  selectedCardIds = next;
  if (lastSelectedAnchorId && !visible.has(lastSelectedAnchorId)) {
    lastSelectedAnchorId = null;
  }
}

async function markItemsAsRead(items, { column = "", button = null, buttonId = "", defaultLabel = "" } = {}) {
  if (isReadOnly()) {
    showMessage(t("err_read_only"), "warn");
    return false;
  }
  if (!items || items.length === 0) return false;
  if (button) {
    button.disabled = true;
    button.textContent = t("kanban_mark_read_working");
  }
  try {
    const res = await api.runtime.sendMessage({
      type: column ? "omathunder:markColumnRead" : "omathunder:markCardsRead",
      column,
      items: items.map((c) => ({
        messageId: c.messageId,
        headerMessageId: c.threadId || "",
        accountId: c.accountId || "",
        folderId: c.folderId || "",
        kanbanColumn: c.kanbanColumn || "incoming",
      })),
    });
    if (res?.state) {
      snapshot.state = res.state;
      if (res?.barSummary) snapshot.barSummary = res.barSummary;
    } else {
      await refreshSnapshot();
    }
    if (selectedMessageId && !snapshot.state?.classifications?.[selectedMessageId]) {
      selectedMessageId = null;
    }
    pruneSelectedCards();
    renderCounts();
    renderSpaceFilterBar();
    renderMain();
    renderRightPane();
    showMessage(t("kanban_mark_read_report", {
      marked: res?.marked ?? 0,
      alreadyRead: res?.alreadyRead ?? 0,
      removed: res?.removed ?? 0,
      skipped: res?.skipped ?? 0,
    }), res?.failed ? "warn" : "ok");
    return true;
  } catch (err) {
    showMessage(errText(err), "err");
    return false;
  } finally {
    const liveBtn = buttonId ? document.getElementById(buttonId) : button;
    if (liveBtn && defaultLabel) {
      liveBtn.disabled = false;
      liveBtn.textContent = defaultLabel;
    }
  }
}

function renderSelectionBar() {
  const bar = document.getElementById("kanban-selection-bar");
  if (!bar) return;
  const kanbanReady = currentView === "kanban" && !standaloneMode() && feature("triage")
    && !(inExtension && snapshot.connected && kanbanAccounts().length === 0);
  if (!kanbanReady) {
    bar.style.display = "none";
    bar.innerHTML = "";
    return;
  }
  pruneSelectedCards();
  const visibleItems = getFilteredItems();
  const selectedItems = visibleItems.filter((c) => selectedCardIds.has(c.messageId));
  if (selectedItems.length === 0) {
    bar.style.display = "none";
    bar.innerHTML = "";
    return;
  }
  const cols = getKanbanColumns();
  const hasNotDone = selectedItems.some((c) => (c.kanbanColumn || "incoming") !== "done");
  bar.style.display = "flex";
  bar.innerHTML = `
    <span class="oma-badge accent">${escapeHtml(t("kanban_sel_count", { count: selectedItems.length }))}</span>
    <span class="oma-muted" style="font-size:11px;">${escapeHtml(t("kanban_sel_move_lbl"))}</span>
    <div style="display:flex; gap:4px; flex-wrap:wrap; align-items:center;">
      ${cols.map((col) => `<button class="mini-btn" data-sel-move-col="${col.id}">${escapeHtml(col.label)}</button>`).join("")}
    </div>
    ${inExtension ? `
      <div style="display:flex; gap:6px; align-items:center; margin-left:4px;">
        ${hasNotDone ? `<button class="oma-btn primary" id="btn-sel-done-read" style="padding:4px 10px; font-size:11.5px;" title="${escapeHtml(t("kanban_sel_done_read_title"))}">${escapeHtml(t("kanban_sel_done_read"))}</button>` : ""}
        <button class="oma-btn" id="btn-sel-mark-read" style="padding:4px 10px; font-size:11.5px;" title="${escapeHtml(t("kanban_sel_mark_read_title"))}">${escapeHtml(t("kanban_sel_mark_read"))}</button>
      </div>
    ` : ""}
    <button class="oma-btn ghost" id="btn-sel-clear" style="margin-left:auto; padding:4px 8px; font-size:11px;">${escapeHtml(t("kanban_sel_clear"))}</button>
  `;

  bar.querySelectorAll("[data-sel-move-col]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      await moveKanbanCards(selectedItems.map((c) => c.messageId), btn.dataset.selMoveCol);
    });
  });

  document.getElementById("btn-sel-mark-read")?.addEventListener("click", async (e) => {
    await markItemsAsRead(selectedItems, {
      button: e.currentTarget,
      buttonId: "btn-sel-mark-read",
      defaultLabel: t("kanban_sel_mark_read"),
    });
  });

  document.getElementById("btn-sel-done-read")?.addEventListener("click", async (e) => {
    if (isReadOnly()) {
      showMessage(t("err_read_only"), "warn");
      return;
    }
    const btn = e.currentTarget;
    btn.disabled = true;
    btn.textContent = t("kanban_mark_read_working");
    try {
      const ids = selectedItems.map((c) => c.messageId);
      await moveKanbanCards(ids, "done", { silentRender: true });
      const updatedItems = ids
        .map((id) => snapshot.state?.classifications?.[id] || selectedItems.find((c) => c.messageId === id))
        .filter(Boolean)
        .map((c) => ({ ...c, kanbanColumn: "done" }));
      await markItemsAsRead(updatedItems, { column: "done" });
    } finally {
      const liveBtn = document.getElementById("btn-sel-done-read");
      if (liveBtn) {
        liveBtn.disabled = false;
        liveBtn.textContent = t("kanban_sel_done_read");
      }
    }
  });

  document.getElementById("btn-sel-clear")?.addEventListener("click", () => {
    clearCardSelection();
    renderMain();
  });
}

function renderMain() {
  const container = document.getElementById("main-content");
  const titles = { kanban: t("view_kanban"), rules: t("view_rules"), archive: t("view_archive"), themes: t("view_themes") };
  const activeAccLabel = currentView === "kanban" && selectedAccountId !== "ALL" ? accountNameById(selectedAccountId) : "";
  const activeFolderLabel = activeAccLabel && selectedFolder !== "ALL" ? folderDisplayLabel(selectedAccountId, selectedFolder) : "";
  document.getElementById("view-title").textContent = activeAccLabel
    ? (activeFolderLabel ? `${titles.kanban} · ${activeAccLabel} · ${activeFolderLabel}` : `${titles.kanban} · ${activeAccLabel}`)
    : (titles[currentView] || "OmaThunder");

  if (standaloneMode() && currentView !== "themes") {
    renderSelectionBar();
    container.innerHTML = optionalHostHtml();
    bindSettingsButtons(container);
    container.querySelector("[data-retry]")?.addEventListener("click", () => refreshSnapshot());
    container.querySelector("[data-go-themes]")?.addEventListener("click", () => {
      currentView = "themes";
      document.querySelectorAll(".nav-item").forEach((b) => b.classList.toggle("active", b.dataset.view === "themes"));
      renderCounts();
      renderSpaceFilterBar();
      renderMain();
      renderRightPane();
    });
    return;
  }

  if ((currentView === "kanban" || currentView === "rules" || currentView === "archive") && !feature("triage")) {
    renderSelectionBar();
    container.innerHTML = noticeHtml(t("feat_off_triage"));
    bindSettingsButtons(container);
    return;
  }
  if (currentView === "themes" && !feature("theme")) {
    renderSelectionBar();
    container.innerHTML = noticeHtml(t("feat_off_theme"));
    bindSettingsButtons(container);
    return;
  }

  if (currentView === "kanban") {
    if (inExtension && snapshot.connected && kanbanAccounts().length === 0) {
      renderSelectionBar();
      container.innerHTML = noticeHtml(t("kanban_no_accounts"));
      bindSettingsButtons(container);
      return;
    }
    const items = getFilteredItems();
    const cols = getKanbanColumns();
    pruneSelectedCards();
    renderSelectionBar();
    const doneItems = items.filter((c) => (c.kanbanColumn || "incoming") === "done");
    const colItemsByCol = new Map();
    for (const col of cols) {
      colItemsByCol.set(col.id, items.filter((c) => (c.kanbanColumn || "incoming") === col.id));
    }
    const visibleIds = cols.flatMap((col) => (colItemsByCol.get(col.id) || []).map((c) => c.messageId));
    const hasSelection = selectedCardIds.size > 0;

    container.innerHTML = `<div class="kanban-board ${hasSelection ? "has-selection" : ""}">${cols.map((col, idx) => {
      const colItems = colItemsByCol.get(col.id) || [];
      const allColSelected = colItems.length > 0 && colItems.every((c) => selectedCardIds.has(c.messageId));
      const showMarkRead = col.id === "done" && colItems.length > 0 && inExtension;
      return `
        <div class="kanban-col" data-col="${col.id}">
          <div class="kanban-col-hdr">
            <label class="kanban-col-title-wrap" title="${escapeHtml(colItems.length > 0 ? t("kanban_sel_col_title") : col.label)}">
              ${colItems.length > 0 ? `<input type="checkbox" class="k-col-check" data-col-select="${col.id}" ${allColSelected ? "checked" : ""} />` : ""}
              <span>${escapeHtml(col.label)}</span>
            </label>
            <div style="display:flex; align-items:center; gap:5px;">
              ${showMarkRead ? `<button class="mini-btn" id="btn-done-mark-read" title="${escapeHtml(t("kanban_mark_done_read_title"))}">${escapeHtml(t("kanban_mark_done_read"))}</button>` : ""}
              <span class="nav-count">${colItems.length}</span>
            </div>
          </div>
          <div class="kanban-cards">
            ${colItems.map((c) => {
              const isMultiSelected = selectedCardIds.has(c.messageId);
              const l1Folder = !c.space && window.OmaCore?.level1FolderName ? window.OmaCore.level1FolderName(c.folder) : "";
              const relFolder = window.OmaCore?.relativeFolderLabel ? window.OmaCore.relativeFolderLabel(c.folder) : (c.folder || "");
              const spaceBadge = c.space
                ? `<span class="oma-badge accent">${escapeHtml(c.space)}</span>`
                : l1Folder
                  ? `<span class="oma-badge accent">${escapeHtml(l1Folder)}</span>`
                  : `<span class="oma-badge">${escapeHtml(t("badge_inbox"))}</span>`;
              return `
              <div class="k-card ${c.messageId === selectedMessageId ? "selected" : ""} ${isMultiSelected ? "multi-selected" : ""}" draggable="true" data-msgid="${escapeHtml(c.messageId)}" data-col-id="${col.id}">
                <div class="k-card-top">
                  <div class="k-card-top-left">
                    <input type="checkbox" class="k-card-check" data-check-id="${escapeHtml(c.messageId)}" data-col-id="${col.id}" ${isMultiSelected ? "checked" : ""} title="${escapeHtml(t("kanban_sel_card_title"))}" />
                    ${spaceBadge}
                  </div>
                  <span class="oma-badge ${c.urgency === "high" ? "err" : c.urgency === "medium" ? "warn" : "ok"}">${escapeHtml(c.urgency || "low")}</span>
                </div>
                <div class="k-card-title">${escapeHtml(c.subject)}</div>
                <div class="k-card-from">${escapeHtml(c.from)}</div>
                <div class="k-card-sum">${escapeHtml(summaryText(c))}</div>
                <div class="k-card-actions">
                  <span class="oma-mono oma-muted" style="font-size:10px;">${escapeHtml(
                    (selectedAccountId === "ALL" && kanbanAccounts().length > 1 && c.accountId
                      ? `${accountNameById(c.accountId)} · `
                      : "") + (c.deadline || c.amount || relFolder || "")
                  )}</span>
                  <div style="display:flex; gap:4px;">
                    ${inExtension ? `<button class="mini-btn" data-open-id="${escapeHtml(c.messageId)}" title="${escapeHtml(t("card_open"))}">↗</button>` : ""}
                    ${idx > 0 ? `<button class="mini-btn" data-move-id="${escapeHtml(c.messageId)}" data-move-col="${cols[idx - 1].id}" title="${escapeHtml(t("card_prev"))}">←</button>` : ""}
                    ${idx < cols.length - 1 ? `<button class="mini-btn" data-move-id="${escapeHtml(c.messageId)}" data-move-col="${cols[idx + 1].id}" title="${escapeHtml(t("card_next"))}">→</button>` : ""}
                  </div>
                </div>
              </div>
            `;
            }).join("")}
          </div>
        </div>
      `;
    }).join("")}</div>`;

    const selectRangeForCard = (colOrdered, msgId) => {
      window.getSelection?.()?.removeAllRanges?.();
      if (!window.OmaCore?.selectCardRange) return;
      const anchor = lastSelectedAnchorId || selectedMessageId;
      const rangeList = anchor && colOrdered.includes(anchor) ? colOrdered : visibleIds;
      selectedCardIds = window.OmaCore.selectCardRange(selectedCardIds, rangeList, anchor, msgId);
    };

    container.querySelectorAll("[data-col-select]").forEach((cb) => {
      const colId = cb.dataset.colSelect;
      const colIds = (colItemsByCol.get(colId) || []).map((c) => c.messageId);
      const selectedInCol = colIds.filter((id) => selectedCardIds.has(id)).length;
      cb.indeterminate = selectedInCol > 0 && selectedInCol < colIds.length;
      cb.addEventListener("click", (e) => {
        e.stopPropagation();
        selectedCardIds = window.OmaCore?.toggleColumnSelection
          ? window.OmaCore.toggleColumnSelection(selectedCardIds, colIds)
          : new Set();
        lastSelectedAnchorId = null;
        renderMain();
      });
    });

    document.getElementById("btn-done-mark-read")?.addEventListener("click", async (e) => {
      e.stopPropagation();
      await markItemsAsRead(doneItems, {
        column: "done",
        button: e.currentTarget,
        buttonId: "btn-done-mark-read",
        defaultLabel: t("kanban_mark_done_read"),
      });
    });

    container.querySelectorAll(".k-card-check").forEach((cb) => {
      cb.addEventListener("click", (e) => {
        e.stopPropagation();
        const msgId = cb.dataset.checkId;
        const colOrdered = (colItemsByCol.get(cb.dataset.colId) || []).map((c) => c.messageId);
        if (e.shiftKey) {
          selectRangeForCard(colOrdered, msgId);
        } else if (window.OmaCore?.toggleCardSelection) {
          selectedCardIds = window.OmaCore.toggleCardSelection(selectedCardIds, msgId);
        }
        lastSelectedAnchorId = msgId;
        renderMain();
      });
    });

    container.querySelectorAll(".k-card").forEach((card) => {
      card.addEventListener("mousedown", (e) => {
        if (e.shiftKey && !e.target.closest("input, button")) {
          e.preventDefault();
        }
      });
      card.addEventListener("click", (e) => {
        const msgId = card.dataset.msgid;
        const colOrdered = (colItemsByCol.get(card.dataset.colId) || []).map((c) => c.messageId);
        if (e.shiftKey) {
          selectRangeForCard(colOrdered, msgId);
          lastSelectedAnchorId = msgId;
          renderMain();
          return;
        }
        if ((e.metaKey || e.ctrlKey) && window.OmaCore?.toggleCardSelection) {
          window.getSelection?.()?.removeAllRanges?.();
          selectedCardIds = window.OmaCore.toggleCardSelection(selectedCardIds, msgId);
          lastSelectedAnchorId = msgId;
          renderMain();
          return;
        }
        lastSelectedAnchorId = msgId;
        if (selectedMessageId === msgId && assistantDrawerOpen) {
          selectedMessageId = null;
          assistantDrawerOpen = false;
        } else {
          selectedMessageId = msgId;
          assistantDrawerOpen = true;
        }
        renderMain();
        renderRightPane();
      });
      card.addEventListener("dragstart", (e) => {
        const msgId = card.dataset.msgid;
        const dragIds = window.OmaCore?.resolveDraggedCardIds
          ? window.OmaCore.resolveDraggedCardIds(msgId, selectedCardIds, visibleIds)
          : [msgId];
        draggingCardIds = dragIds;
        if (e.dataTransfer) {
          e.dataTransfer.effectAllowed = "move";
          e.dataTransfer.setData("application/x-omathunder-cards", JSON.stringify(dragIds));
          e.dataTransfer.setData("text/plain", msgId);
          if (dragIds.length > 1 && e.dataTransfer.setDragImage) {
            const ghost = document.createElement("div");
            ghost.className = "drag-ghost-pill";
            ghost.textContent = t("kanban_drag_multi", { count: dragIds.length });
            document.body.appendChild(ghost);
            e.dataTransfer.setDragImage(ghost, 14, 14);
            setTimeout(() => ghost.remove(), 0);
          }
        }
        const dragSet = new Set(dragIds);
        container.querySelectorAll(".k-card").forEach((el) => {
          if (dragSet.has(el.dataset.msgid)) el.classList.add("dragging");
        });
      });
      card.addEventListener("dragend", () => {
        draggingCardIds = [];
        container.querySelectorAll(".k-card.dragging").forEach((el) => el.classList.remove("dragging"));
        container.querySelectorAll(".kanban-col.drag-over").forEach((el) => el.classList.remove("drag-over"));
      });
    });

    container.querySelectorAll(".kanban-col").forEach((colEl) => {
      colEl.addEventListener("dragenter", (e) => {
        e.preventDefault();
        if (e.dataTransfer) e.dataTransfer.dropEffect = "move";
        colEl.classList.add("drag-over");
      });
      colEl.addEventListener("dragover", (e) => {
        e.preventDefault();
        if (e.dataTransfer) e.dataTransfer.dropEffect = "move";
        colEl.classList.add("drag-over");
      });
      colEl.addEventListener("dragleave", (e) => {
        if (!e.relatedTarget || !colEl.contains(e.relatedTarget)) {
          colEl.classList.remove("drag-over");
        }
      });
      colEl.addEventListener("drop", async (e) => {
        e.preventDefault();
        colEl.classList.remove("drag-over");
        let dragIds = [];
        const rawJson = e.dataTransfer?.getData("application/x-omathunder-cards");
        if (rawJson) {
          try {
            const parsed = JSON.parse(rawJson);
            if (Array.isArray(parsed)) dragIds = parsed.map(String).filter(Boolean);
          } catch (_) {}
        }
        if (dragIds.length === 0 && draggingCardIds.length > 0) {
          dragIds = [...draggingCardIds];
        }
        if (dragIds.length === 0) {
          const single = e.dataTransfer?.getData("text/plain");
          if (single) dragIds = [single];
        }
        draggingCardIds = [];
        if (dragIds.length > 0) await moveKanbanCards(dragIds, colEl.dataset.col);
      });
    });

    container.querySelectorAll("[data-open-id]").forEach((btn) => {
      btn.addEventListener("click", async (e) => {
        e.stopPropagation();
        try {
          await api.runtime.sendMessage({ type: "omathunder:openMessage", messageId: btn.dataset.openId });
        } catch (err) {
          showMessage(errText(err), "err");
        }
      });
    });

    container.querySelectorAll("[data-move-id]").forEach((btn) => {
      btn.addEventListener("click", async (e) => {
        e.stopPropagation();
        await moveKanbanCard(btn.dataset.moveId, btn.dataset.moveCol);
      });
    });
    return;
  }

  renderSelectionBar();

  if (currentView === "rules") {
    const rules = snapshot.config?.rules || [];
    const accounts = triageAccounts();
    const allAccounts = snapshot.accounts || [];
    const accountIds = new Set(accounts.map((a) => a.id));
    const folderOpts = (snapshot.folders || []).filter((f) => !inExtension || accountIds.has(f.accountId)).map((f) =>
      `<option value="${escapeHtml(f.path)}">${escapeHtml(f.accountName ? `[${f.accountName}] ${f.path}` : f.path)}</option>`
    ).join("");
    const accountOpts = accounts.map((a) => `<option value="${escapeHtml(a.id)}">${escapeHtml(a.name || a.id)}</option>`).join("");
    const accountName = (id) => allAccounts.find((a) => a.id === id)?.name || id || "—";
    const defaultFilterAccId = accounts[0]?.id || nativeFiltersData.accounts?.[0]?.accountId || allAccounts[0]?.id || "";

    const nativeSupported = Boolean(nativeFiltersData.supported);
    const nativeFilters = nativeFiltersData.filters || [];
    const nativeIssues = nativeFiltersData.issues || [];
    const importCandidates = nativeFiltersData.candidates || [];
    const activeNativeCount = nativeFilters.filter((f) => f.enabled).length;

    const findLinkedNativeFilters = (rule) => {
      const names = new Set((rule.nativeFilterNames || []).map((n) => String(n).toLowerCase()));
      const cleanFolder = String(rule.folderPath || "").replace(/^\/+|\/+$/g, "").toLowerCase();
      return nativeFilters.filter((f) => {
        if (rule.accountId && f.accountId !== rule.accountId) return false;
        if (names.has(String(f.name || "").toLowerCase())) return true;
        if (names.size === 0 && cleanFolder) {
          return (f.actions || []).some(
            (a) => a.type === "moveToFolder" && String(a.targetFolderPath || "").replace(/^\/+|\/+$/g, "").toLowerCase() === cleanFolder,
          );
        }
        return false;
      });
    };

    container.innerHTML = `
      <div class="oma-card" style="margin-bottom:14px;">
        <div style="display:flex; justify-content:space-between; align-items:center; gap:10px; flex-wrap:wrap;">
          <div>
            <div style="font-weight:700; font-size:13px;">${escapeHtml(t("native_bridge_title"))}</div>
            <div class="oma-muted" style="font-size:11.5px; margin-top:2px;">
              ${escapeHtml(nativeSupported
                ? t("native_bridge_summary", { active: activeNativeCount, total: nativeFilters.length, issues: nativeIssues.length })
                : t("native_bridge_unsupported"))}
            </div>
          </div>
          <div style="display:flex; gap:8px; flex-wrap:wrap; align-items:center;">
            ${nativeIssues.length > 0 ? `<span class="oma-badge warn">${escapeHtml(t("native_issues_badge", { count: nativeIssues.length }))}</span>` : ""}
            ${nativeSupported ? `<button class="oma-btn primary" id="btn-toggle-import">${escapeHtml(t("native_btn_import"))}</button>` : ""}
            ${nativeSupported && defaultFilterAccId ? `<button class="oma-btn" id="btn-run-all-native">${escapeHtml(t("native_btn_run_all"))}</button>` : ""}
            ${nativeSupported ? `<button class="oma-btn ghost" id="btn-open-tb-filters">${escapeHtml(t("native_btn_dialog"))}</button>` : ""}
          </div>
        </div>
        ${showImportPanel ? `
          <div style="margin-top:12px; padding-top:12px; border-top:1px solid var(--oma-border);">
            <div style="display:flex; justify-content:space-between; align-items:center; gap:8px; margin-bottom:8px;">
              <strong style="font-size:12px;">${escapeHtml(t("native_import_title"))}</strong>
              <span class="oma-muted" style="font-size:11px;">${escapeHtml(t("native_import_desc"))}</span>
            </div>
            ${importCandidates.length === 0 ? `<div class="oma-muted" style="font-size:11.5px;">${escapeHtml(t("native_import_empty"))}</div>` : `
              <div style="display:flex; flex-direction:column; gap:6px; max-height:260px; overflow-y:auto;">
                ${importCandidates.map((c, idx) => `
                  <div style="display:grid; grid-template-columns:auto 180px 1fr auto; gap:8px; align-items:center; padding:6px 8px; border:1px solid var(--oma-border); border-radius:6px; background:var(--oma-bg-darker);">
                    <input type="checkbox" data-import-idx="${idx}" ${c.alreadyImported ? "" : "checked"} />
                    <input class="oma-input" data-import-space="${idx}" value="${escapeHtml(c.space)}" style="padding:4px 7px; font-size:11.5px;" />
                    <div>
                      <div class="oma-mono" style="font-size:11px;">${escapeHtml(c.folderPath)} <span class="oma-muted">(${escapeHtml(c.accountName || accountName(c.accountId))})</span></div>
                      <div class="oma-muted oma-mono" style="font-size:10.5px;">${escapeHtml([...(c.domains || []), ...(c.fromContains || [])].join(", ") || (c.nativeFilterNames || []).join(", "))}</div>
                    </div>
                    ${c.alreadyImported ? `<span class="oma-badge ok">${escapeHtml(t("native_import_linked"))}</span>` : `<span class="oma-badge accent">${escapeHtml(t("native_import_new"))}</span>`}
                  </div>
                `).join("")}
              </div>
              <div style="display:flex; justify-content:flex-end; gap:8px; margin-top:10px;">
                <button class="oma-btn ghost" id="btn-cancel-import">${escapeHtml(t("btn_cancel"))}</button>
                <button class="oma-btn primary" id="btn-confirm-import">${escapeHtml(t("native_btn_confirm_import"))}</button>
              </div>
            `}
          </div>
        ` : ""}
      </div>

      <div class="oma-card" style="margin-bottom:14px;">
        <div style="font-weight:700; margin-bottom:6px;">${escapeHtml(t(editingRuleId ? "rules_edit_title" : "rules_new_title"))}</div>
        <div class="oma-muted" style="font-size:11.5px; margin-bottom:10px;">
          ${escapeHtml(t("rules_new_desc"))}
        </div>
        ${accounts.length === 0 ? `<div class="oma-badge warn" style="margin-bottom:8px;">${escapeHtml(t("rules_no_account"))}</div>` : ""}
        <div class="rules-form">
          <div>
            <label class="oma-muted" style="font-size:11px;">${escapeHtml(t("rules_lbl_account"))}</label>
            <select class="oma-select" id="rule-account" ${accounts.length === 0 ? "disabled" : ""}>${accountOpts}</select>
          </div>
          <div>
            <label class="oma-muted" style="font-size:11px;">${escapeHtml(t("rules_lbl_space"))}</label>
            <input class="oma-input" id="rule-space" placeholder="${escapeHtml(t("rules_ph_space"))}" />
          </div>
          <div>
            <label class="oma-muted" style="font-size:11px;">${escapeHtml(t("rules_lbl_folder"))}</label>
            <input class="oma-input" id="rule-folder" list="tb-folders" placeholder="${escapeHtml(t("rules_ph_folder"))}" />
            <datalist id="tb-folders">${folderOpts}</datalist>
          </div>
          <div>
            <label class="oma-muted" style="font-size:11px;">${escapeHtml(t("rules_lbl_domains"))}</label>
            <input class="oma-input" id="rule-domains" placeholder="${escapeHtml(t("rules_ph_domains"))}" />
          </div>
          <button class="oma-btn primary" id="btn-add-rule" ${accounts.length === 0 ? "disabled" : ""}>${escapeHtml(t(editingRuleId ? "rules_btn_save" : "rules_btn_add"))}</button>
        </div>
        <div style="display:flex; justify-content:space-between; align-items:center; gap:14px; margin-top:8px; flex-wrap:wrap;">
          <label style="display:flex; align-items:center; gap:6px; font-size:11.5px; cursor:pointer;">
            <input type="checkbox" id="chk-autocreate-folder" checked />
            <span>${escapeHtml(t("rules_autocreate"))}</span>
          </label>
          ${nativeSupported ? `
            <label style="display:flex; align-items:center; gap:6px; font-size:11.5px; cursor:pointer;">
              <input type="checkbox" id="chk-sync-native" checked />
              <span>${escapeHtml(t("rules_sync_native"))}</span>
            </label>
          ` : ""}
        </div>
        <details style="margin-top:8px;">
          <summary class="oma-muted" style="font-size:11px; cursor:pointer;">${escapeHtml(t("rules_sub_summary"))}</summary>
          <div style="display:grid; grid-template-columns: 1fr 1fr; gap:8px; margin-top:8px;">
            <div>
              <label class="oma-muted" style="font-size:11px;">${escapeHtml(t("rules_sub_waiting"))}</label>
              <input class="oma-input" id="rule-col-waiting" list="tb-folders" placeholder="${escapeHtml(t("rules_sub_waiting_ph"))}" />
            </div>
            <div>
              <label class="oma-muted" style="font-size:11px;">${escapeHtml(t("rules_sub_done"))}</label>
              <input class="oma-input" id="rule-col-done" list="tb-folders" placeholder="${escapeHtml(t("rules_sub_done_ph"))}" />
            </div>
          </div>
        </details>
      </div>

      <div class="oma-card" style="margin-bottom:14px;">
        <table class="rules-table">
          <thead>
            <tr>
              <th>${escapeHtml(t("rules_th_space"))}</th>
              <th>${escapeHtml(t("rules_th_folder"))}</th>
              <th>${escapeHtml(t("rules_th_domains"))}</th>
              <th>${escapeHtml(t("rules_th_native"))}</th>
              <th>${escapeHtml(t("rules_th_automove"))}</th>
              <th>${escapeHtml(t("rules_th_scan"))}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            ${rules.length === 0 ? `<tr><td colspan="7" class="oma-muted">${escapeHtml(t("rules_empty"))}</td></tr>` : rules.map((r) => {
              const subList = Object.entries(r.columnFolders || {}).map(([col, path]) => `${col} → ${path}`).join(" · ");
              const linked = findLinkedNativeFilters(r);
              const anyEnabled = linked.some((f) => f.enabled);
              let nativeCell = `<span class="oma-badge">${escapeHtml(t("rules_native_none"))}</span>`;
              if (nativeSupported) {
                if (linked.length > 0 && anyEnabled) {
                  nativeCell = `<div style="display:flex; align-items:center; gap:5px; flex-wrap:wrap;">
                    <span class="oma-badge ok" title="${escapeHtml(linked.map((f) => f.name).join(", "))}">${escapeHtml(t("rules_native_active", { count: linked.length }))}</span>
                    <button class="mini-btn" data-run-rule-filter="${escapeHtml(r.id)}" title="${escapeHtml(t("diag_run_inbox"))}">▶</button>
                  </div>`;
                } else if (linked.length > 0) {
                  nativeCell = `<div style="display:flex; align-items:center; gap:5px; flex-wrap:wrap;">
                    <span class="oma-badge warn" title="${escapeHtml(linked.map((f) => f.name).join(", "))}">${escapeHtml(t("rules_native_disabled"))}</span>
                    <button class="mini-btn" data-enable-rule-filter="${escapeHtml(r.id)}">${escapeHtml(t("native_btn_enable"))}</button>
                  </div>`;
                } else {
                  nativeCell = `<div style="display:flex; align-items:center; gap:5px; flex-wrap:wrap;">
                    <span class="oma-badge">${escapeHtml(t("rules_native_none"))}</span>
                    <button class="mini-btn" data-create-rule-filter="${escapeHtml(r.id)}">${escapeHtml(t("rules_native_create"))}</button>
                  </div>`;
                }
              }
              return `
              <tr>
                <td><strong>${escapeHtml(r.space)}</strong><div class="oma-muted" style="font-size:10.5px;">${escapeHtml(accountName(r.accountId))}</div></td>
                <td>
                  <div class="oma-mono">${escapeHtml(r.folderPath)}</div>
                  ${subList ? `<div class="oma-muted oma-mono" style="font-size:10.5px; margin-top:2px;">${escapeHtml(subList)}</div>` : `<div class="oma-muted" style="font-size:10.5px; margin-top:2px;">${escapeHtml(t("rules_sub_flex"))}</div>`}
                </td>
                <td class="oma-mono">${escapeHtml((r.domains || []).join(", "))}</td>
                <td>${nativeCell}</td>
                <td><input type="checkbox" data-toggle-move="${escapeHtml(r.id)}" ${r.autoMove ? "checked" : ""} /></td>
                <td><input type="checkbox" data-toggle-scan="${escapeHtml(r.id)}" ${r.includeInScan ? "checked" : ""} /></td>
                <td style="text-align:right; white-space:nowrap;">
                  <button class="oma-btn ghost" data-edit-rule="${escapeHtml(r.id)}">${escapeHtml(t("rules_btn_edit"))}</button>
                  <button class="oma-btn ghost" data-del-rule="${escapeHtml(r.id)}">${escapeHtml(t("rules_btn_delete"))}</button>
                </td>
              </tr>`;
            }).join("")}
          </tbody>
        </table>
      </div>

      ${nativeSupported ? `
        <div class="oma-card">
          <div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:8px;">
            <strong style="font-size:13px;">${escapeHtml(t("native_list_title"))}</strong>
            <span class="oma-muted" style="font-size:11px;">${escapeHtml(t("native_list_sub", { count: nativeFilters.length }))}</span>
          </div>
          ${nativeIssues.length > 0 ? `
            <div style="display:flex; flex-direction:column; gap:6px; margin-bottom:12px;">
              ${nativeIssues.map((iss, idx) => `
                <div style="display:flex; justify-content:space-between; align-items:center; gap:10px; padding:7px 9px; border:1px solid color-mix(in srgb, var(--oma-yellow) 45%, var(--oma-border)); border-radius:6px; background:var(--oma-bg-darker);">
                  <div>
                    <div style="display:flex; align-items:center; gap:6px;">
                      <span class="oma-badge ${iss.severity === "error" ? "err" : "warn"}">${escapeHtml(iss.code)}</span>
                      <strong style="font-size:12px;">${escapeHtml(iss.filterName)}</strong>
                    </div>
                    <div class="oma-muted" style="font-size:11px; margin-top:2px;">${escapeHtml(iss.detail)}</div>
                  </div>
                  ${iss.quickFix ? `<button class="oma-btn" style="padding:4px 8px; font-size:11px; white-space:nowrap;" data-fix-issue-idx="${idx}">${escapeHtml(iss.quickFix === "enable" ? t("diag_fix_enable") : t("diag_fix_set_or"))}</button>` : ""}
                </div>
              `).join("")}
            </div>
          ` : ""}
          ${nativeFilters.length === 0 ? `<div class="oma-muted" style="font-size:11.5px;">${escapeHtml(t("native_filters_empty"))}</div>` : `
            <table class="rules-table">
              <thead>
                <tr>
                  <th>${escapeHtml(t("native_th_on"))}</th>
                  <th>${escapeHtml(t("native_th_name"))}</th>
                  <th>${escapeHtml(t("native_th_logic"))}</th>
                  <th>${escapeHtml(t("native_th_target"))}</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                ${nativeFilters.map((f, idx) => {
                  const target = (f.actions || []).map((a) => a.targetFolderPath ? `${a.type}: ${a.targetFolderPath}` : a.type).join(", ");
                  const condPreview = (f.conditions || []).slice(0, 3).map((c) => `${c.field} ${c.op} "${c.value}"`).join(f.matchAll ? " AND " : " OR ");
                  const extra = (f.conditions || []).length > 3 ? ` (+${f.conditions.length - 3})` : "";
                  return `
                  <tr>
                    <td><input type="checkbox" data-toggle-native-idx="${idx}" ${f.enabled ? "checked" : ""} /></td>
                    <td>
                      <strong>${escapeHtml(f.name)}</strong>
                      <div class="oma-muted" style="font-size:10.5px;">${escapeHtml(f.accountName || accountName(f.accountId))}</div>
                    </td>
                    <td>
                      <span class="oma-badge ${f.matchAll && (f.conditions || []).length > 1 ? "warn" : ""}">${escapeHtml(f.matchAll ? "AND" : "OR")}</span>
                      <div class="oma-muted oma-mono" style="font-size:10.5px; margin-top:2px;">${escapeHtml(condPreview + extra)}</div>
                    </td>
                    <td class="oma-mono" style="font-size:11px;">${escapeHtml(target || "—")}</td>
                    <td style="text-align:right; white-space:nowrap;">
                      <button class="mini-btn" data-run-native-idx="${idx}" title="${escapeHtml(t("diag_run_inbox"))}">▶</button>
                    </td>
                  </tr>`;
                }).join("")}
              </tbody>
            </table>
          `}
        </div>
      ` : ""}
    `;

    const editing = editingRuleId ? rules.find((r) => r.id === editingRuleId) : null;
    if (editing) {
      if (editing.accountId) document.getElementById("rule-account").value = editing.accountId;
      document.getElementById("rule-space").value = editing.space || "";
      document.getElementById("rule-folder").value = editing.folderPath || "";
      document.getElementById("rule-domains").value = (editing.domains || []).join(", ");
      document.getElementById("rule-col-waiting").value = editing.columnFolders?.waiting || "";
      document.getElementById("rule-col-done").value = editing.columnFolders?.done || "";
    }

    document.getElementById("btn-toggle-import")?.addEventListener("click", async () => {
      showImportPanel = !showImportPanel;
      if (showImportPanel) await refreshNativeFilters();
      renderMain();
    });
    document.getElementById("btn-cancel-import")?.addEventListener("click", () => {
      showImportPanel = false;
      renderMain();
    });
    document.getElementById("btn-confirm-import")?.addEventListener("click", async () => {
      const selected = [];
      container.querySelectorAll("[data-import-idx]").forEach((cb) => {
        if (!cb.checked) return;
        const idx = Number(cb.dataset.importIdx);
        const cand = importCandidates[idx];
        if (!cand) return;
        const spaceInput = container.querySelector(`[data-import-space="${idx}"]`);
        selected.push({ ...cand, space: spaceInput?.value.trim() || cand.space });
      });
      if (selected.length === 0) return;
      if (inExtension && snapshot.connected) {
        try {
          const res = await api.runtime.sendMessage({ type: "omathunder:importSpaceRules", candidates: selected });
          if (res?.config) snapshot.config = res.config;
          showImportPanel = false;
          await refreshNativeFilters();
          renderCounts();
          renderSpaceFilterBar();
          renderMain();
          showMessage(t("native_import_done", { count: res?.importedCount ?? selected.length }), "ok");
        } catch (err) {
          showMessage(errText(err), "err");
        }
      }
    });
    document.getElementById("btn-open-tb-filters")?.addEventListener("click", async () => {
      if (!inExtension) return;
      const accId = document.getElementById("rule-account")?.value || defaultFilterAccId;
      await api.runtime.sendMessage({ type: "omathunder:openNativeFilters", accountId: accId }).catch(() => {});
    });
    document.getElementById("btn-run-all-native")?.addEventListener("click", async () => {
      if (!inExtension) return;
      const accId = document.getElementById("rule-account")?.value || defaultFilterAccId;
      if (!accId) return;
      try {
        const res = await api.runtime.sendMessage({ type: "omathunder:runNativeFilters", accountId: accId });
        showMessage(t("diag_run_done", { count: res?.executed ?? 0 }), "ok");
      } catch (err) {
        showMessage(errText(err), "err");
      }
    });

    container.querySelectorAll("[data-edit-rule]").forEach((btn) => {
      btn.addEventListener("click", () => {
        editingRuleId = editingRuleId === btn.dataset.editRule ? null : btn.dataset.editRule;
        renderMain();
      });
    });

    document.getElementById("btn-add-rule").addEventListener("click", async () => {
      const space = document.getElementById("rule-space").value.trim();
      const folderPath = document.getElementById("rule-folder").value.trim();
      const domains = document.getElementById("rule-domains").value.split(",").map((s) => s.trim().toLowerCase()).filter(Boolean);
      const waitSub = document.getElementById("rule-col-waiting").value.trim();
      const doneSub = document.getElementById("rule-col-done").value.trim();
      const autoCreate = document.getElementById("chk-autocreate-folder").checked;
      const syncNative = Boolean(document.getElementById("chk-sync-native")?.checked);
      const accountId = document.getElementById("rule-account").value;
      if (!accountId || !space || !folderPath || domains.length === 0) return;

      const columnFolders = { ...(editing?.columnFolders || {}) };
      if (waitSub) columnFolders.waiting = waitSub;
      else delete columnFolders.waiting;
      if (doneSub) columnFolders.done = doneSub;
      else delete columnFolders.done;

      const folderObj = (snapshot.folders || []).find((f) => f.accountId === accountId && f.path.toLowerCase() === folderPath.toLowerCase());
      let targetRule;
      if (editing) {
        const { columnFolders: _old, ...kept } = editing;
        targetRule = {
          ...kept, space, accountId, folderPath, folderId: folderObj?.id || "", domains,
          ...(Object.keys(columnFolders).length ? { columnFolders } : {}),
        };
        editingRuleId = null;
        await saveRules((snapshot.config?.rules || []).map((r) => (r.id === targetRule.id ? targetRule : r)), autoCreate);
      } else {
        targetRule = {
          id: `rule-${Date.now()}`,
          space,
          accountId,
          folderPath,
          folderId: folderObj?.id || "",
          domains,
          autoMove: false,
          includeInScan: true,
          ...(Object.keys(columnFolders).length ? { columnFolders } : {}),
        };
        await saveRules([...(snapshot.config?.rules || []), targetRule], autoCreate);
      }

      if (syncNative && inExtension && snapshot.connected && nativeSupported && window.OmaCore?.buildNativeFilterSpecFromRule) {
        try {
          const spec = window.OmaCore.buildNativeFilterSpecFromRule(targetRule);
          const applied = await api.runtime.sendMessage({
            type: "omathunder:applyNativeFilter",
            spec,
            linkRuleId: targetRule.id,
          });
          await refreshSnapshot();
          showMessage(t("popup_tb_filter_applied", { name: applied?.filter?.name || spec.name }), "ok");
        } catch (err) {
          showMessage(errText(err), "warn");
        }
      }
    });

    container.querySelectorAll("[data-create-rule-filter]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const rule = rules.find((r) => r.id === btn.dataset.createRuleFilter);
        if (!rule || !inExtension || !window.OmaCore?.buildNativeFilterSpecFromRule) return;
        try {
          const spec = window.OmaCore.buildNativeFilterSpecFromRule(rule);
          const applied = await api.runtime.sendMessage({
            type: "omathunder:applyNativeFilter",
            spec,
            linkRuleId: rule.id,
          });
          await refreshSnapshot();
          showMessage(t("popup_tb_filter_applied", { name: applied?.filter?.name || spec.name }), "ok");
        } catch (err) {
          showMessage(errText(err), "err");
        }
      });
    });

    container.querySelectorAll("[data-enable-rule-filter]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const rule = rules.find((r) => r.id === btn.dataset.enableRuleFilter);
        if (!rule || !inExtension) return;
        const linked = findLinkedNativeFilters(rule);
        if (!linked[0]) return;
        try {
          await api.runtime.sendMessage({
            type: "omathunder:applyNativeFilter",
            spec: { accountId: linked[0].accountId, mode: "toggle", existingName: linked[0].name, enabled: true },
          });
          await refreshNativeFilters();
          renderMain();
        } catch (err) {
          showMessage(errText(err), "err");
        }
      });
    });

    container.querySelectorAll("[data-run-rule-filter]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const rule = rules.find((r) => r.id === btn.dataset.runRuleFilter);
        if (!rule || !inExtension) return;
        const linked = findLinkedNativeFilters(rule);
        if (linked.length === 0) return;
        try {
          const r = await api.runtime.sendMessage({
            type: "omathunder:runNativeFilters",
            accountId: linked[0].accountId,
            filterNames: linked.map((f) => f.name),
          });
          showMessage(t("diag_run_done", { count: r?.executed ?? linked.length }), "ok");
        } catch (err) {
          showMessage(errText(err), "err");
        }
      });
    });

    container.querySelectorAll("[data-fix-issue-idx]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const iss = nativeIssues[Number(btn.dataset.fixIssueIdx)];
        if (!iss || !inExtension) return;
        try {
          await api.runtime.sendMessage({
            type: "omathunder:applyNativeFilter",
            spec: iss.quickFix === "enable"
              ? { accountId: iss.accountId, mode: "toggle", existingName: iss.filterName, enabled: true }
              : { accountId: iss.accountId, mode: "fix_match_all", existingName: iss.filterName, matchAll: false, enable: true },
          });
          await refreshNativeFilters();
          renderMain();
        } catch (err) {
          showMessage(errText(err), "err");
        }
      });
    });

    container.querySelectorAll("[data-toggle-native-idx]").forEach((cb) => {
      cb.addEventListener("change", async () => {
        const f = nativeFilters[Number(cb.dataset.toggleNativeIdx)];
        if (!f || !inExtension) return;
        try {
          await api.runtime.sendMessage({
            type: "omathunder:applyNativeFilter",
            spec: { accountId: f.accountId, mode: "toggle", existingName: f.name, enabled: cb.checked },
          });
          await refreshNativeFilters();
          renderMain();
        } catch (err) {
          showMessage(errText(err), "err");
        }
      });
    });

    container.querySelectorAll("[data-run-native-idx]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const f = nativeFilters[Number(btn.dataset.runNativeIdx)];
        if (!f || !inExtension) return;
        try {
          const r = await api.runtime.sendMessage({
            type: "omathunder:runNativeFilters",
            accountId: f.accountId,
            filterNames: [f.name],
          });
          showMessage(t("diag_run_done", { count: r?.executed ?? 1 }), "ok");
        } catch (err) {
          showMessage(errText(err), "err");
        }
      });
    });

    container.querySelectorAll("[data-toggle-move]").forEach((cb) => {
      cb.addEventListener("change", async () => {
        const nextRules = (snapshot.config?.rules || []).map((r) => r.id === cb.dataset.toggleMove ? { ...r, autoMove: cb.checked } : r);
        await saveRules(nextRules);
      });
    });
    container.querySelectorAll("[data-toggle-scan]").forEach((cb) => {
      cb.addEventListener("change", async () => {
        const nextRules = (snapshot.config?.rules || []).map((r) => r.id === cb.dataset.toggleScan ? { ...r, includeInScan: cb.checked } : r);
        await saveRules(nextRules);
      });
    });
    container.querySelectorAll("[data-del-rule]").forEach((btn) => {
      btn.addEventListener("click", () => openDeleteModal(btn.dataset.delRule));
    });
    return;
  }

  if (currentView === "archive") {
    const accounts = triageAccounts();
    if (!archiveForm.accountId && accounts[0]?.id) archiveForm.accountId = accounts[0].id;
    if (!archiveForm.archiveDir && snapshot.config?.mailbox?.archiveDir) {
      archiveForm.archiveDir = snapshot.config.mailbox.archiveDir;
    }
    const accFolders = (snapshot.folders || []).filter((f) => f.accountId === archiveForm.accountId);
    const folderOpts = accFolders.map((f) => `<option value="${escapeHtml(f.path)}">${escapeHtml(f.path)}</option>`).join("");
    const accountOpts = accounts.map((a) => `<option value="${escapeHtml(a.id)}" ${a.id === archiveForm.accountId ? "selected" : ""}>${escapeHtml(a.name || a.id)}</option>`).join("");
    const readOnlyNow = isReadOnly();
    const mutatingMode = archiveForm.mode === "export_and_local" || archiveForm.mode === "export_and_delete";
    const blockedByReadOnly = mutatingMode && readOnlyNow;

    container.innerHTML = `
      <div class="oma-card" style="margin-bottom:14px;">
        <div style="display:flex; justify-content:space-between; align-items:center; gap:10px; flex-wrap:wrap;">
          <div>
            <div style="font-weight:700; font-size:13px;">${escapeHtml(t("archive_title"))}</div>
            <div class="oma-muted" style="font-size:11.5px; margin-top:2px;">${escapeHtml(t("archive_desc"))}</div>
          </div>
          <div style="display:flex; gap:8px; align-items:center; flex-wrap:wrap;">
            <span class="oma-badge ok">${escapeHtml(t("archive_stats_badge", { files: archiveStatsData.fileCount ?? 0, size: formatBytes(archiveStatsData.totalBytes || 0) }))}</span>
            <button class="oma-btn ghost" id="btn-refresh-archive-stats">${escapeHtml(t("archive_btn_refresh_stats"))}</button>
          </div>
        </div>
        <div style="margin-top:10px; display:grid; grid-template-columns:1fr auto; gap:8px; align-items:end;">
          <div>
            <label class="oma-muted" style="font-size:11px;">${escapeHtml(t("archive_lbl_dir"))}</label>
            <input class="oma-input oma-mono" id="arch-dir" value="${escapeHtml(archiveForm.archiveDir || archiveStatsData.archiveDir || "")}" placeholder="~/OmaThunder-Archiv" />
          </div>
          <button class="oma-btn" id="btn-save-arch-dir">${escapeHtml(t("archive_btn_save_dir"))}</button>
        </div>
      </div>

      ${accounts.length === 0 ? `<div class="oma-card"><span class="oma-badge warn">${escapeHtml(t("rules_no_account"))}</span></div>` : `
        <div class="oma-card" style="margin-bottom:14px;">
          <div style="font-weight:700; margin-bottom:10px;">${escapeHtml(t("archive_filter_title"))}</div>
          <div style="display:grid; grid-template-columns:repeat(auto-fit, minmax(180px, 1fr)); gap:10px;">
            <div>
              <label class="oma-muted" style="font-size:11px;">${escapeHtml(t("rules_lbl_account"))}</label>
              <select class="oma-select" id="arch-account">${accountOpts}</select>
            </div>
            <div>
              <label class="oma-muted" style="font-size:11px;">${escapeHtml(t("archive_lbl_scope"))}</label>
              <select class="oma-select" id="arch-scope">
                <option value="inbox" ${archiveForm.scope === "inbox" ? "selected" : ""}>${escapeHtml(t("archive_scope_inbox"))}</option>
                <option value="folder" ${archiveForm.scope === "folder" ? "selected" : ""}>${escapeHtml(t("archive_scope_folder"))}</option>
                <option value="all" ${archiveForm.scope === "all" ? "selected" : ""}>${escapeHtml(t("archive_scope_all"))}</option>
              </select>
            </div>
            <div>
              <label class="oma-muted" style="font-size:11px;">${escapeHtml(t("archive_lbl_days"))}</label>
              <input class="oma-input" id="arch-days" type="number" min="0" max="36500" value="${escapeHtml(String(archiveForm.olderThanDays))}" />
            </div>
            <div>
              <label class="oma-muted" style="font-size:11px;">${escapeHtml(t("archive_lbl_max"))}</label>
              <input class="oma-input" id="arch-max" type="number" min="1" max="1000" value="${escapeHtml(String(archiveForm.maxMessages))}" />
            </div>
          </div>

          ${archiveForm.scope === "folder" ? `
            <div style="display:grid; grid-template-columns:1fr auto; gap:10px; align-items:end; margin-top:10px;">
              <div>
                <label class="oma-muted" style="font-size:11px;">${escapeHtml(t("archive_lbl_folder"))}</label>
                <input class="oma-input" id="arch-folder" list="arch-folder-list" value="${escapeHtml(archiveForm.folderPath)}" placeholder="z. B. Rechnungen/Strom" />
                <datalist id="arch-folder-list">${folderOpts}</datalist>
              </div>
              <label style="display:flex; align-items:center; gap:6px; font-size:11.5px; cursor:pointer; padding-bottom:6px;">
                <input type="checkbox" id="arch-subfolders" ${archiveForm.includeSubfolders ? "checked" : ""} />
                <span>${escapeHtml(t("archive_lbl_subfolders"))}</span>
              </label>
            </div>
          ` : ""}

          <div style="display:flex; gap:18px; flex-wrap:wrap; margin-top:10px;">
            <label style="display:flex; align-items:center; gap:6px; font-size:11.5px; cursor:pointer;">
              <input type="checkbox" id="arch-only-read" ${archiveForm.onlyRead ? "checked" : ""} />
              <span>${escapeHtml(t("archive_lbl_only_read"))}</span>
            </label>
            <label style="display:flex; align-items:center; gap:6px; font-size:11.5px; cursor:pointer;">
              <input type="checkbox" id="arch-exclude-starred" ${archiveForm.excludeStarred ? "checked" : ""} />
              <span>${escapeHtml(t("archive_lbl_exclude_starred"))}</span>
            </label>
          </div>

          <div style="margin-top:12px; padding-top:12px; border-top:1px solid var(--oma-border);">
            <div style="font-weight:600; font-size:12px; margin-bottom:6px;">${escapeHtml(t("archive_mode_title"))}</div>
            <div style="display:grid; grid-template-columns:repeat(auto-fit, minmax(220px, 1fr)); gap:8px;">
              ${["simulate", "export_only", "export_and_local", "export_and_delete"].map((m) => `
                <label style="display:flex; align-items:flex-start; gap:8px; padding:8px 10px; border:1px solid ${archiveForm.mode === m ? "var(--oma-accent)" : "var(--oma-border)"}; border-radius:7px; background:var(--oma-bg-darker); cursor:pointer;">
                  <input type="radio" name="arch-mode" value="${m}" ${archiveForm.mode === m ? "checked" : ""} style="margin-top:3px;" />
                  <div>
                    <div style="font-weight:600; font-size:12px;">${escapeHtml(t(`archive_mode_${m}`))}</div>
                    <div class="oma-muted" style="font-size:11px; margin-top:2px;">${escapeHtml(t(`archive_mode_${m}_desc`))}</div>
                  </div>
                </label>
              `).join("")}
            </div>
            ${archiveForm.mode === "export_and_delete" ? `
              <label style="display:flex; align-items:center; gap:6px; font-size:11.5px; cursor:pointer; margin-top:8px; color:var(--oma-yellow);">
                <input type="checkbox" id="arch-skip-trash" ${archiveForm.skipTrash ? "checked" : ""} />
                <span>${escapeHtml(t("archive_lbl_skip_trash"))}</span>
              </label>
            ` : ""}
            ${blockedByReadOnly ? `<div class="oma-badge warn" style="margin-top:8px;">${escapeHtml(t("archive_ro_blocked"))}</div>` : ""}
          </div>

          <div style="display:flex; justify-content:flex-end; gap:8px; margin-top:12px; flex-wrap:wrap;">
            <button class="oma-btn" id="btn-arch-preview" ${archiveBusy ? "disabled" : ""}>${escapeHtml(t("archive_btn_preview"))}</button>
            <button class="oma-btn primary" id="btn-arch-run" ${archiveBusy || blockedByReadOnly ? "disabled" : ""}>${escapeHtml(archiveBusy ? t("archive_btn_running") : t(`archive_run_${archiveForm.mode}`))}</button>
          </div>
        </div>
      `}

      ${archiveReportData ? `
        <div class="oma-card" style="margin-bottom:14px;">
          <div style="display:flex; justify-content:space-between; align-items:center; gap:8px;">
            <strong>${escapeHtml(t(archiveReportData.dryRun ? "archive_report_sim_title" : "archive_report_done_title"))}</strong>
            <span class="oma-badge ${archiveReportData.failed?.length ? "warn" : "ok"}">${escapeHtml(t("archive_report_summary", {
              exported: archiveReportData.exported || 0,
              dups: archiveReportData.skippedDuplicates || 0,
              moved: archiveReportData.moved || 0,
              deleted: archiveReportData.deleted || 0,
              size: formatBytes(archiveReportData.dryRun ? archiveReportData.bytesEstimated : archiveReportData.bytesExported),
            }))}</span>
          </div>
          ${archiveReportData.failed?.length ? `
            <div class="oma-badge err" style="margin-top:8px;">${escapeHtml(t("archive_report_failed", { count: archiveReportData.failed.length }))}</div>
            <ul class="report-list">${archiveReportData.failed.slice(0, 20).map((f) => `<li>${escapeHtml(f.subject || "")}: ${escapeHtml(errText(new Error(f.error)))}</li>`).join("")}</ul>
          ` : ""}
          ${archiveReportData.items?.length ? `
            <table class="rules-table" style="margin-top:8px;">
              <thead><tr><th>${escapeHtml(t("archive_th_date"))}</th><th>${escapeHtml(t("archive_th_subject"))}</th><th>${escapeHtml(t("archive_th_size"))}</th><th>${escapeHtml(t("archive_th_target"))}</th></tr></thead>
              <tbody>
                ${archiveReportData.items.slice(0, 50).map((it) => `
                  <tr>
                    <td class="oma-mono" style="white-space:nowrap; font-size:11px;">${escapeHtml(String(it.date || "").slice(0, 10))}</td>
                    <td>${escapeHtml(it.subject)}<div class="oma-muted" style="font-size:10.5px;">${escapeHtml(it.from)}</div></td>
                    <td class="oma-mono" style="white-space:nowrap; font-size:11px;">${escapeHtml(formatBytes(it.size || 0))}</td>
                    <td class="oma-mono" style="font-size:10.5px;">${escapeHtml(it.targetPath)}${it.skippedDuplicate ? ` <span class="oma-badge">${escapeHtml(t("archive_dup_badge"))}</span>` : ""}</td>
                  </tr>
                `).join("")}
              </tbody>
            </table>
          ` : ""}
        </div>
      ` : ""}

      ${archivePreviewData ? `
        <div class="oma-card">
          <div style="display:flex; justify-content:space-between; align-items:center; gap:8px; flex-wrap:wrap;">
            <strong>${escapeHtml(t("archive_preview_title", { count: archivePreviewData.totalCount, matched: archivePreviewData.totalMatched, size: formatBytes(archivePreviewData.totalBytesAll || archivePreviewData.totalBytes || 0) }))}</strong>
            <div style="display:flex; gap:6px; align-items:center;">
              ${archivePreviewData.hasLocalAccount ? `<span class="oma-badge ok">${escapeHtml(t("archive_local_ready", { name: archivePreviewData.localAccountName }))}</span>` : `<span class="oma-badge warn">${escapeHtml(t("archive_local_missing"))}</span>`}
            </div>
          </div>
          ${archivePreviewData.truncated ? `<div class="oma-muted" style="font-size:11px; margin-top:4px;">${escapeHtml(t("archive_preview_truncated", { count: archivePreviewData.totalCount, matched: archivePreviewData.totalMatched }))}</div>` : ""}
          ${archivePreviewData.candidates?.length === 0 ? `<div class="oma-muted" style="font-size:11.5px; margin-top:8px;">${escapeHtml(t("archive_preview_empty"))}</div>` : `
            <table class="rules-table" style="margin-top:8px;">
              <thead><tr><th>${escapeHtml(t("archive_th_date"))}</th><th>${escapeHtml(t("archive_th_subject"))}</th><th>${escapeHtml(t("archive_th_folder"))}</th><th>${escapeHtml(t("archive_th_size"))}</th><th>${escapeHtml(t("archive_th_target"))}</th></tr></thead>
              <tbody>
                ${(archivePreviewData.candidates || []).slice(0, 60).map((c) => `
                  <tr>
                    <td class="oma-mono" style="white-space:nowrap; font-size:11px;">${escapeHtml(String(c.date || "").slice(0, 10))}</td>
                    <td>${escapeHtml(c.subject)}<div class="oma-muted" style="font-size:10.5px;">${escapeHtml(c.from)}</div></td>
                    <td class="oma-mono" style="font-size:11px;">${escapeHtml(c.folderPath)}</td>
                    <td class="oma-mono" style="white-space:nowrap; font-size:11px;">${escapeHtml(formatBytes(c.size || 0))}</td>
                    <td class="oma-mono" style="font-size:10.5px;">${escapeHtml(c.archiveRelPath)}</td>
                  </tr>
                `).join("")}
              </tbody>
            </table>
          `}
        </div>
      ` : ""}
    `;

    const syncArchForm = () => {
      const accEl = document.getElementById("arch-account");
      if (accEl) archiveForm.accountId = accEl.value;
      const scopeEl = document.getElementById("arch-scope");
      if (scopeEl) archiveForm.scope = scopeEl.value;
      const folderEl = document.getElementById("arch-folder");
      if (folderEl) archiveForm.folderPath = folderEl.value.trim();
      const subEl = document.getElementById("arch-subfolders");
      if (subEl) archiveForm.includeSubfolders = subEl.checked;
      const daysEl = document.getElementById("arch-days");
      if (daysEl) archiveForm.olderThanDays = Math.max(0, Number(daysEl.value || 0));
      const maxEl = document.getElementById("arch-max");
      if (maxEl) archiveForm.maxMessages = Math.min(1000, Math.max(1, Number(maxEl.value || 200)));
      const readEl = document.getElementById("arch-only-read");
      if (readEl) archiveForm.onlyRead = readEl.checked;
      const starEl = document.getElementById("arch-exclude-starred");
      if (starEl) archiveForm.excludeStarred = starEl.checked;
      const dirEl = document.getElementById("arch-dir");
      if (dirEl) archiveForm.archiveDir = dirEl.value.trim();
      const skipTrashEl = document.getElementById("arch-skip-trash");
      if (skipTrashEl) archiveForm.skipTrash = skipTrashEl.checked;
    };

    document.getElementById("arch-account")?.addEventListener("change", () => { syncArchForm(); archivePreviewData = null; renderMain(); });
    document.getElementById("arch-scope")?.addEventListener("change", () => { syncArchForm(); archivePreviewData = null; renderMain(); });
    container.querySelectorAll('input[name="arch-mode"]').forEach((rb) => {
      rb.addEventListener("change", () => { syncArchForm(); archiveForm.mode = rb.value; renderMain(); });
    });

    document.getElementById("btn-refresh-archive-stats")?.addEventListener("click", async () => {
      syncArchForm();
      await refreshArchiveStats();
      renderCounts();
      renderMain();
    });

    document.getElementById("btn-save-arch-dir")?.addEventListener("click", async () => {
      syncArchForm();
      if (inExtension && snapshot.connected) {
        try {
          const res = await api.runtime.sendMessage({ type: "omathunder:saveConfig", config: { mailbox: { archiveDir: archiveForm.archiveDir } } });
          if (res?.config) snapshot.config = res.config;
          await refreshArchiveStats();
          renderCounts();
          renderMain();
          showMessage(t("opts_saved"), "ok");
        } catch (err) {
          showMessage(errText(err), "err");
        }
      }
    });

    document.getElementById("btn-arch-preview")?.addEventListener("click", async () => {
      syncArchForm();
      if (!inExtension || !snapshot.connected) return;
      archiveBusy = true;
      renderMain();
      try {
        archivePreviewData = await api.runtime.sendMessage({ type: "omathunder:previewCleanup", ...archiveForm });
        archiveReportData = null;
      } catch (err) {
        showMessage(errText(err), "err");
      } finally {
        archiveBusy = false;
        renderMain();
      }
    });

    document.getElementById("btn-arch-run")?.addEventListener("click", async () => {
      syncArchForm();
      if (!inExtension || !snapshot.connected) return;
      if (archiveForm.mode === "export_and_delete") {
        const msgKey = archiveForm.skipTrash ? "archive_confirm_delete_perm" : "archive_confirm_delete_trash";
        if (!window.confirm(t(msgKey))) return;
      }
      archiveBusy = true;
      renderMain();
      try {
        archiveReportData = await api.runtime.sendMessage({ type: "omathunder:runCleanup", ...archiveForm });
        await refreshArchiveStats();
        renderCounts();
      } catch (err) {
        showMessage(errText(err), "err");
      } finally {
        archiveBusy = false;
        renderMain();
      }
    });
    return;
  }

  if (currentView === "themes") {
    const activeName = snapshot.theme?.palette?.name;
    container.innerHTML = `<div class="theme-grid">${(snapshot.themes || []).map((th) => `
      <div class="theme-swatch ${th.name === activeName ? "active" : ""}" data-theme="${escapeHtml(th.name)}" style="background:${th.background}; color:${th.foreground}; border-color:${th.border || th.selection};">
        <div style="display:flex; justify-content:space-between; align-items:center;">
          <strong>${escapeHtml(th.label || th.name)}</strong>
          <span class="oma-mono" style="font-size:10px; opacity:0.75;">${th.isLight ? "LIGHT" : "DARK"}</span>
        </div>
        <div class="swatch-dots">
          <span class="swatch-dot" style="background:${th.background}"></span>
          <span class="swatch-dot" style="background:${th.lighterBackground || th.selection}"></span>
          <span class="swatch-dot" style="background:${th.accent}"></span>
          <span class="swatch-dot" style="background:${th.green || "#9ece6a"}"></span>
          <span class="swatch-dot" style="background:${th.red || "#f7768e"}"></span>
        </div>
      </div>
    `).join("")}</div>`;
    container.querySelectorAll("[data-theme]").forEach((el) => {
      el.addEventListener("click", () => selectTheme(el.dataset.theme));
    });
  }
}

function showMessage(text, kind = "ok") {
  const el = document.getElementById("scan-report");
  el.style.display = "";
  el.innerHTML = `<div class="oma-card" style="display:flex; justify-content:space-between; gap:10px; align-items:center;">
    <span class="oma-badge ${kind}">${escapeHtml(text)}</span>
    <button class="mini-btn" data-close-report>${escapeHtml(t("report_close"))}</button>
  </div>`;
  el.querySelector("[data-close-report]").addEventListener("click", () => { el.style.display = "none"; });
}

function renderScanReport() {
  const el = document.getElementById("scan-report");
  const rep = lastReport;
  if (!rep) {
    el.style.display = "none";
    return;
  }
  const planned = rep.planned || [];
  const title = rep.readOnly
    ? t("report_title_dry", { count: planned.length })
    : t("report_title_live", { moved: (rep.moved || []).length, failed: (rep.failed || []).length });
  const rows = rep.readOnly ? planned : [...(rep.moved || []), ...(rep.failed || [])];
  const list = rows.length
    ? `<ul class="report-list">${rows.map((m) => `<li>${escapeHtml(m.subject)} <span class="oma-muted">(${escapeHtml(m.from)})</span> → <span class="oma-mono">${escapeHtml(m.targetFolder)}</span>${m.error ? ` <span class="oma-badge err">${escapeHtml(errText(new Error(m.error)))}</span>` : ""}</li>`).join("")}</ul>`
    : `<div class="oma-muted" style="font-size:11.5px; margin-top:4px;">${escapeHtml(t("report_none"))}</div>`;
  el.style.display = "";
  el.innerHTML = `<div class="oma-card">
    <div style="display:flex; justify-content:space-between; gap:10px; align-items:center;">
      <strong>${escapeHtml(title)}</strong>
      <button class="mini-btn" data-close-report>${escapeHtml(t("report_close"))}</button>
    </div>
    <div class="oma-muted" style="font-size:11px;">${escapeHtml(t("report_scanned", { messages: rep.scannedMessages ?? 0, folders: rep.scannedFolders ?? 0 }))}</div>
    <div class="oma-muted" style="font-size:11px;">${escapeHtml(rep.aiEnabled === false ? t("report_ai_off") : t("report_ai_calls", { calls: rep.aiCalls ?? 0 }))}</div>
    ${rep.aiSkipped ? `<div style="margin-top:6px;"><span class="oma-badge warn">${escapeHtml(t("report_ai_skipped", { count: rep.aiSkipped }))}</span></div>` : ""}
    ${rep.classifyErrors ? `<div style="margin-top:6px;"><span class="oma-badge warn">${escapeHtml(t("report_classify_error", { count: rep.classifyErrors }))}</span>
      <div class="oma-muted" style="font-size:11px; margin-top:3px;">${escapeHtml(errText(new Error(rep.classifyError)))}</div></div>` : ""}
    ${list}
    ${!rep.readOnly && (rep.moved || []).length > 0 && rep.scanId ? `<div style="margin-top:8px;"><button class="oma-btn" data-undo-scan>${escapeHtml(t("report_undo"))}</button></div>` : ""}
  </div>`;
  el.querySelector("[data-close-report]").addEventListener("click", () => {
    lastReport = null;
    el.style.display = "none";
  });
  el.querySelector("[data-undo-scan]")?.addEventListener("click", async () => {
    try {
      const res = await api.runtime.sendMessage({ type: "omathunder:undoScan", scanId: rep.scanId });
      lastReport = null;
      const r = res?.revertReport || { reverted: 0, skipped: [], failed: [] };
      await refreshSnapshot();
      showMessage(t("revert_report", { reverted: r.reverted, skipped: r.skipped.length, failed: r.failed.length }), r.failed.length ? "warn" : "ok");
    } catch (err) {
      showMessage(errText(err), "err");
    }
  });
}

async function moveKanbanCards(messageIds, column, { silentRender = false } = {}) {
  const uniqueIds = [...new Set((messageIds || []).map(String).filter(Boolean))];
  const idsToMove = uniqueIds.filter((id) => {
    const cls = snapshot.state?.classifications?.[id];
    return cls && (cls.kanbanColumn || "incoming") !== column;
  });
  if (idsToMove.length === 0) return;

  if (inExtension && snapshot.connected) {
    try {
      const msg = idsToMove.length === 1
        ? { type: "omathunder:moveKanban", messageId: idsToMove[0], column }
        : { type: "omathunder:moveKanbanBatch", messageIds: idsToMove, column };
      const res = await api.runtime.sendMessage(msg);
      if (res?.state) snapshot.state = res.state;
      const skipped = res?.effects?.skipped;
      if (skipped && skipped !== "read_only") showMessage(errText(new Error(skipped)), "warn");
    } catch (err) {
      showMessage(errText(err), "err");
    }
  } else {
    for (const id of idsToMove) {
      if (snapshot.state?.classifications?.[id]) {
        snapshot.state.classifications[id].kanbanColumn = column;
      }
    }
  }
  if (!silentRender) {
    renderCounts();
    renderMain();
  }
}

async function moveKanbanCard(messageId, column) {
  return moveKanbanCards([messageId], column);
}

async function saveRules(nextRules, autoCreateFolders = false) {
  snapshot.config.rules = nextRules;
  if (inExtension && snapshot.connected) {
    try {
      const res = await api.runtime.sendMessage({
        type: "omathunder:saveRules",
        rules: nextRules,
        autoCreateFolders,
      });
      if (res?.config) snapshot.config = res.config;
      if (res?.folderNotes?.length) {
        showMessage(t("rules_folder_note", { reason: errText(new Error(res.folderNotes[0])) }), "warn");
      }
    } catch (err) {
      showMessage(errText(err), "err");
      await refreshSnapshot();
      return;
    }
  }
  renderCounts();
  renderMain();
}

function openDeleteModal(ruleId) {
  const rule = (snapshot.config?.rules || []).find((r) => r.id === ruleId);
  if (!rule) return;
  pendingDeleteRule = rule;
  document.getElementById("delete-modal-text").textContent = t("del_modal_body", {
    space: rule.space,
    folder: rule.folderPath,
    domains: (rule.domains || []).join(", "),
  });
  const revertBox = document.getElementById("chk-revert-incoming");
  revertBox.checked = false;
  revertBox.disabled = isReadOnly();
  document.getElementById("revert-ro-note").style.display = isReadOnly() ? "" : "none";
  document.getElementById("delete-modal").classList.add("open");
}

async function renderDisplayedMailInfo() {
  const title = document.getElementById("selected-title");
  const meta = document.getElementById("selected-meta");
  title.textContent = t("ai_current_mail");
  meta.textContent = "";
  if (!inExtension) return;
  const info = await api.runtime.sendMessage({ type: "omathunder:getDisplayedMessage" }).catch(() => null);
  if (selectedMessageId) return; // a card was selected meanwhile
  meta.textContent = info ? `${info.subject} • ${info.from}` : t("ai_no_displayed");
}

function getBoardProposals({ forSelected = true } = {}) {
  const all = (snapshot.state?.proposals || []).filter((p) => p.status === "pending");
  const classifications = snapshot.state?.classifications || {};
  const kanbanIds = new Set(kanbanAccounts().map((a) => a.id));
  return all.filter((p) => {
    if (forSelected && selectedMessageId) {
      return p.messageId === selectedMessageId;
    }
    const cls = p.messageId ? classifications[p.messageId] : null;
    const accId = p.accountId || cls?.accountId || "";
    if (inExtension && snapshot.connected) {
      if (!accId || !kanbanIds.has(accId)) return false;
      if (selectedAccountId !== "ALL" && accId !== selectedAccountId) return false;
      if (selectedAccountId !== "ALL" && selectedFolder !== "ALL" && cls && !matchesSelectedFolder(cls, selectedAccountId, selectedFolder)) return false;
    }
    const space = p.space || cls?.space || "";
    if (selectedSpace === "UNASSIGNED") return !space;
    if (selectedSpace !== "ALL") return space === selectedSpace;
    return true;
  });
}

function renderRightPane() {
  const assistantOn = !inExtension || (feature("assistant") && !hostOffline());
  const hasKanbanBoard = currentView === "kanban" && !(inExtension && snapshot.connected && kanbanAccounts().length === 0) && !hostOffline();
  const showDrawer = hasKanbanBoard && assistantDrawerOpen;
  document.querySelector(".layout")?.classList.toggle("drawer-open", showDrawer);

  const toggleBtn = document.getElementById("btn-toggle-assistant");
  const toggleBadge = document.getElementById("assistant-toggle-badge");
  const boardCount = getBoardProposals({ forSelected: false }).length;
  if (toggleBtn) {
    toggleBtn.style.display = hasKanbanBoard && assistantOn ? "inline-flex" : "none";
    toggleBtn.classList.toggle("primary", showDrawer);
  }
  if (toggleBadge) {
    toggleBadge.style.display = boardCount > 0 ? "" : "none";
    toggleBadge.textContent = String(boardCount);
  }

  if (!showDrawer) return;

  const offEl = document.getElementById("ai-off");
  offEl.style.display = assistantOn ? "none" : "";
  document.getElementById("ai-mode-tabs").style.display = assistantOn ? "" : "none";
  document.getElementById("ai-pane-body").style.display = assistantOn ? "" : "none";
  if (!assistantOn) {
    offEl.innerHTML = hostOffline() ? "" : noticeHtml(t("feat_off_assistant"));
    bindSettingsButtons(offEl);
    document.getElementById("proposal-count").textContent = "";
    const dismissAllBtn = document.getElementById("btn-dismiss-all-proposals");
    if (dismissAllBtn) dismissAllBtn.style.display = "none";
    return;
  }

  const selected = selectedMessageId ? snapshot.state?.classifications?.[selectedMessageId] : null;
  document.getElementById("btn-use-displayed").style.display = selected && inExtension ? "" : "none";
  if (!selected) renderDisplayedMailInfo();
  else document.getElementById("selected-title").textContent = selected.subject;
  const spaceBadge = document.getElementById("selected-space");
  if (selected?.space) {
    spaceBadge.style.display = "inline-flex";
    spaceBadge.textContent = selected.space;
  } else {
    spaceBadge.style.display = "none";
  }
  if (selected) document.getElementById("selected-meta").textContent = `${selected.from} • ${selected.folder || "INBOX"}`;

  document.getElementById("tone-wrap").style.display = aiMode === "summarize" ? "none" : "block";
  document.getElementById("draft-review-wrap").style.display = aiMode === "review_draft" ? "block" : "none";
  const askBtnLabels = {
    summarize: t("ai_btn_summarize"),
    draft_reply: t("ai_btn_draft"),
    review_draft: t("ai_btn_review"),
  };
  document.getElementById("btn-ask").textContent = askBtnLabels[aiMode] || t("ai_btn_draft");

  const pending = getBoardProposals({ forSelected: true });
  document.getElementById("proposal-count").textContent = t("ai_proposals_count", { count: pending.length });
  const dismissAllBtn = document.getElementById("btn-dismiss-all-proposals");
  if (dismissAllBtn) dismissAllBtn.style.display = pending.length > 0 ? "" : "none";

  const list = document.getElementById("proposals-list");
  if (pending.length === 0) {
    list.innerHTML = `<div class="oma-muted" style="font-size:11.5px;">${escapeHtml(t("ai_no_proposals"))}</div>`;
    return;
  }
  list.innerHTML = pending.map((p) => `
    <div class="proposal-card">
      <div style="display:flex; justify-content:space-between; align-items:center;">
        <span class="oma-badge accent">${escapeHtml(p.kind)}</span>
        <span class="oma-muted oma-mono" style="font-size:10px;">${escapeHtml(t("ai_one_click"))}</span>
      </div>
      <div style="font-weight:600; font-size:12.5px;">${escapeHtml(p.title)}</div>
      <div class="oma-muted" style="font-size:11.5px;">${escapeHtml(p.summary)}</div>
      ${p.body ? `<div class="proposal-preview">${escapeHtml(p.body)}</div>` : ""}
      <div style="display:flex; gap:8px;">
        <button class="oma-btn primary" data-apply="${escapeHtml(p.id)}" style="flex:1; justify-content:center;">${escapeHtml(t(p.kind === "draft_reply" ? "ai_reply" : "ai_apply"))}</button>
        ${p.kind === "draft_reply" ? `<button class="oma-btn" data-apply="${escapeHtml(p.id)}" data-reply-all="1">${escapeHtml(t("ai_reply_all"))}</button>` : ""}
        <button class="oma-btn ghost" data-dismiss="${escapeHtml(p.id)}">${escapeHtml(t("ai_dismiss"))}</button>
      </div>
    </div>
  `).join("");

  list.querySelectorAll("[data-apply]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const prop = pending.find((p) => p.id === btn.dataset.apply);
      if (!prop) return;
      if (inExtension) {
        try {
          const res = await api.runtime.sendMessage({ type: "omathunder:applyProposal", proposal: prop, replyAll: Boolean(btn.dataset.replyAll) });
          if (res?.state) snapshot.state = res.state;
        } catch (err) {
          showMessage(errText(err), "err");
        }
      } else prop.status = "applied";
      renderRightPane();
    });
  });
  list.querySelectorAll("[data-dismiss]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const id = btn.dataset.dismiss;
      if (inExtension) {
        try {
          const res = await api.runtime.sendMessage({ type: "omathunder:dismissProposal", id });
          if (res?.state) snapshot.state = res.state;
        } catch (err) {
          showMessage(errText(err), "err");
        }
      } else {
        const prop = (snapshot.state?.proposals || []).find((p) => p.id === id);
        if (prop) prop.status = "dismissed";
      }
      renderRightPane();
    });
  });
}

async function selectTheme(name) {
  if (inExtension) {
    try {
      const payload = await api.runtime.sendMessage({ type: "omathunder:setTheme", preset: name, mode: "preset" });
      if (payload) { snapshot.theme = payload; applyPaletteLocally(payload); }
    } catch (err) {
      showMessage(errText(err), "err");
    }
  } else {
    const pal = (snapshot.themes || []).find((th) => th.name === name);
    if (pal) { snapshot.theme = { palette: pal, source: "preview" }; applyPaletteLocally(snapshot.theme); }
  }
  renderMain();
}

function updateLangButtons() {
  const cur = window.OMA_I18N ? window.OMA_I18N.getLang() : "de";
  const deBtn = document.getElementById("btn-lang-de");
  const enBtn = document.getElementById("btn-lang-en");
  if (deBtn) {
    deBtn.style.background = cur === "de" ? "var(--oma-accent)" : "transparent";
    deBtn.style.color = cur === "de" ? "var(--oma-accent-fg)" : "var(--oma-fg-muted)";
  }
  if (enBtn) {
    enBtn.style.background = cur === "en" ? "var(--oma-accent)" : "transparent";
    enBtn.style.color = cur === "en" ? "var(--oma-accent-fg)" : "var(--oma-fg-muted)";
  }
}

async function applyLanguage(lang) {
  if (window.OMA_I18N) {
    window.OMA_I18N.setLang(lang);
    window.OMA_I18N.applyDom();
  }
  updateLangButtons();
  renderCounts();
  renderSpaceFilterBar();
  renderMain();
  renderRightPane();
  if (inExtension && (lang === "de" || lang === "en")) {
    snapshot.config.language = lang;
    await api.runtime.sendMessage({ type: "omathunder:saveConfig", config: { language: lang } }).catch(() => {});
  }
}

async function init() {
  document.querySelectorAll(".nav-item[data-view]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      document.querySelectorAll(".nav-item").forEach((b) => b.classList.remove("active"));
      btn.classList.add("active");
      currentView = btn.dataset.view;
      clearCardSelection();
      if (currentView === "kanban") {
        selectedAccountId = "ALL";
        selectedFolder = "ALL";
        selectedSpace = "ALL";
      }
      renderCounts();
      renderSpaceFilterBar();
      renderRightPane();
      if (currentView === "rules" && inExtension && snapshot.connected && nativeFiltersData.filters.length === 0) {
        await refreshNativeFilters();
      }
      if (currentView === "archive" && inExtension && snapshot.connected) {
        await refreshArchiveStats();
        renderCounts();
      }
      renderMain();
    });
  });

  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && currentView === "kanban" && selectedCardIds.size > 0) {
      clearCardSelection();
      renderMain();
    }
  });

  document.getElementById("btn-toggle-assistant")?.addEventListener("click", () => {
    assistantDrawerOpen = !assistantDrawerOpen;
    if (!assistantDrawerOpen) selectedMessageId = null;
    renderMain();
    renderRightPane();
  });

  document.getElementById("btn-close-assistant")?.addEventListener("click", () => {
    assistantDrawerOpen = false;
    selectedMessageId = null;
    renderMain();
    renderRightPane();
  });

  document.getElementById("btn-dismiss-all-proposals")?.addEventListener("click", async () => {
    const visible = getBoardProposals({ forSelected: true });
    if (visible.length === 0) return;
    const ids = visible.map((p) => p.id);
    if (inExtension) {
      try {
        const res = await api.runtime.sendMessage({ type: "omathunder:dismissProposals", ids });
        if (res?.state) snapshot.state = res.state;
      } catch (err) {
        showMessage(errText(err), "err");
      }
    } else {
      const idSet = new Set(ids);
      for (const p of snapshot.state?.proposals || []) {
        if (idSet.has(p.id)) p.status = "dismissed";
      }
    }
    renderRightPane();
  });

  document.querySelectorAll(".mode-tab[data-aimode]").forEach((tab) => {
    tab.addEventListener("click", () => {
      document.querySelectorAll(".mode-tab").forEach((tb) => tb.classList.remove("active"));
      tab.classList.add("active");
      aiMode = tab.dataset.aimode;
      renderRightPane();
    });
  });

  document.querySelectorAll(".tone-pill[data-tone]").forEach((pill) => {
    pill.addEventListener("click", () => {
      document.querySelectorAll(".tone-pill").forEach((p) => p.classList.remove("active"));
      pill.classList.add("active");
      aiTone = pill.dataset.tone;
    });
  });

  document.getElementById("btn-lang-de")?.addEventListener("click", () => applyLanguage("de"));
  document.getElementById("btn-lang-en")?.addEventListener("click", () => applyLanguage("en"));

  document.getElementById("btn-load-compose").addEventListener("click", async () => {
    if (!inExtension) return;
    const draft = await api.runtime.sendMessage({ type: "omathunder:getActiveCompose" }).catch(() => null);
    if (draft?.body) document.getElementById("user-draft").value = draft.body;
  });

  document.getElementById("btn-cancel-del").addEventListener("click", () => {
    pendingDeleteRule = null;
    document.getElementById("delete-modal").classList.remove("open");
  });

  document.getElementById("btn-confirm-del").addEventListener("click", async () => {
    if (!pendingDeleteRule) return;
    const revert = document.getElementById("chk-revert-incoming").checked;
    const ruleId = pendingDeleteRule.id;
    pendingDeleteRule = null;
    document.getElementById("delete-modal").classList.remove("open");

    if (inExtension && snapshot.connected) {
      try {
        const res = await api.runtime.sendMessage({ type: "omathunder:deleteRule", ruleId, revertToIncoming: revert });
        if (res?.config) snapshot.config = res.config;
        const rep = res?.revertReport;
        if (rep) {
          showMessage(t("revert_report", { reverted: rep.reverted, skipped: rep.skipped.length, failed: rep.failed.length }), rep.failed.length ? "warn" : "ok");
        }
      } catch (err) {
        showMessage(errText(err), "err");
      }
    } else {
      snapshot.config.rules = (snapshot.config?.rules || []).filter((r) => r.id !== ruleId);
    }
    renderCounts();
    renderSpaceFilterBar();
    renderMain();
  });

  document.getElementById("btn-options").addEventListener("click", openSettings);
  document.getElementById("btn-use-displayed").addEventListener("click", () => {
    selectedMessageId = null;
    renderMain();
    renderRightPane();
  });

  document.getElementById("btn-triage").addEventListener("click", async () => {
    const btn = document.getElementById("btn-triage");
    btn.disabled = true;
    btn.textContent = t("btn_scanning");
    try {
      if (inExtension && snapshot.connected) {
        const res = await api.runtime.sendMessage({
          type: "omathunder:triageInbox",
          language: window.OMA_I18N ? window.OMA_I18N.getLang() : "",
          accountId: currentView === "kanban" && selectedAccountId !== "ALL" ? selectedAccountId : "",
        });
        lastReport = res?.moveReport || null;
        await refreshSnapshot();
        renderScanReport();
      }
    } catch (err) {
      showMessage(errText(err), "err");
    } finally {
      btn.disabled = false;
      btn.textContent = t("btn_scan");
    }
  });

  document.getElementById("btn-ask").addEventListener("click", async () => {
    const q = document.getElementById("agent-question").value;
    const userDraft = document.getElementById("user-draft").value;
    const ansEl = document.getElementById("agent-answer");
    ansEl.innerHTML = `<div class="oma-card oma-muted">${escapeHtml(t("ai_working", { mode: aiMode }))}</div>`;
    if (inExtension && snapshot.connected) {
      try {
        const res = await api.runtime.sendMessage({
          type: "omathunder:analyzeMessage",
          messageId: selectedMessageId || "",
          mode: aiMode,
          language: window.OMA_I18N ? window.OMA_I18N.getLang() : "",
          tone: aiTone,
          question: q,
          userDraft,
        });
        const issuesHtml = (res.reviewIssues || []).length
          ? `<ul style="margin:6px 0 0; padding-left:18px; color:var(--oma-yellow);">${res.reviewIssues.map((i) => `<li>${escapeHtml(i)}</li>`).join("")}</ul>`
          : "";
        const answerHtml = window.OmaCore?.renderMarkdownHtml
          ? `<div class="oma-md" style="margin-top:6px;">${window.OmaCore.renderMarkdownHtml(res.answer)}</div>`
          : `<p class="oma-pre" style="margin:6px 0 0;">${escapeHtml(res.answer)}</p>`;
        ansEl.innerHTML = `<div class="oma-card"><strong>${escapeHtml(res.provider || "Agent")}</strong>${answerHtml}${issuesHtml}</div>`;
        await refreshSnapshot();
      } catch (err) {
        ansEl.innerHTML = `<div class="oma-card oma-muted">${escapeHtml(errText(err))}</div>`;
      }
    } else {
      ansEl.innerHTML = `<div class="oma-card"><strong>Preview (${escapeHtml(aiMode)})</strong><p style="margin:6px 0 0;">${escapeHtml(t("ai_preview_note"))}</p></div>`;
    }
  });

  const qs = new URLSearchParams(window.location.search);
  if (qs.get("view")) {
    currentView = qs.get("view");
    document.querySelectorAll(".nav-item").forEach((b) => b.classList.toggle("active", b.dataset.view === currentView));
  }
  if (qs.get("theme")) {
    const pal = DEMO_THEMES.find((th) => th.name === qs.get("theme"));
    if (pal) snapshot.theme = { palette: pal, source: "preview" };
  }
  if (!inExtension && qs.get("report") === "dry") {
    // Preview helper: show a dry-run scan report as it appears in read-only mode.
    lastReport = {
      readOnly: true, scanId: "preview", scannedFolders: 3, scannedMessages: 42, aiEnabled: false, aiCalls: 0, aiSkipped: 0,
      planned: [
        { subject: "Neue API-Zugänge für Staging-Test", from: "jonas@beta-systems.com", targetFolder: "Spaces/Beta GmbH" },
        { subject: "Freigabe Q4-Architektur & NDA bis Freitag", from: "laura@acme.de", targetFolder: "Spaces/Acme/Incoming" },
      ],
      moved: [], failed: [],
    };
  }
  if (!inExtension && qs.get("off")) {
    // Preview helper: ?off=triage,assistant,theme shows the UI with those blocks switched off.
    for (const name of qs.get("off").split(",")) snapshot.config.features[name.trim()] = false;
  }
  if (qs.get("aimode")) {
    aiMode = qs.get("aimode");
    document.querySelectorAll(".mode-tab").forEach((tb) => tb.classList.toggle("active", tb.dataset.aimode === aiMode));
  }
  if (window.OMA_I18N) {
    window.OMA_I18N.setLang(qs.get("lang") || "auto");
    window.OMA_I18N.applyDom();
  }
  updateLangButtons();

  if (lastReport) renderScanReport();
  if (inExtension) {
    let lastAutoRefreshAt = Date.now();
    await refreshSnapshot();
    api.runtime.onMessage.addListener((msg) => {
      if (msg?.type === "omathunder:scanFinished") {
        lastReport = msg.report || null;
        lastAutoRefreshAt = Date.now();
        refreshSnapshot().then(renderScanReport);
        return;
      }
      if (msg?.type === "omathunder:stateChanged" && msg.state) {
        snapshot.state = msg.state;
        if (msg.barSummary) snapshot.barSummary = msg.barSummary;
        if (selectedMessageId && !snapshot.state?.classifications?.[selectedMessageId]) {
          selectedMessageId = null;
        }
        renderCounts();
        renderSpaceFilterBar();
        renderMain();
        renderRightPane();
        return;
      }
      if (msg?.type === "omathunder:themeChanged" && msg.payload && feature("theme")) {
        snapshot.theme = msg.payload;
        applyPaletteLocally(msg.payload);
        renderMain();
      }
    });
    document.addEventListener("visibilitychange", () => {
      if (document.visibilityState === "visible" && Date.now() - lastAutoRefreshAt > 2000) {
        lastAutoRefreshAt = Date.now();
        refreshSnapshot();
      }
    });
  } else {
    if (aiMode === "review_draft") {
      document.getElementById("user-draft").value =
        "Hallo Laura,\n\nich schaue mir die Q4-Architektur nächste Woche an. Passt für mich!\n\nGrüße";
      document.getElementById("agent-answer").innerHTML = `<div class="oma-card">
        <strong>Review (claude-cli)</strong>
        <p style="margin:6px 0 4px;">Entwurf geprüft gegen Thread-Kontext &amp; Deadlines:</p>
        <ul style="margin:4px 0 0; padding-left:18px; color:var(--oma-yellow); font-size:11.5px;">
          <li>NDA-Frist (Freitag, 15.11.) wird noch nicht adressiert („nächste Woche“ ist zu spät).</li>
          <li>Konkreter Terminvorschlag für das Q4-Go fehlt.</li>
        </ul>
      </div>`;
    }
    applyPaletteLocally(snapshot.theme);
    renderCounts();
    renderSpaceFilterBar();
    renderMain();
    renderRightPane();
  }
}

async function refreshSnapshot() {
  const res = await api.runtime.sendMessage({ type: "omathunder:getSnapshot" }).catch((err) => ({ connected: false, error: String(err?.message || err) }));
  if (res) {
    snapshot = { ...snapshot, ...res, themes: res.themes || snapshot.themes, state: res.state || snapshot.state, folders: res.folders || snapshot.folders };
    if (res.config?.language && res.config.language !== "auto" && window.OMA_I18N) {
      window.OMA_I18N.setLang(res.config.language);
      window.OMA_I18N.applyDom();
      updateLangButtons();
    }
    if (selectedMessageId && !getAllItems().some((c) => c.messageId === selectedMessageId)) selectedMessageId = null;
    if (res.connected && res.nativeFiltersSupported) {
      await refreshNativeFilters();
    }
    if (res.connected && feature("triage")) {
      await refreshArchiveStats();
    }
  }
  applyPaletteLocally(snapshot.theme);
  renderCounts();
  renderSpaceFilterBar();
  renderMain();
  renderRightPane();
  if (snapshot.connected && snapshot.protocolOk === false) {
    showMessage(t("host_mismatch", { host: snapshot.host?.version, ext: snapshot.extensionVersion }), "err");
  }
}

init();
