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
  { id: "f-acme-in", path: "Kunden/Acme/Incoming" },
  { id: "f-acme-done", path: "Kunden/Acme/Erledigt" },
  { id: "f-beta", path: "Kunden/Beta GmbH" },
  { id: "f-inv", path: "Buchhaltung/Rechnungen" },
];

const DEMO_RULES = [
  { id: "r-acme", customer: "Acme GmbH", folderPath: "Kunden/Acme/Incoming", domains: ["acme.de", "acme-cloud.io"], autoMove: true, includeInScan: true, columnFolders: { done: "Kunden/Acme/Erledigt" } },
  { id: "r-beta", customer: "Beta Systems", folderPath: "Kunden/Beta GmbH", domains: ["beta-systems.com"], autoMove: true, includeInScan: true },
];

const DEMO_STATE = {
  classifications: {
    "demo-1": {
      messageId: "demo-1", customer: "Acme GmbH", folder: "Kunden/Acme/Incoming", kanbanColumn: "action",
      subject: "Freigabe Q4-Architektur & NDA bis Freitag", from: "Laura Weber <laura@acme.de>",
      date: "2026-11-12T14:20:00Z", bucket: "action", urgency: "high",
      summary: "Wartet auf NDA-Freigabe & Terminvorschlag bis 15.11.", deadline: "2026-11-15",
      stage: "stage1-llm", model: "gemini-4-hydrogen", needsAttention: true,
    },
    "demo-2": {
      messageId: "demo-2", customer: "Beta Systems", folder: "Kunden/Beta GmbH", kanbanColumn: "incoming",
      subject: "Neue API-Zugänge für Staging-Test", from: "Jonas Meyer <jonas@beta-systems.com>",
      date: "2026-11-12T17:05:00Z", bucket: "incoming", urgency: "medium",
      summary: "Fragt nach SSH-Key und Testfenster nächste Woche.",
      stage: "stage1-llm", model: "gemini-4-hydrogen", needsAttention: true,
    },
    "demo-3": {
      messageId: "demo-3", customer: "Acme GmbH", folder: "Kunden/Acme/Incoming", kanbanColumn: "invoice",
      subject: "Rechnung #2026-1184 — Cloud-Cluster", from: "billing@acme-cloud.io",
      date: "2026-11-12T07:40:00Z", bucket: "invoice", urgency: "medium",
      summary: "Rechnung erkannt (Stage 0, 0 Tokens) — € 480,00.", amount: "€ 480,00",
      stage: "stage0-rule", model: "deterministic", needsAttention: true,
    },
    "demo-4": {
      messageId: "demo-4", customer: "Beta Systems", folder: "Kunden/Beta GmbH", kanbanColumn: "waiting",
      subject: "Rückfrage Liefertermin Server-Rack", from: "Max <einkauf@beta-systems.com>",
      date: "2026-11-10T16:12:00Z", bucket: "waiting", urgency: "low",
      summary: "Du hast vor 2 Tagen geantwortet — wartet auf Rückmeldung.",
      stage: "stage0-rule", model: "deterministic", needsAttention: false,
    },
    "demo-5": {
      messageId: "demo-5", customer: "", folder: "INBOX", kanbanColumn: "done",
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
let selectedCustomer = "ALL";
// Board card chosen for the assistant. null = use the mail currently open in Thunderbird.
let selectedMessageId = inExtension ? null : "demo-1";
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
    mailbox: { readOnly: true, triageAccountIds: ["demo-account"], autoMoveOnlyUnread: true },
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

function hostOffline() {
  return inExtension && !snapshot.connected;
}

function offlineHtml() {
  return `<div class="notice oma-card">
    <strong>${escapeHtml(t("host_offline_title"))}</strong>
    <div>${escapeHtml(t("host_offline_body"))}</div>
    ${snapshot.error ? `<div class="oma-muted oma-mono" style="font-size:11px;">${escapeHtml(snapshot.error)}</div>` : ""}
    <button class="oma-btn primary" data-retry>${escapeHtml(t("host_retry"))}</button>
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

function applyPaletteLocally(p) {
  if (!p) return;
  if (inExtension && !feature("theme")) {
    document.getElementById("oma-dynamic-vars").textContent = "";
    document.getElementById("theme-label").textContent = "";
    return;
  }
  const contrast = p.isLight ? "#ffffff" : "#0f1117";
  document.getElementById("oma-dynamic-vars").textContent = `:root {
    --oma-bg: ${p.background};
    --oma-bg-dark: ${p.darkBackground || p.background};
    --oma-bg-darker: ${p.darkerBackground || p.darkBackground || p.background};
    --oma-bg-elevated: ${p.lighterBackground || p.background};
    --oma-fg: ${p.foreground};
    --oma-fg-muted: ${p.darkForeground || p.foreground};
    --oma-accent: ${p.accent};
    --oma-accent-fg: ${contrast};
    --oma-selection: ${p.selection || p.lighterBackground};
    --oma-border: ${p.border || p.selection};
    --oma-green: ${p.green || "#9ece6a"};
    --oma-yellow: ${p.yellow || "#e0af68"};
    --oma-red: ${p.red || "#f7768e"};
    color-scheme: ${p.isLight ? "light" : "dark"};
  }`;
  document.getElementById("theme-label").textContent = `Theme: ${p.label || p.name}`;
}

function escapeHtml(str) {
  return String(str || "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

function getAllItems() {
  const items = Object.values(snapshot.state?.classifications || {});
  if (!inExtension || !snapshot.connected) return items;
  // Only show mails of accounts that are currently enabled for triage.
  const ids = new Set(snapshot.config?.mailbox?.triageAccountIds || []);
  return items.filter((c) => ids.has(c.accountId));
}

function getFilteredItems() {
  const items = getAllItems();
  if (selectedCustomer === "ALL") return items;
  if (selectedCustomer === "UNASSIGNED") return items.filter((c) => !c.customer);
  return items.filter((c) => c.customer === selectedCustomer);
}

function renderCounts() {
  const all = getAllItems();
  const triageCounts = feature("triage");
  document.getElementById("cnt-kanban").textContent = triageCounts ? all.length : "–";
  document.getElementById("cnt-rules").textContent = triageCounts ? (snapshot.config?.rules || []).length : "–";
  document.getElementById("cnt-themes").textContent = (snapshot.themes || []).length;

  const stage0 = all.filter((c) => c.stage === "stage0-rule").length;
  const pct = all.length ? Math.round((stage0 / all.length) * 100) : 0;
  document.getElementById("savings-badge").textContent = t("stage0_savings", { pct });

  const badge = document.getElementById("host-badge");
  if (snapshot.connected) { badge.className = "oma-badge ok"; badge.textContent = t("host_active"); }
  else if (!inExtension) { badge.className = "oma-badge accent"; badge.textContent = t("host_preview"); }
  else { badge.className = "oma-badge warn"; badge.textContent = t("host_offline"); }

  const triageOn = feature("triage");
  document.getElementById("readonly-badge").style.display = triageOn && isReadOnly() ? "" : "none";
  document.getElementById("readonly-badge").title = t("badge_readonly_title");
  document.getElementById("btn-triage").style.display = triageOn ? "" : "none";
  document.getElementById("savings-badge").style.display = triageOn ? "" : "none";

  const aiCfg = snapshot.config?.ai;
  if (aiCfg) {
    document.getElementById("lbl-classifier").textContent = `Stage 1: ${aiCfg.classifier?.model || aiCfg.classifier?.backend}`;
    document.getElementById("lbl-agent").textContent = `Stage 2: ${aiCfg.agent?.backend}`;
  }
}

function renderCustomerFilterBar() {
  const bar = document.getElementById("customer-filter-bar");
  if (currentView !== "kanban") {
    bar.style.display = "none";
    return;
  }
  if (!feature("triage")) {
    bar.style.display = "none";
    return;
  }
  bar.style.display = "flex";
  const customers = new Set();
  for (const r of snapshot.config?.rules || []) if (r.customer) customers.add(r.customer);
  for (const c of getAllItems()) if (c.customer) customers.add(c.customer);

  const pills = [
    { key: "ALL", label: t("filter_all") },
    ...[...customers].sort().map((c) => ({ key: c, label: c })),
    { key: "UNASSIGNED", label: t("filter_unassigned") },
  ];
  bar.innerHTML = `<span class="oma-muted" style="font-size:11px; margin-right:4px;">${escapeHtml(t("filter_customer"))}</span>` +
    pills.map((p) => `<button class="filter-pill ${selectedCustomer === p.key ? "active" : ""}" data-cust="${escapeHtml(p.key)}">${escapeHtml(p.label)}</button>`).join("");

  bar.querySelectorAll("[data-cust]").forEach((btn) => {
    btn.addEventListener("click", () => {
      selectedCustomer = btn.dataset.cust;
      renderMain();
      renderCustomerFilterBar();
    });
  });
}

function renderMain() {
  const container = document.getElementById("main-content");
  const titles = { kanban: t("view_kanban"), rules: t("view_rules"), themes: t("view_themes") };
  document.getElementById("view-title").textContent = titles[currentView] || "OmaThunder";

  if (hostOffline()) {
    container.innerHTML = offlineHtml();
    container.querySelector("[data-retry]").addEventListener("click", () => refreshSnapshot());
    return;
  }

  if ((currentView === "kanban" || currentView === "rules") && !feature("triage")) {
    container.innerHTML = noticeHtml(t("feat_off_triage"));
    bindSettingsButtons(container);
    return;
  }
  if (currentView === "themes" && !feature("theme")) {
    container.innerHTML = noticeHtml(t("feat_off_theme"));
    bindSettingsButtons(container);
    return;
  }

  if (currentView === "kanban") {
    const items = getFilteredItems();
    const cols = getKanbanColumns();
    container.innerHTML = `<div class="kanban-board">${cols.map((col, idx) => {
      const colItems = items.filter((c) => (c.kanbanColumn || "incoming") === col.id);
      return `
        <div class="kanban-col" data-col="${col.id}">
          <div class="kanban-col-hdr">
            <span>${escapeHtml(col.label)}</span>
            <span class="nav-count">${colItems.length}</span>
          </div>
          <div class="kanban-cards">
            ${colItems.map((c) => `
              <div class="k-card ${c.messageId === selectedMessageId ? "selected" : ""}" draggable="true" data-msgid="${escapeHtml(c.messageId)}">
                <div class="k-card-top">
                  ${c.customer ? `<span class="oma-badge accent">${escapeHtml(c.customer)}</span>` : `<span class="oma-badge">${escapeHtml(t("badge_inbox"))}</span>`}
                  <span class="oma-badge ${c.urgency === "high" ? "err" : c.urgency === "medium" ? "warn" : "ok"}">${escapeHtml(c.urgency || "low")}</span>
                </div>
                <div class="k-card-title">${escapeHtml(c.subject)}</div>
                <div class="k-card-from">${escapeHtml(c.from)}</div>
                <div class="k-card-sum">${escapeHtml(summaryText(c))}</div>
                <div class="k-card-actions">
                  <span class="oma-mono oma-muted" style="font-size:10px;">${escapeHtml(c.deadline || c.amount || c.folder || "")}</span>
                  <div style="display:flex; gap:4px;">
                    ${inExtension ? `<button class="mini-btn" data-open-id="${escapeHtml(c.messageId)}" title="${escapeHtml(t("card_open"))}">↗</button>` : ""}
                    ${idx > 0 ? `<button class="mini-btn" data-move-id="${escapeHtml(c.messageId)}" data-move-col="${cols[idx - 1].id}" title="${escapeHtml(t("card_prev"))}">←</button>` : ""}
                    ${idx < cols.length - 1 ? `<button class="mini-btn" data-move-id="${escapeHtml(c.messageId)}" data-move-col="${cols[idx + 1].id}" title="${escapeHtml(t("card_next"))}">→</button>` : ""}
                  </div>
                </div>
              </div>
            `).join("")}
          </div>
        </div>
      `;
    }).join("")}</div>`;

    container.querySelectorAll(".k-card").forEach((card) => {
      card.addEventListener("click", () => {
        // Clicking the selected card again switches the assistant back to the open mail.
        selectedMessageId = selectedMessageId === card.dataset.msgid && inExtension ? null : card.dataset.msgid;
        renderMain();
        renderRightPane();
      });
      card.addEventListener("dragstart", (e) => {
        e.dataTransfer.setData("text/plain", card.dataset.msgid);
      });
    });

    container.querySelectorAll(".kanban-col").forEach((colEl) => {
      colEl.addEventListener("dragover", (e) => { e.preventDefault(); colEl.classList.add("drag-over"); });
      colEl.addEventListener("dragleave", () => colEl.classList.remove("drag-over"));
      colEl.addEventListener("drop", async (e) => {
        e.preventDefault();
        colEl.classList.remove("drag-over");
        const msgId = e.dataTransfer.getData("text/plain");
        if (msgId) await moveKanbanCard(msgId, colEl.dataset.col);
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

  if (currentView === "rules") {
    const rules = snapshot.config?.rules || [];
    const accounts = triageAccounts();
    const accountIds = new Set(accounts.map((a) => a.id));
    const folderOpts = (snapshot.folders || []).filter((f) => !inExtension || accountIds.has(f.accountId)).map((f) =>
      `<option value="${escapeHtml(f.path)}">${escapeHtml(f.accountName ? `[${f.accountName}] ${f.path}` : f.path)}</option>`
    ).join("");
    const accountOpts = accounts.map((a) => `<option value="${escapeHtml(a.id)}">${escapeHtml(a.name || a.id)}</option>`).join("");
    const accountName = (id) => (snapshot.accounts || []).find((a) => a.id === id)?.name || id || "—";
    container.innerHTML = `
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
            <label class="oma-muted" style="font-size:11px;">${escapeHtml(t("rules_lbl_customer"))}</label>
            <input class="oma-input" id="rule-customer" placeholder="${escapeHtml(t("rules_ph_customer"))}" />
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
        <div style="display:flex; justify-content:space-between; align-items:center; gap:10px; margin-top:8px; flex-wrap:wrap;">
          <label style="display:flex; align-items:center; gap:6px; font-size:11.5px; cursor:pointer;">
            <input type="checkbox" id="chk-autocreate-folder" checked />
            <span>${escapeHtml(t("rules_autocreate"))}</span>
          </label>
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

      <div class="oma-card">
        <table class="rules-table">
          <thead>
            <tr>
              <th>${escapeHtml(t("rules_th_customer"))}</th>
              <th>${escapeHtml(t("rules_th_folder"))}</th>
              <th>${escapeHtml(t("rules_th_domains"))}</th>
              <th>${escapeHtml(t("rules_th_automove"))}</th>
              <th>${escapeHtml(t("rules_th_scan"))}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            ${rules.length === 0 ? `<tr><td colspan="6" class="oma-muted">${escapeHtml(t("rules_empty"))}</td></tr>` : rules.map((r) => {
              const subList = Object.entries(r.columnFolders || {}).map(([col, path]) => `${col} → ${path}`).join(" · ");
              return `
              <tr>
                <td><strong>${escapeHtml(r.customer)}</strong><div class="oma-muted" style="font-size:10.5px;">${escapeHtml(accountName(r.accountId))}</div></td>
                <td>
                  <div class="oma-mono">${escapeHtml(r.folderPath)}</div>
                  ${subList ? `<div class="oma-muted oma-mono" style="font-size:10.5px; margin-top:2px;">${escapeHtml(subList)}</div>` : `<div class="oma-muted" style="font-size:10.5px; margin-top:2px;">${escapeHtml(t("rules_sub_flex"))}</div>`}
                </td>
                <td class="oma-mono">${escapeHtml((r.domains || []).join(", "))}</td>
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
    `;

    const editing = editingRuleId ? rules.find((r) => r.id === editingRuleId) : null;
    if (editing) {
      if (editing.accountId) document.getElementById("rule-account").value = editing.accountId;
      document.getElementById("rule-customer").value = editing.customer || "";
      document.getElementById("rule-folder").value = editing.folderPath || "";
      document.getElementById("rule-domains").value = (editing.domains || []).join(", ");
      document.getElementById("rule-col-waiting").value = editing.columnFolders?.waiting || "";
      document.getElementById("rule-col-done").value = editing.columnFolders?.done || "";
    }
    container.querySelectorAll("[data-edit-rule]").forEach((btn) => {
      btn.addEventListener("click", () => {
        editingRuleId = editingRuleId === btn.dataset.editRule ? null : btn.dataset.editRule;
        renderMain();
      });
    });

    document.getElementById("btn-add-rule").addEventListener("click", async () => {
      const customer = document.getElementById("rule-customer").value.trim();
      const folderPath = document.getElementById("rule-folder").value.trim();
      const domains = document.getElementById("rule-domains").value.split(",").map((s) => s.trim().toLowerCase()).filter(Boolean);
      const waitSub = document.getElementById("rule-col-waiting").value.trim();
      const doneSub = document.getElementById("rule-col-done").value.trim();
      const autoCreate = document.getElementById("chk-autocreate-folder").checked;
      const accountId = document.getElementById("rule-account").value;
      if (!accountId || !customer || !folderPath || domains.length === 0) return;

      const columnFolders = {};
      if (waitSub) columnFolders.waiting = waitSub;
      if (doneSub) columnFolders.done = doneSub;

      const folderObj = (snapshot.folders || []).find((f) => f.accountId === accountId && f.path.toLowerCase() === folderPath.toLowerCase());
      if (editing) {
        // Keep the rule's identity and switches; replace what the form shows.
        const { columnFolders: _old, ...kept } = editing;
        const updated = {
          ...kept, customer, accountId, folderPath, folderId: folderObj?.id || "", domains,
          ...(Object.keys(columnFolders).length ? { columnFolders } : {}),
        };
        editingRuleId = null;
        await saveRules((snapshot.config?.rules || []).map((r) => (r.id === updated.id ? updated : r)), autoCreate);
        return;
      }
      const nextRules = [...(snapshot.config?.rules || []), {
        id: `rule-${Date.now()}`,
        customer,
        accountId,
        folderPath,
        folderId: folderObj?.id || "",
        domains,
        // Auto-move stays off until the user enables it explicitly in the table.
        autoMove: false,
        includeInScan: true,
        ...(Object.keys(columnFolders).length ? { columnFolders } : {}),
      }];
      await saveRules(nextRules, autoCreate);
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

async function moveKanbanCard(messageId, column) {
  if (inExtension && snapshot.connected) {
    try {
      const res = await api.runtime.sendMessage({ type: "omathunder:moveKanban", messageId, column });
      if (res?.state) snapshot.state = res.state;
      const skipped = res?.effects?.skipped;
      if (skipped && skipped !== "read_only") showMessage(errText(new Error(skipped)), "warn");
    } catch (err) {
      showMessage(errText(err), "err");
    }
  } else if (snapshot.state?.classifications?.[messageId]) {
    snapshot.state.classifications[messageId].kanbanColumn = column;
  }
  renderCounts();
  renderMain();
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
    customer: rule.customer,
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

function renderRightPane() {
  const assistantOn = !inExtension || (feature("assistant") && !hostOffline());
  const offEl = document.getElementById("ai-off");
  offEl.style.display = assistantOn ? "none" : "";
  document.getElementById("ai-mode-tabs").style.display = assistantOn ? "" : "none";
  document.getElementById("ai-pane-body").style.display = assistantOn ? "" : "none";
  if (!assistantOn) {
    offEl.innerHTML = hostOffline() ? "" : noticeHtml(t("feat_off_assistant"));
    bindSettingsButtons(offEl);
    document.getElementById("proposal-count").textContent = "";
    return;
  }

  const selected = selectedMessageId ? snapshot.state?.classifications?.[selectedMessageId] : null;
  document.getElementById("btn-use-displayed").style.display = selected && inExtension ? "" : "none";
  if (!selected) renderDisplayedMailInfo();
  else document.getElementById("selected-title").textContent = selected.subject;
  const custBadge = document.getElementById("selected-customer");
  if (selected?.customer) {
    custBadge.style.display = "inline-flex";
    custBadge.textContent = selected.customer;
  } else {
    custBadge.style.display = "none";
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

  const pending = (snapshot.state?.proposals || []).filter((p) => p.status === "pending");
  document.getElementById("proposal-count").textContent = t("ai_proposals_count", { count: pending.length });

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
        const res = await api.runtime.sendMessage({ type: "omathunder:dismissProposal", id });
        if (res?.state) snapshot.state = res.state;
      } else {
        const prop = (snapshot.state?.proposals || []).find((p) => p.id === id);
        if (prop) prop.status = "dismissed";
      }
      renderRightPane();
    });
  });
}

async function selectTheme(name) {
  if (inExtension && snapshot.connected) {
    const payload = await api.runtime.sendMessage({ type: "omathunder:setTheme", preset: name, mode: "preset" });
    if (payload) { snapshot.theme = payload; applyPaletteLocally(payload.palette); }
  } else {
    const pal = (snapshot.themes || []).find((th) => th.name === name);
    if (pal) { snapshot.theme = { palette: pal, source: "preview" }; applyPaletteLocally(pal); }
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
  renderCustomerFilterBar();
  renderMain();
  renderRightPane();
  if (inExtension && snapshot.connected && (lang === "de" || lang === "en")) {
    snapshot.config.language = lang;
    await api.runtime.sendMessage({ type: "omathunder:saveConfig", config: { language: lang } }).catch(() => {});
  }
}

async function init() {
  document.querySelectorAll(".nav-item[data-view]").forEach((btn) => {
    btn.addEventListener("click", () => {
      document.querySelectorAll(".nav-item").forEach((b) => b.classList.remove("active"));
      btn.classList.add("active");
      currentView = btn.dataset.view;
      renderCustomerFilterBar();
      renderMain();
    });
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
    renderCustomerFilterBar();
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
        const res = await api.runtime.sendMessage({ type: "omathunder:triageInbox", language: window.OMA_I18N ? window.OMA_I18N.getLang() : "" });
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
        ansEl.innerHTML = `<div class="oma-card"><strong>${escapeHtml(res.provider || "Agent")}</strong><p style="margin:6px 0 0;">${escapeHtml(res.answer)}</p>${issuesHtml}</div>`;
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
        { subject: "Neue API-Zugänge für Staging-Test", from: "jonas@beta-systems.com", targetFolder: "Kunden/Beta GmbH" },
        { subject: "Freigabe Q4-Architektur & NDA bis Freitag", from: "laura@acme.de", targetFolder: "Kunden/Acme/Incoming" },
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
    await refreshSnapshot();
    api.runtime.onMessage.addListener((msg) => {
      if (msg?.type === "omathunder:scanFinished") {
        lastReport = msg.report || null;
        refreshSnapshot().then(renderScanReport);
        return;
      }
      if (msg?.type === "omathunder:themeChanged" && msg.payload && feature("theme")) {
        snapshot.theme = msg.payload;
        applyPaletteLocally(msg.payload.palette);
        renderMain();
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
    applyPaletteLocally(snapshot.theme?.palette);
    renderCounts();
    renderCustomerFilterBar();
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
  }
  applyPaletteLocally(snapshot.theme?.palette);
  renderCounts();
  renderCustomerFilterBar();
  renderMain();
  renderRightPane();
  if (snapshot.connected && snapshot.protocolOk === false) {
    showMessage(t("host_mismatch", { host: snapshot.host?.version, ext: snapshot.extensionVersion }), "err");
  }
}

init();
