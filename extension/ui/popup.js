/* global browser, messenger */
const api = typeof messenger !== "undefined" ? messenger : typeof browser !== "undefined" ? browser : null;
const inExtension = Boolean(api?.runtime?.sendMessage);

const QUICK_THEMES = [
  { name: "tokyo-night", label: "Tokyo Night" },
  { name: "catppuccin", label: "Catppuccin" },
  { name: "nord", label: "Nord" },
  { name: "gruvbox", label: "Gruvbox" },
  { name: "osaka-jade", label: "Osaka Jade" },
  { name: "flexoki-light", label: "Flexoki Light" },
];

function renderQuickThemes(active) {
  const el = document.getElementById("quick-themes");
  el.innerHTML = QUICK_THEMES.map((t) =>
    `<button class="pill ${t.name === active ? "active" : ""}" data-t="${t.name}">${t.label}</button>`
  ).join("");
  el.querySelectorAll("[data-t]").forEach((b) => {
    b.addEventListener("click", async () => {
      if (inExtension) {
        await api.runtime.sendMessage({ type: "omathunder:setTheme", preset: b.dataset.t, mode: "preset" });
      }
      renderQuickThemes(b.dataset.t);
    });
  });
}

const t = (k, vars) => (window.OMA_I18N ? window.OMA_I18N.t(k, vars) : k);

async function init() {
  if (window.OMA_I18N) {
    window.OMA_I18N.setLang("auto");
    window.OMA_I18N.applyDom();
  }
  renderQuickThemes("tokyo-night");
  if (inExtension) {
    const snap = await api.runtime.sendMessage({ type: "omathunder:getSnapshot" }).catch(() => null);
    if (snap) {
      if (snap.config?.language && snap.config.language !== "auto" && window.OMA_I18N) {
        window.OMA_I18N.setLang(snap.config.language);
        window.OMA_I18N.applyDom();
      }
      const badge = document.getElementById("popup-status");
      badge.textContent = snap.connected ? t("popup_host_active") : t("popup_setup_needed");
      badge.className = `oma-badge ${snap.connected ? "ok" : "warn"}`;
      if (snap.barSummary) {
        document.getElementById("st-action").textContent = snap.barSummary.actionCount ?? 0;
        document.getElementById("st-proposals").textContent = snap.barSummary.pendingProposals ?? 0;
      }
      if (snap.theme?.palette?.name) renderQuickThemes(snap.theme.palette.name);
      const features = snap.config?.features;
      if (features) {
        document.getElementById("quick-theme-card").style.display = features.theme ? "" : "none";
        document.getElementById("btn-analyze-mail").style.display = features.assistant ? "" : "none";
        document.getElementById("stats-grid").style.display = features.triage || features.assistant ? "" : "none";
        document.getElementById("popup-readonly").style.display =
          features.triage && snap.config?.mailbox?.readOnly !== false ? "" : "none";
      }
    }
  }

  document.getElementById("btn-open-space").addEventListener("click", () => {
    if (inExtension) api.runtime.sendMessage({ type: "omathunder:openSpace" });
    else window.location.href = "space.html";
  });
  document.getElementById("btn-open-opts").addEventListener("click", () => {
    if (inExtension && api.runtime.openOptionsPage) api.runtime.openOptionsPage();
    else window.location.href = "options.html";
  });
  document.getElementById("btn-analyze-mail").addEventListener("click", async () => {
    const out = document.getElementById("popup-out");
    out.innerHTML = `<div class="oma-card oma-muted">${t("popup_analyzing")}</div>`;
    if (inExtension) {
      try {
        const res = await api.runtime.sendMessage({
          type: "omathunder:analyzeMessage",
          mode: "summarize",
          language: window.OMA_I18N ? window.OMA_I18N.getLang() : "",
        });
        const card = document.createElement("div");
        card.className = "oma-card";
        card.textContent = res.answer || "";
        out.innerHTML = "";
        out.appendChild(card);
      } catch (err) {
        const card = document.createElement("div");
        card.className = "oma-card oma-muted";
        card.textContent = window.OMA_I18N ? window.OMA_I18N.errText(err) : err.message;
        out.innerHTML = "";
        out.appendChild(card);
      }
    } else {
      out.innerHTML = `<div class="oma-card">${t("popup_preview_analyze")}</div>`;
    }
  });
}

init();
