/* global browser, messenger */
// Settings of the add-on: what OmaThunder may do (accounts & permissions, bridge, modules) and how
// Thunderbird looks. AI, triage scans and archive settings live in the OmaThunder app.
const api = typeof messenger !== "undefined" ? messenger : typeof browser !== "undefined" ? browser : null;
const inExtension = Boolean(api?.runtime?.sendMessage);

const t = (k, vars) => (window.OMA_I18N ? window.OMA_I18N.t(k, vars) : k);
const $ = (id) => document.getElementById(id);

function applyTheme(themePayload, themeEnabled = true) {
  const el = $("oma-dynamic-vars");
  if (el) el.textContent = themeEnabled && themePayload?.uiCssVars ? themePayload.uiCssVars : "";
}

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
  if (!snap?.connected) {
    row(t("st_host"), "err", t("st_host_off"), t("st_host_off_desc"));
    return;
  }
  row(t("st_host"), "ok", t("st_host_on", { version: snap.host?.version || "?" }));
  if (snap.protocolOk === false) {
    row(t("st_versions"), "err", t("st_versions_bad"), t("st_versions_bad_desc", { host: snap.host?.version, ext: snap.extensionVersion }));
  } else {
    row(t("st_versions"), "ok", t("st_versions_ok", { ext: snap.extensionVersion }));
  }
  const bridge = snap.config?.bridge || {};
  row(t("st_bridge"), bridge.enabled ? "ok" : "accent", bridge.enabled ? t("st_bridge_on") : t("st_bridge_off"));
  if (snap.doctor?.envFile) row(t("st_keys"), "accent", snap.doctor.envFile, t("st_keys_desc"));
}

function renderAccounts(accounts, enabledIds) {
  const box = $("accounts");
  box.innerHTML = "";
  if (!accounts.length) {
    box.innerHTML = `<span class="oma-muted">${t("opts_no_accounts")}</span>`;
    return;
  }
  const enabled = new Set(enabledIds || []);
  for (const acc of accounts) {
    const row = document.createElement("div");
    row.className = "account-row";
    const label = document.createElement("label");
    label.className = "check";
    const cb = document.createElement("input");
    cb.type = "checkbox";
    cb.value = acc.id;
    cb.dataset.account = "1";
    cb.checked = enabled.has(acc.id);
    const name = document.createElement("span");
    name.textContent = acc.name || acc.id;
    label.append(cb, name);
    row.appendChild(label);
    box.appendChild(row);
  }
}

function renderPresets(themes, current) {
  const sel = $("theme-preset");
  sel.innerHTML = "";
  const list = Array.isArray(themes) && themes.length ? themes : [{ name: current || "tokyo-night", label: current || "tokyo-night" }];
  for (const th of list) {
    const opt = document.createElement("option");
    opt.value = th.name || th.id;
    opt.textContent = th.label || th.name;
    sel.appendChild(opt);
  }
  if (current) sel.value = current;
}

function updateBridgeFields() {
  const on = $("bridge-enabled").checked;
  $("bridge-send-row").style.display = on ? "" : "none";
  if (!on) $("bridge-allow-send").checked = false;
}

function applyLanguage(lang) {
  if (!window.OMA_I18N) return;
  window.OMA_I18N.setLang(lang || "auto");
  window.OMA_I18N.applyDom();
}

async function snapshot() {
  if (!inExtension) return null;
  return api.runtime.sendMessage({ type: "omathunder:getSnapshot" }).catch(() => null);
}

function fill(snap) {
  const cfg = snap?.config || {};
  const features = cfg.features || { theme: true, assistant: true, triage: false };
  const mb = cfg.mailbox || { readOnly: true, triageAccountIds: [] };
  $("ui-lang").value = cfg.language || "auto";
  applyLanguage(cfg.language);
  applyTheme(snap?.theme, features.theme !== false);
  renderStatus(snap);
  renderAccounts(snap?.accounts || [], mb.triageAccountIds);
  renderPresets(snap?.themes, cfg.theme?.preset);
  $("mb-readonly").checked = mb.readOnly !== false;
  $("feat-assistant").checked = features.assistant !== false;
  $("feat-triage").checked = Boolean(features.triage);
  $("feat-theme").checked = features.theme !== false;
  $("theme-mode").value = cfg.theme?.mode || "auto";
  $("theme-style-body").checked = Boolean(cfg.theme?.styleBody);
  $("bridge-enabled").checked = Boolean(cfg.bridge?.enabled);
  $("bridge-allow-send").checked = Boolean(cfg.bridge?.allowSend);
  updateBridgeFields();
  const disabled = !snap?.connected;
  document.querySelectorAll("input, select, #btn-save").forEach((el) => { el.disabled = disabled; });
}

async function init() {
  applyLanguage("auto");
  let snap = await snapshot();
  fill(snap);
  $("bridge-enabled").addEventListener("change", updateBridgeFields);

  $("btn-save").addEventListener("click", async () => {
    const st = $("save-status");
    st.className = "save-status oma-muted";
    // A partial config: the host merges it, so settings made in the app stay untouched.
    const patch = {
      language: $("ui-lang").value,
      features: {
        theme: $("feat-theme").checked,
        assistant: $("feat-assistant").checked,
        triage: $("feat-triage").checked,
      },
      mailbox: {
        readOnly: $("mb-readonly").checked,
        triageAccountIds: [...document.querySelectorAll("[data-account]:checked")].map((cb) => cb.value),
      },
      // Only enabled/allowSend: bridge.port and bridge.accountIds stay as configured in config.json.
      bridge: {
        enabled: $("bridge-enabled").checked,
        allowSend: $("bridge-enabled").checked && $("bridge-allow-send").checked,
      },
      theme: {
        mode: $("theme-mode").value,
        preset: $("theme-preset").value,
        styleBody: $("theme-style-body").checked,
      },
    };
    try {
      await api.runtime.sendMessage({ type: "omathunder:saveConfig", config: patch });
      snap = await snapshot();
      fill(snap);
      st.className = "save-status ok";
      st.textContent = t("opts_saved");
    } catch (err) {
      st.className = "save-status err";
      st.textContent = window.OMA_I18N ? window.OMA_I18N.errText(err) : String(err?.message || err);
    }
  });

  if (inExtension) {
    api.runtime.onMessage.addListener((msg) => {
      if (msg?.type === "omathunder:themeChanged") applyTheme(msg.payload, $("feat-theme").checked);
    });
  }
}

init();
