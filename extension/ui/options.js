/* global browser, messenger */
const api = typeof messenger !== "undefined" ? messenger : typeof browser !== "undefined" ? browser : null;
const inExtension = Boolean(api?.runtime?.sendMessage);

const t = (k, vars) => (window.OMA_I18N ? window.OMA_I18N.t(k, vars) : k);
const $ = (id) => document.getElementById(id);

const PREVIEW_ACCOUNTS = [
  { id: "account1", name: "Firma (IMAP)" },
  { id: "account2", name: "Privat (Gmail)" },
];

function renderAccounts(accounts, enabledIds) {
  const box = $("triage-accounts");
  box.innerHTML = "";
  if (!accounts.length) {
    box.innerHTML = `<span class="oma-muted">${t("opts_no_accounts")}</span>`;
    return;
  }
  for (const acc of accounts) {
    const label = document.createElement("label");
    label.className = "check";
    label.style.marginTop = "2px";
    const cb = document.createElement("input");
    cb.type = "checkbox";
    cb.value = acc.id;
    cb.dataset.account = "1";
    cb.checked = enabledIds.includes(acc.id);
    const span = document.createElement("span");
    span.textContent = acc.name || acc.id;
    label.append(cb, span);
    box.appendChild(label);
  }
}

function updateDependentFields() {
  const triageOn = $("feat-triage").checked;
  document.querySelectorAll(".safety input:not(#mb-readonly)").forEach((el) => {
    el.disabled = !triageOn;
  });
}

async function init() {
  if (window.OMA_I18N) {
    window.OMA_I18N.setLang("auto");
    window.OMA_I18N.applyDom();
  }
  let cfg = null;
  let accounts = PREVIEW_ACCOUNTS;
  let hostThemes = null;
  if (inExtension) {
    const snap = await api.runtime.sendMessage({ type: "omathunder:getSnapshot" }).catch(() => null);
    cfg = snap?.config;
    accounts = snap?.accounts || [];
    hostThemes = snap?.themes || null;
  }
  const features = cfg?.features || { theme: true, assistant: true, triage: false };
  const mb = cfg?.mailbox || { readOnly: true, triageAccountIds: [], autoMoveOnlyUnread: true, scanLimit: 20, scanDays: 30 };

  const langPref = cfg?.language || "auto";
  $("ui-lang").value = langPref;
  if (window.OMA_I18N && langPref !== "auto") {
    window.OMA_I18N.setLang(langPref);
    window.OMA_I18N.applyDom();
  }
  $("feat-theme").checked = Boolean(features.theme);
  $("feat-assistant").checked = Boolean(features.assistant);
  $("feat-triage").checked = Boolean(features.triage);
  $("mb-readonly").checked = mb.readOnly !== false;
  $("mb-only-unread").checked = mb.autoMoveOnlyUnread !== false;
  $("mb-scan-limit").value = mb.scanLimit || 20;
  $("mb-scan-days").value = mb.scanDays || 30;
  renderAccounts(accounts, mb.triageAccountIds || []);
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
  $("theme-mode").value = cfg?.theme?.mode || "auto";
  $("theme-preset").value = cfg?.theme?.preset || "tokyo-night";
  $("clf-backend").value = cfg?.ai?.classifier?.backend || "gemini-api";
  $("clf-model").value = cfg?.ai?.classifier?.model || "gemini-4-hydrogen";
  $("agent-backend").value = cfg?.ai?.agent?.backend || "claude-cli";
  $("agent-model").value = cfg?.ai?.agent?.model || "";
  $("ai-classify").checked = cfg?.ai?.classifyWithAi !== false;
  $("ai-max-calls").value = cfg?.ai?.maxAiCallsPerScan || 50;
  $("ai-allow-cli").checked = Boolean(cfg?.ai?.allowUnrestrictedCli);

  $("feat-triage").addEventListener("change", updateDependentFields);
  $("ui-lang").addEventListener("change", (e) => {
    if (window.OMA_I18N) {
      window.OMA_I18N.setLang(e.target.value);
      window.OMA_I18N.applyDom();
      renderAccounts(accounts, [...document.querySelectorAll("[data-account]:checked")].map((cb) => cb.value));
      updateDependentFields();
    }
  });

  $("btn-save").addEventListener("click", async () => {
    const selectedLang = $("ui-lang").value;
    if (window.OMA_I18N) {
      window.OMA_I18N.setLang(selectedLang);
      window.OMA_I18N.applyDom();
    }
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
        triageAccountIds: [...document.querySelectorAll("[data-account]:checked")].map((cb) => cb.value),
        autoMoveOnlyUnread: $("mb-only-unread").checked,
        scanLimit: Number($("mb-scan-limit").value) || 20,
        scanDays: Number($("mb-scan-days").value) || 30,
      },
      theme: {
        mode: $("theme-mode").value,
        preset: $("theme-preset").value,
      },
      ai: {
        allowUnrestrictedCli: $("ai-allow-cli").checked,
        classifyWithAi: $("ai-classify").checked,
        maxAiCallsPerScan: Number($("ai-max-calls").value) || 50,
        classifier: {
          backend: $("clf-backend").value,
          model: $("clf-model").value.trim(),
        },
        agent: {
          backend: $("agent-backend").value,
          model: $("agent-model").value.trim(),
        },
      },
    };
    const st = $("save-status");
    if (!inExtension) {
      st.textContent = t("opts_preview_saved");
      return;
    }
    try {
      const res = await api.runtime.sendMessage({ type: "omathunder:saveConfig", config: patch });
      if (res?.config) cfg = res.config;
      st.textContent = t("opts_saved");
    } catch (err) {
      st.textContent = window.OMA_I18N ? window.OMA_I18N.errText(err) : String(err?.message || err);
    }
  });
}

init();
