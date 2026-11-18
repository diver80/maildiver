/* global browser, messenger */
const api = typeof messenger !== "undefined" ? messenger : typeof browser !== "undefined" ? browser : null;
const inExtension = Boolean(api?.runtime?.sendMessage);

const t = (k, vars) => (window.OMA_I18N ? window.OMA_I18N.t(k, vars) : k);
const $ = (id) => document.getElementById(id);

function applyTheme(themePayload, themeEnabled = true) {
  const el = $("oma-dynamic-vars");
  if (!el) return;
  if (!themeEnabled || !themePayload?.uiCssVars) {
    el.textContent = "";
    return;
  }
  el.textContent = themePayload.uiCssVars;
}

const PREVIEW_ACCOUNTS = [
  { id: "account1", name: "Firma (IMAP)" },
  { id: "account2", name: "Privat (Gmail)" },
];

const PREVIEW_FOLDERS = [
  { id: "a1-inbox", accountId: "account1", name: "Inbox", path: "INBOX", type: "inbox" },
  { id: "a1-atl", accountId: "account1", name: "atlassian", path: "INBOX/atlassian" },
  { id: "a1-avo", accountId: "account1", name: "avono", path: "INBOX/avono" },
  { id: "a1-hel", accountId: "account1", name: "Helvetia", path: "INBOX/Helvetia" },
  { id: "a2-inbox", accountId: "account2", name: "Inbox", path: "INBOX", type: "inbox" },
];

function collectKanbanFoldersFromDom(fallbackMap = {}) {
  const next = { ...(fallbackMap || {}) };
  document.querySelectorAll("[data-kanban-folders-acc]").forEach((box) => {
    const accId = box.dataset.kanbanFoldersAcc;
    if (!accId) return;
    next[accId] = [...box.querySelectorAll("[data-kanban-folder]:checked")].map((cb) => cb.value);
  });
  return next;
}

function renderAccounts(accounts, enabledIds, kanbanIds, folders = [], kanbanFolders = {}) {
  const box = $("triage-accounts");
  box.innerHTML = "";
  if (!accounts.length) {
    box.innerHTML = `<span class="oma-muted">${t("opts_no_accounts")}</span>`;
    return;
  }
  const enabledSet = new Set(enabledIds || []);
  const kanbanSet = new Set(kanbanIds || []);
  const core = window.OmaCore;

  for (const acc of accounts) {
    const group = document.createElement("div");
    group.className = "account-group";

    const row = document.createElement("div");
    row.className = "account-row";

    const label = document.createElement("label");
    label.className = "check";
    const cb = document.createElement("input");
    cb.type = "checkbox";
    cb.value = acc.id;
    cb.dataset.account = "1";
    cb.checked = enabledSet.has(acc.id);
    const span = document.createElement("span");
    span.textContent = acc.name || acc.id;
    label.append(cb, span);

    const kLabel = document.createElement("label");
    kLabel.className = "account-kanban";
    const kCb = document.createElement("input");
    kCb.type = "checkbox";
    kCb.value = acc.id;
    kCb.dataset.kanbanAccount = "1";
    kCb.checked = enabledSet.has(acc.id) && kanbanSet.has(acc.id);
    kCb.disabled = !cb.checked;
    const kSpan = document.createElement("span");
    kSpan.textContent = t("opts_account_kanban");
    kLabel.append(kCb, kSpan);

    cb.addEventListener("change", () => {
      if (!cb.checked) kCb.checked = false;
      updateDependentFields();
    });
    kCb.addEventListener("change", updateDependentFields);

    row.append(label, kLabel);
    group.appendChild(row);

    const inboxFolder = core?.findAccountInbox ? core.findAccountInbox(folders, acc.id) : null;
    const level1 = core?.listInboxLevel1Folders ? core.listInboxLevel1Folders(folders, acc.id) : [];
    if (inboxFolder && level1.length > 0) {
      const configured = Object.prototype.hasOwnProperty.call(kanbanFolders || {}, acc.id)
        ? kanbanFolders[acc.id]
        : null;
      const selectedPaths = new Set(
        (Array.isArray(configured) ? configured : [inboxFolder.path]).map((p) => String(p || "").replace(/^\/+|\/+$/g, "").toLowerCase()),
      );

      const folderWrap = document.createElement("div");
      folderWrap.className = "account-folders";
      folderWrap.dataset.kanbanFoldersAcc = acc.id;

      const hdr = document.createElement("div");
      hdr.className = "account-folders-hdr";
      const hdrTitle = document.createElement("span");
      hdrTitle.textContent = t("opts_kanban_folders");
      const quickBtns = document.createElement("span");
      quickBtns.style.display = "inline-flex";
      quickBtns.style.gap = "8px";
      const btnAll = document.createElement("button");
      btnAll.type = "button";
      btnAll.className = "oma-btn ghost";
      btnAll.style.padding = "1px 6px";
      btnAll.style.fontSize = "10.5px";
      btnAll.textContent = t("opts_kanban_folders_all");
      btnAll.addEventListener("click", () => {
        folderWrap.querySelectorAll("[data-kanban-folder]").forEach((fCb) => {
          if (!fCb.disabled) fCb.checked = true;
        });
      });
      const btnInboxOnly = document.createElement("button");
      btnInboxOnly.type = "button";
      btnInboxOnly.className = "oma-btn ghost";
      btnInboxOnly.style.padding = "1px 6px";
      btnInboxOnly.style.fontSize = "10.5px";
      btnInboxOnly.textContent = t("opts_kanban_folders_inbox");
      btnInboxOnly.addEventListener("click", () => {
        folderWrap.querySelectorAll("[data-kanban-folder]").forEach((fCb, idx) => {
          if (!fCb.disabled) fCb.checked = idx === 0;
        });
      });
      quickBtns.append(btnAll, btnInboxOnly);
      hdr.append(hdrTitle, quickBtns);

      const chips = document.createElement("div");
      chips.className = "folder-chips";
      const folderEntries = [
        { path: inboxFolder.path, label: inboxFolder.name || "Inbox" },
        ...level1.map((f) => ({ path: f.path, label: f.name || f.path })),
      ];
      for (const entry of folderEntries) {
        const clean = String(entry.path || "").replace(/^\/+|\/+$/g, "");
        const chip = document.createElement("label");
        chip.className = "folder-chip";
        const fCb = document.createElement("input");
        fCb.type = "checkbox";
        fCb.value = clean;
        fCb.dataset.kanbanFolder = acc.id;
        fCb.checked = selectedPaths.has(clean.toLowerCase());
        const fSpan = document.createElement("span");
        fSpan.textContent = entry.label;
        chip.append(fCb, fSpan);
        chips.appendChild(chip);
      }

      folderWrap.append(hdr, chips);
      group.appendChild(folderWrap);
    }

    box.appendChild(group);
  }
}

function backendReady(backend, doctor) {
  const clis = doctor?.clis || {};
  const keys = doctor?.envKeys || {};
  switch (backend) {
    case "gemini-api": return keys.GEMINI_API_KEY ? ["ok", "st_key_found"] : ["warn", "st_key_missing"];
    case "claude-api": return keys.ANTHROPIC_API_KEY ? ["ok", "st_key_found"] : ["warn", "st_key_missing"];
    case "claude-cli": return clis.claude ? ["ok", "st_cli_found"] : ["warn", "st_cli_missing"];
    case "gemini-cli": return clis.gemini ? ["ok", "st_cli_found"] : ["warn", "st_cli_missing"];
    case "opencode-cli": return clis.opencode ? ["ok", "st_cli_found"] : ["warn", "st_cli_missing"];
    case "ollama": return clis.ollama ? ["ok", "st_cli_found"] : ["warn", "st_cli_missing"];
    default: return ["warn", "st_cli_missing"];
  }
}

function standaloneBackendReady(backend, secretsMeta) {
  switch (backend) {
    case "gemini-api": return secretsMeta?.hasGeminiKey ? ["ok", "st_key_found"] : ["warn", "st_key_missing"];
    case "claude-api": return secretsMeta?.hasAnthropicKey ? ["ok", "st_key_found"] : ["warn", "st_key_missing"];
    case "ollama": return ["ok", "st_ollama_local"];
    case "claude-cli":
    case "gemini-cli":
    case "opencode-cli":
      return ["warn", "st_cli_needs_host"];
    default: return ["warn", "st_cli_needs_host"];
  }
}

function updateSecretPlaceholders(secretsMeta) {
  const g = $("standalone-gemini-key");
  const a = $("standalone-anthropic-key");
  if (g) g.placeholder = secretsMeta?.hasGeminiKey ? t("opts_key_saved_ph") : t("opts_key_ph");
  if (a) a.placeholder = secretsMeta?.hasAnthropicKey ? t("opts_key_saved_ph") : t("opts_key_ph");
}

// Shows whether host, versions and the configured AI backends are ready, so failures are explainable.
function renderStatus(snap) {
  const box = $("status-rows");
  box.innerHTML = "";
  const row = (label, kind, text, sub = "") => {
    const el = document.createElement("div");
    el.className = "status-row";
    const l = document.createElement("div");
    l.textContent = label;
    const r = document.createElement("div");
    const badge = document.createElement("span");
    badge.className = `oma-badge ${kind}`;
    badge.textContent = text;
    r.appendChild(badge);
    if (sub) {
      const s = document.createElement("div");
      s.className = "oma-muted check-sub";
      s.textContent = sub;
      r.appendChild(s);
    }
    el.append(l, r);
    box.appendChild(el);
  };
  const triageCb = $("feat-triage");
  const triageNote = $("feat-triage-host-note");
  if (!inExtension) {
    if (triageCb) triageCb.disabled = false;
    if (triageNote) triageNote.style.display = "none";
    row(t("st_host"), "accent", t("host_preview"));
    return;
  }
  const ai = snap?.config?.ai || {};
  if (!snap?.connected) {
    if (triageCb) triageCb.disabled = true;
    if (triageNote) triageNote.style.display = "";
    row(t("st_host"), "accent", t("st_standalone_on"), t("st_standalone_desc"));
    const agentBackend = ai.agent?.backend || "gemini-api";
    const [aKind, aKey] = standaloneBackendReady(agentBackend, snap?.secretsMeta);
    row(t("st_assistant"), aKind, `${agentBackend}: ${t(aKey)}`);
    return;
  }
  if (triageCb) triageCb.disabled = false;
  if (triageNote) triageNote.style.display = "none";
  row(t("st_host"), "ok", t("st_host_on", { version: snap.host?.version || "?" }));
  if (snap.protocolOk === false) {
    row(t("st_versions"), "err", t("st_versions_bad"), t("host_mismatch", { host: snap.host?.version, ext: snap.extensionVersion }));
  } else {
    row(t("st_versions"), "ok", t("st_versions_ok", { ext: snap.extensionVersion }));
  }
  const [aKind, aKey] = backendReady(ai.agent?.backend, snap.doctor);
  row(t("st_assistant"), aKind, `${ai.agent?.backend}: ${t(aKey)}`);
  if (ai.classifyWithAi) {
    const [cKind, cKey] = backendReady(ai.classifier?.backend, snap.doctor);
    row(t("st_classifier"), cKind, `${ai.classifier?.backend}: ${t(cKey)}`);
  } else {
    row(t("st_classifier"), "ok", t("report_ai_off"));
  }
  row(t("st_keys"), "accent", snap.doctor?.envFile || "–", t("st_keys_desc"));
}

function updateDependentFields() {
  const triageOn = $("feat-triage").checked && !$("feat-triage").disabled;
  document.querySelectorAll(".safety input:not(#mb-readonly)").forEach((el) => {
    el.disabled = !triageOn;
  });
  document.querySelectorAll(".account-group").forEach((group) => {
    const accCb = group.querySelector("[data-account]");
    const kanbanCb = group.querySelector("[data-kanban-account]");
    if (accCb && kanbanCb) {
      kanbanCb.disabled = !triageOn || !accCb.checked;
    }
    const foldersDisabled = !triageOn || !accCb?.checked || !kanbanCb?.checked;
    group.querySelectorAll("[data-kanban-folder]").forEach((fCb) => {
      fCb.disabled = foldersDisabled;
    });
    group.querySelectorAll(".account-folders button").forEach((btn) => {
      btn.disabled = foldersDisabled;
    });
  });
}

// Bridge for the native app: allowSend is only offered while the bridge is on; both need the Go host.
function updateBridgeFields(hostAvailable) {
  const enabled = $("bridge-enabled");
  const send = $("bridge-allow-send");
  if (!enabled || !send) return;
  enabled.disabled = !hostAvailable;
  const note = $("bridge-host-note");
  if (note) note.style.display = hostAvailable ? "none" : "";
  const on = enabled.checked && hostAvailable;
  $("bridge-send-row").style.display = enabled.checked ? "" : "none";
  send.disabled = !on;
}

function switchOptionsTab(tabName) {
  const valid = ["general", "accounts", "theme", "ai"].includes(tabName) ? tabName : "general";
  document.querySelectorAll(".opts-tab[data-tab]").forEach((btn) => {
    const active = btn.dataset.tab === valid;
    btn.classList.toggle("active", active);
    btn.setAttribute("aria-selected", active ? "true" : "false");
  });
  document.querySelectorAll(".opts-panel").forEach((panel) => {
    panel.classList.toggle("active", panel.id === `panel-${valid}`);
  });
}

async function init() {
  document.querySelectorAll(".opts-tab[data-tab]").forEach((btn) => {
    btn.addEventListener("click", () => switchOptionsTab(btn.dataset.tab));
  });
  const qsTab = new URLSearchParams(window.location.search).get("tab") || window.location.hash.replace(/^#/, "");
  if (qsTab) switchOptionsTab(qsTab);

  if (window.OMA_I18N) {
    window.OMA_I18N.setLang("auto");
    window.OMA_I18N.applyDom();
  }
  let cfg = null;
  let accounts = PREVIEW_ACCOUNTS;
  let folders = PREVIEW_FOLDERS;
  let hostThemes = null;
  let lastSnap = null;
  if (inExtension) {
    const snap = await api.runtime.sendMessage({ type: "omathunder:getSnapshot" }).catch(() => null);
    cfg = snap?.config;
    lastSnap = snap;
    accounts = snap?.accounts || [];
    folders = snap?.folders || [];
    hostThemes = snap?.themes || null;
  }
  const features = cfg?.features || { theme: true, assistant: true, triage: false };
  const mb = cfg?.mailbox || { readOnly: true, triageAccountIds: [], kanbanAccountIds: [], kanbanFolders: {}, autoMoveOnlyUnread: true, scanLimit: 20, scanDays: 30 };

  applyTheme(lastSnap?.theme, features.theme !== false);

  const langPref = cfg?.language || "auto";
  $("ui-lang").value = langPref;
  if (window.OMA_I18N && langPref !== "auto") {
    window.OMA_I18N.setLang(langPref);
    window.OMA_I18N.applyDom();
  }
  $("mb-scan-new").checked = Boolean(mb.scanOnNewMail);
  $("feat-theme").checked = Boolean(features.theme);
  $("feat-assistant").checked = Boolean(features.assistant);
  $("feat-triage").checked = Boolean(features.triage);
  $("mb-readonly").checked = mb.readOnly !== false;
  $("mb-only-unread").checked = mb.autoMoveOnlyUnread !== false;
  $("mb-scan-limit").value = mb.scanLimit || 20;
  $("mb-scan-days").value = mb.scanDays || 30;
  $("mb-archive-dir").value = mb.archiveDir || "";
  $("bridge-enabled").checked = cfg?.bridge?.enabled === true;
  $("bridge-allow-send").checked = cfg?.bridge?.allowSend === true;
  const bridgeHostAvailable = () => !inExtension || Boolean(lastSnap?.connected);
  updateBridgeFields(bridgeHostAvailable());
  renderAccounts(accounts, mb.triageAccountIds || [], mb.kanbanAccountIds || [], folders, mb.kanbanFolders || {});
  renderStatus(lastSnap);
  updateSecretPlaceholders(lastSnap?.secretsMeta);
  updateDependentFields();

  // Offer every palette the host knows, so saving never replaces a preset chosen in the gallery.
  const themes = hostThemes;
  if (Array.isArray(themes) && themes.length > 0) {
    const sel = $("theme-preset");
    sel.innerHTML = "";
    for (const th of themes) {
      const opt = document.createElement("option");
      opt.value = th.name;
      opt.textContent = `${th.label || th.name}${th.isLight ? " (light)" : ""}`;
      sel.appendChild(opt);
    }
  }
  $("theme-style-body").checked = Boolean(cfg?.theme?.styleBody);
  $("theme-mode").value = cfg?.theme?.mode || "auto";
  const initialTheme = { mode: cfg?.theme?.mode || "auto", preset: cfg?.theme?.preset || "tokyo-night" };
  $("theme-preset").value = cfg?.theme?.preset || "tokyo-night";
  $("clf-backend").value = cfg?.ai?.classifier?.backend || "gemini-api";
  $("clf-model").value = cfg?.ai?.classifier?.model || "gemini-4-hydrogen";
  $("clf-endpoint").value = cfg?.ai?.classifier?.endpoint || "";
  $("agent-backend").value = cfg?.ai?.agent?.backend || (lastSnap?.connected ? "claude-cli" : "gemini-api");
  $("agent-model").value = cfg?.ai?.agent?.model || "";
  $("agent-endpoint").value = cfg?.ai?.agent?.endpoint || "";
  $("ai-classify").checked = Boolean(cfg?.ai?.classifyWithAi);
  $("ai-max-calls").value = cfg?.ai?.maxAiCallsPerScan || 50;
  $("ai-allow-cli").checked = Boolean(cfg?.ai?.allowUnrestrictedCli);

  if (inExtension) {
    api.runtime.onMessage.addListener((msg) => {
      if (msg?.type === "omathunder:themeChanged" && msg.payload) {
        applyTheme(msg.payload, $("feat-theme").checked);
      }
    });
  }

  $("feat-triage").addEventListener("change", updateDependentFields);
  $("bridge-enabled").addEventListener("change", () => updateBridgeFields(bridgeHostAvailable()));
  $("ui-lang").addEventListener("change", (e) => {
    if (window.OMA_I18N) {
      const curKanbanFolders = collectKanbanFoldersFromDom(cfg?.mailbox?.kanbanFolders || {});
      window.OMA_I18N.setLang(e.target.value);
      window.OMA_I18N.applyDom();
      renderAccounts(
        accounts,
        [...document.querySelectorAll("[data-account]:checked")].map((cb) => cb.value),
        [...document.querySelectorAll("[data-kanban-account]:checked")].map((cb) => cb.value),
        folders,
        curKanbanFolders,
      );
      renderStatus(lastSnap);
      updateSecretPlaceholders(lastSnap?.secretsMeta);
      updateDependentFields();
    }
  });

  $("btn-save").addEventListener("click", async () => {
    const selectedLang = $("ui-lang").value;
    if (window.OMA_I18N) {
      window.OMA_I18N.setLang(selectedLang);
      window.OMA_I18N.applyDom();
    }
    const enabledAccountIds = [...document.querySelectorAll("[data-account]:checked")].map((cb) => cb.value);
    const enabledSet = new Set(enabledAccountIds);
    const kanbanIds = [...document.querySelectorAll("[data-kanban-account]:checked")]
      .map((cb) => cb.value)
      .filter((id) => enabledSet.has(id));
    const kanbanFolders = collectKanbanFoldersFromDom(cfg?.mailbox?.kanbanFolders || {});
    // Partial config: the host merges it, so rules saved elsewhere (Kanban tab) are never overwritten.
    const patch = {
      language: selectedLang,
      features: {
        theme: $("feat-theme").checked,
        assistant: $("feat-assistant").checked,
        triage: $("feat-triage").checked,
      },
      mailbox: {
        readOnly: $("mb-readonly").checked,
        triageAccountIds: enabledAccountIds,
        kanbanAccountIds: kanbanIds,
        kanbanFolders,
        autoMoveOnlyUnread: $("mb-only-unread").checked,
        scanOnNewMail: $("mb-scan-new").checked,
        scanLimit: Number($("mb-scan-limit").value) || 20,
        scanDays: Number($("mb-scan-days").value) || 30,
        archiveDir: $("mb-archive-dir").value.trim(),
      },
      // Only enabled/allowSend: bridge.port and bridge.accountIds stay as configured in config.json.
      // Sending is switched off together with the bridge.
      ...(bridgeHostAvailable()
        ? {
            bridge: {
              enabled: $("bridge-enabled").checked,
              allowSend: $("bridge-enabled").checked && $("bridge-allow-send").checked,
            },
          }
        : {}),
      // Mode/preset only when changed here: a theme picked in the gallery or popup meanwhile stays.
      theme: {
        ...($("theme-mode").value !== initialTheme.mode ? { mode: $("theme-mode").value } : {}),
        ...($("theme-preset").value !== initialTheme.preset ? { preset: $("theme-preset").value } : {}),
        styleBody: $("theme-style-body").checked,
      },
      ai: {
        allowUnrestrictedCli: $("ai-allow-cli").checked,
        classifyWithAi: $("ai-classify").checked,
        maxAiCallsPerScan: Number($("ai-max-calls").value) || 50,
        classifier: {
          backend: $("clf-backend").value,
          model: $("clf-model").value.trim(),
          endpoint: $("clf-endpoint").value.trim(),
        },
        agent: {
          backend: $("agent-backend").value,
          model: $("agent-model").value.trim(),
          endpoint: $("agent-endpoint").value.trim(),
        },
      },
    };
    const geminiKey = $("standalone-gemini-key").value.trim();
    const anthropicKey = $("standalone-anthropic-key").value.trim();
    const secrets = {};
    if (geminiKey) secrets.geminiApiKey = geminiKey;
    if (anthropicKey) secrets.anthropicApiKey = anthropicKey;

    const st = $("save-status");
    st.className = "save-status oma-muted";
    if (!inExtension) {
      st.className = "save-status ok";
      st.textContent = t("opts_preview_saved");
      return;
    }
    try {
      const res = await api.runtime.sendMessage({
        type: "omathunder:saveConfig",
        config: patch,
        ...(Object.keys(secrets).length ? { secrets } : {}),
      });
      if (geminiKey) $("standalone-gemini-key").value = "";
      if (anthropicKey) $("standalone-anthropic-key").value = "";
      if (res?.config) {
        cfg = res.config;
        initialTheme.mode = res.config.theme?.mode || initialTheme.mode;
        initialTheme.preset = res.config.theme?.preset || initialTheme.preset;
      }
      applyTheme(res?.theme, $("feat-theme").checked);
      st.className = "save-status ok";
      st.textContent = t("opts_saved");
      const freshSnap = await api.runtime.sendMessage({ type: "omathunder:getSnapshot" }).catch(() => lastSnap);
      lastSnap = freshSnap;
      renderStatus(freshSnap);
      updateSecretPlaceholders(freshSnap?.secretsMeta);
      updateDependentFields();
      if (!$("bridge-enabled").checked) $("bridge-allow-send").checked = false;
      updateBridgeFields(bridgeHostAvailable());
    } catch (err) {
      st.className = "save-status err";
      st.textContent = window.OMA_I18N ? window.OMA_I18N.errText(err) : String(err?.message || err);
    }
  });
}

init();
