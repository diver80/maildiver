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

function renderAccounts(accounts, enabledIds, kanbanIds) {
  const box = $("triage-accounts");
  box.innerHTML = "";
  if (!accounts.length) {
    box.innerHTML = `<span class="oma-muted">${t("opts_no_accounts")}</span>`;
    return;
  }
  const enabledSet = new Set(enabledIds || []);
  const kanbanSet = new Set(kanbanIds || []);
  for (const acc of accounts) {
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

    row.append(label, kLabel);
    box.appendChild(row);
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
  document.querySelectorAll(".account-row").forEach((row) => {
    const accCb = row.querySelector("[data-account]");
    const kanbanCb = row.querySelector("[data-kanban-account]");
    if (accCb && kanbanCb) {
      kanbanCb.disabled = !triageOn || !accCb.checked;
    }
  });
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
  let hostThemes = null;
  let lastSnap = null;
  if (inExtension) {
    const snap = await api.runtime.sendMessage({ type: "omathunder:getSnapshot" }).catch(() => null);
    cfg = snap?.config;
    lastSnap = snap;
    accounts = snap?.accounts || [];
    hostThemes = snap?.themes || null;
  }
  const features = cfg?.features || { theme: true, assistant: true, triage: false };
  const mb = cfg?.mailbox || { readOnly: true, triageAccountIds: [], kanbanAccountIds: [], autoMoveOnlyUnread: true, scanLimit: 20, scanDays: 30 };

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
  renderAccounts(accounts, mb.triageAccountIds || [], mb.kanbanAccountIds || []);
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
  $("ui-lang").addEventListener("change", (e) => {
    if (window.OMA_I18N) {
      window.OMA_I18N.setLang(e.target.value);
      window.OMA_I18N.applyDom();
      renderAccounts(
        accounts,
        [...document.querySelectorAll("[data-account]:checked")].map((cb) => cb.value),
        [...document.querySelectorAll("[data-kanban-account]:checked")].map((cb) => cb.value),
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
        autoMoveOnlyUnread: $("mb-only-unread").checked,
        scanOnNewMail: $("mb-scan-new").checked,
        scanLimit: Number($("mb-scan-limit").value) || 20,
        scanDays: Number($("mb-scan-days").value) || 30,
        archiveDir: $("mb-archive-dir").value.trim(),
      },
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
    } catch (err) {
      st.className = "save-status err";
      st.textContent = window.OMA_I18N ? window.OMA_I18N.errText(err) : String(err?.message || err);
    }
  });
}

init();
