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

function applyTheme(themePayload, themeEnabled = true) {
  const el = document.getElementById("oma-dynamic-vars");
  if (!el) return;
  if (!themeEnabled || !themePayload?.uiCssVars) {
    el.textContent = "";
    return;
  }
  el.textContent = themePayload.uiCssVars;
}

function renderQuickThemes(active) {
  const el = document.getElementById("quick-themes");
  el.innerHTML = QUICK_THEMES.map((t) =>
    `<button class="pill ${t.name === active ? "active" : ""}" data-t="${t.name}">${t.label}</button>`
  ).join("");
  el.querySelectorAll("[data-t]").forEach((b) => {
    b.addEventListener("click", async () => {
      if (inExtension) {
        try {
          const payload = await api.runtime.sendMessage({ type: "omathunder:setTheme", preset: b.dataset.t, mode: "preset" });
          if (payload) applyTheme(payload, true);
        } catch (err) {
          document.getElementById("popup-out").textContent = window.OMA_I18N ? window.OMA_I18N.errText(err) : err.message;
          return;
        }
      }
      renderQuickThemes(b.dataset.t);
    });
  });
}

const t = (k, vars) => (window.OMA_I18N ? window.OMA_I18N.t(k, vars) : k);
const errText = (err) => (window.OMA_I18N ? window.OMA_I18N.errText(err) : err.message);

let targetLang = "";
let selectionInfo = { count: 0, subjects: [], froms: [] };
let pendingAvailable = false;

function renderLangPills() {
  document.querySelectorAll("#popup-lang-pills [data-target-lang]").forEach((btn) => {
    btn.classList.toggle("active", (btn.dataset.targetLang || "") === targetLang);
  });
}

function updateSelectionUI(info) {
  selectionInfo = info || { count: 0, subjects: [], froms: [] };
  const titleEl = document.getElementById("popup-sel-title");
  const subEl = document.getElementById("popup-sel-sub");
  const sumBtn = document.getElementById("btn-analyze-mail");
  const draftBtn = document.getElementById("btn-draft-reply");
  const count = selectionInfo.count || 0;

  if (count > 1) {
    titleEl.textContent = t("popup_sel_multi", { count });
    const preview = (selectionInfo.subjects || []).filter(Boolean).slice(0, 2).join(" · ");
    subEl.textContent = preview;
    subEl.style.display = preview ? "" : "none";
    sumBtn.textContent = t("popup_summarize_multi", { count });
    draftBtn.disabled = true;
  } else if (count === 1) {
    const subj = (selectionInfo.subjects && selectionInfo.subjects[0]) || "—";
    titleEl.textContent = t("popup_sel_one", { subject: subj });
    const from = (selectionInfo.froms && selectionInfo.froms[0]) || "";
    subEl.textContent = from;
    subEl.style.display = from ? "" : "none";
    sumBtn.textContent = t("popup_summarize_one");
    draftBtn.disabled = false;
  } else {
    titleEl.textContent = t("popup_sel_none");
    subEl.textContent = "";
    subEl.style.display = "none";
    sumBtn.textContent = t("popup_summarize_one");
    draftBtn.disabled = false;
  }
}

function formatFilterText(sug) {
  const parts = [];
  if (sug.domains?.length) parts.push(`${t("filter_cond_domain")}: ${sug.domains.join(", ")}`);
  if (sug.fromContains?.length) parts.push(`${t("filter_cond_from")}: ${sug.fromContains.join(", ")}`);
  if (sug.subjectContains?.length) parts.push(`${t("filter_cond_subject")}: ${sug.subjectContains.join(", ")}`);
  const joiner = sug.matchAll ? t("filter_match_all") : t("filter_match_any");
  const lines = [
    `${sug.name || t("popup_suggest_filter")}`,
    `${t("filter_mode")}: ${joiner}`,
    ...parts,
  ];
  if (sug.suggestedFolder) lines.push(`${t("filter_target_folder")}: ${sug.suggestedFolder}`);
  if (sug.markRead) lines.push(t("filter_mark_read"));
  return lines.join("\n");
}

function downloadICS(icsContent) {
  const blob = new Blob([icsContent], { type: "text/calendar;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = "omathunder-termine.ics";
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 5000);
}

async function runSelectionAnalysis(mode = "summarize", customQuestion = "") {
  const out = document.getElementById("popup-out");
  out.innerHTML = `<div class="oma-card oma-muted">${t("popup_analyzing")}</div>`;
  if (!inExtension) {
    out.innerHTML = `<div class="oma-card">${t("popup_preview_analyze")}</div>`;
    return;
  }
  const usePending = pendingAvailable;
  pendingAvailable = false;
  try {
    const res = await api.runtime.sendMessage({
      type: "omathunder:analyzeMessage",
      mode,
      question: customQuestion,
      language: window.OMA_I18N ? window.OMA_I18N.getLang() : "",
      targetLanguage: targetLang,
      usePendingSelection: usePending,
    });
    const count = res.selectionCount || selectionInfo.count || 1;
    out.innerHTML = "";
    const wrap = document.createElement("div");
    wrap.className = "oma-card";
    if (count > 1) {
      const hdr = document.createElement("div");
      hdr.className = "oma-muted";
      hdr.style.cssText = "font-size:11px; font-weight:600; margin-bottom:6px;";
      hdr.textContent = t("popup_summary_header_multi", { count });
      wrap.appendChild(hdr);
    }
    if (res.answer) {
      const body = document.createElement("div");
      body.className = "oma-pre summary-box";
      body.textContent = res.answer;
      wrap.appendChild(body);
    }

    if (mode === "extract_events") {
      const events = Array.isArray(res.events) ? res.events : [];
      if (events.length === 0) {
        const empty = document.createElement("div");
        empty.className = "oma-muted";
        empty.style.cssText = "font-size:11.5px; margin-top:6px;";
        empty.textContent = t("popup_no_events");
        wrap.appendChild(empty);
      } else {
        for (const ev of events) {
          const box = document.createElement("div");
          box.className = "sug-box";
          const title = document.createElement("div");
          title.style.fontWeight = "600";
          title.textContent = ev.title || "Termin";
          const meta = document.createElement("div");
          meta.className = "oma-muted";
          meta.textContent = [ev.start, ev.location].filter(Boolean).join(" · ");
          box.append(title, meta);
          wrap.appendChild(box);
        }
        if (res.ics) {
          const dlRow = document.createElement("div");
          dlRow.style.cssText = "display:flex; justify-content:flex-end; margin-top:8px;";
          const dlBtn = document.createElement("button");
          dlBtn.className = "oma-btn primary";
          dlBtn.style.cssText = "padding:5px 10px; font-size:11.5px;";
          dlBtn.textContent = t("popup_download_ics");
          dlBtn.addEventListener("click", () => downloadICS(res.ics));
          dlRow.appendChild(dlBtn);
          wrap.appendChild(dlRow);
        }
      }
    }

    if (mode === "suggest_filter") {
      const suggestions = Array.isArray(res.filterSuggestions) ? res.filterSuggestions : [];
      for (const sug of suggestions) {
        const box = document.createElement("div");
        box.className = "sug-box";
        const title = document.createElement("div");
        title.style.fontWeight = "600";
        title.textContent = sug.name || t("popup_suggest_filter");
        box.appendChild(title);
        if (sug.reason) {
          const reason = document.createElement("div");
          reason.className = "oma-muted";
          reason.style.marginTop = "2px";
          reason.textContent = sug.reason;
          box.appendChild(reason);
        }
        const cond = document.createElement("pre");
        cond.className = "oma-pre oma-mono";
        cond.style.cssText = "margin:6px 0; font-size:11px; background:var(--oma-bg-dark); padding:6px; border-radius:4px;";
        cond.textContent = formatFilterText(sug);
        box.appendChild(cond);

        const row = document.createElement("div");
        row.style.cssText = "display:flex; gap:6px; margin-top:6px; flex-wrap:wrap;";
        const copyBtn = document.createElement("button");
        copyBtn.className = "oma-btn ghost";
        copyBtn.style.cssText = "padding:4px 8px; font-size:11px;";
        copyBtn.textContent = t("popup_copy_filter");
        copyBtn.addEventListener("click", async () => {
          await navigator.clipboard?.writeText(formatFilterText(sug)).catch(() => {});
          copyBtn.textContent = t("popup_copied");
          setTimeout(() => { copyBtn.textContent = t("popup_copy_filter"); }, 1500);
        });
        row.appendChild(copyBtn);

        if (sug.suggestedFolder) {
          const saveBtn = document.createElement("button");
          saveBtn.className = "oma-btn";
          saveBtn.style.cssText = "padding:4px 8px; font-size:11px;";
          saveBtn.textContent = t("popup_save_rule");
          saveBtn.addEventListener("click", async () => {
            try {
              await api.runtime.sendMessage({
                type: "omathunder:saveFilterRule",
                rule: {
                  customer: sug.name,
                  accountId: res.accountId || selectionInfo.accountId || "",
                  folderPath: sug.suggestedFolder,
                  domains: sug.domains || [],
                  fromContains: sug.fromContains || [],
                  subjectContains: sug.subjectContains || [],
                  autoMove: true,
                },
              });
              saveBtn.textContent = t("popup_rule_saved");
              saveBtn.disabled = true;
            } catch (err) {
              saveBtn.textContent = errText(err);
            }
          });
          row.appendChild(saveBtn);
        }
        box.appendChild(row);
        wrap.appendChild(box);
      }
    }

    if (res.answer && mode === "summarize") {
      const actions = document.createElement("div");
      actions.style.cssText = "display:flex; justify-content:flex-end; margin-top:8px;";
      const copyBtn = document.createElement("button");
      copyBtn.className = "oma-btn ghost";
      copyBtn.style.cssText = "padding:4px 9px; font-size:11px;";
      copyBtn.textContent = t("cmp_copy");
      copyBtn.addEventListener("click", async () => {
        await navigator.clipboard?.writeText(res.answer).catch(() => {});
        copyBtn.textContent = t("popup_copied");
        setTimeout(() => { copyBtn.textContent = t("cmp_copy"); }, 1500);
      });
      actions.appendChild(copyBtn);
      wrap.appendChild(actions);
    }
    out.appendChild(wrap);
  } catch (err) {
    const card = document.createElement("div");
    card.className = "oma-card oma-muted";
    card.textContent = errText(err);
    out.innerHTML = "";
    out.appendChild(card);
  }
}

async function init() {
  const params = new URLSearchParams(window.location.search);
  const autoMode = params.get("auto") || "";
  const qsTarget = params.get("target") || params.get("lang") || "";
  if (qsTarget === "de" || qsTarget === "en") targetLang = qsTarget;
  pendingAvailable = params.get("pending") === "1";

  if (autoMode) {
    document.body.classList.add("is-window");
    document.getElementById("stats-grid").style.display = "none";
    document.getElementById("quick-theme-card").style.display = "none";
  }

  if (window.OMA_I18N) {
    window.OMA_I18N.setLang("auto");
    window.OMA_I18N.applyDom();
  }
  renderLangPills();
  document.querySelectorAll("#popup-lang-pills [data-target-lang]").forEach((btn) => {
    btn.addEventListener("click", () => {
      targetLang = btn.dataset.targetLang || "";
      renderLangPills();
    });
  });

  renderQuickThemes("tokyo-night");
  let assistantEnabled = true;

  if (inExtension) {
    const [snap, selInfo] = await Promise.all([
      api.runtime.sendMessage({ type: "omathunder:getSnapshot" }).catch(() => null),
      api.runtime.sendMessage({ type: "omathunder:getSelectionInfo", usePendingSelection: pendingAvailable }).catch(() => null),
    ]);
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
      const themeEnabled = snap.config?.features?.theme !== false;
      applyTheme(snap.theme, themeEnabled);
      if (snap.theme?.palette?.name) renderQuickThemes(snap.theme.palette.name);
      const features = snap.config?.features;
      if (features) {
        assistantEnabled = Boolean(features.assistant);
        if (!autoMode) {
          document.getElementById("quick-theme-card").style.display = features.theme ? "" : "none";
          document.getElementById("stats-grid").style.display = features.triage || features.assistant ? "" : "none";
        }
        document.getElementById("popup-ai-card").style.display = features.assistant ? "" : "none";
        document.getElementById("btn-analyze-mail").style.display = features.assistant ? "" : "none";
        document.getElementById("popup-ai-extras").style.display = features.assistant ? "" : "none";
        document.getElementById("btn-draft-reply").style.display = features.assistant ? "" : "none";
        document.getElementById("popup-readonly").style.display =
          features.triage && snap.config?.mailbox?.readOnly !== false ? "" : "none";
      }
      api.runtime.onMessage.addListener((msg) => {
        if (msg?.type === "omathunder:themeChanged" && msg.payload) {
          applyTheme(msg.payload, snap.config?.features?.theme !== false);
          if (msg.payload.palette?.name) renderQuickThemes(msg.payload.palette.name);
        }
      });
    }
    if (selInfo) updateSelectionUI(selInfo);
  } else {
    updateSelectionUI({ count: 0, subjects: [], froms: [] });
  }

  document.getElementById("btn-open-space").addEventListener("click", () => {
    if (inExtension) api.runtime.sendMessage({ type: "omathunder:openSpace" });
    else window.location.href = "space.html";
  });
  document.getElementById("btn-open-opts").addEventListener("click", () => {
    if (inExtension && api.runtime.openOptionsPage) api.runtime.openOptionsPage();
    else window.location.href = "options.html";
  });
  document.getElementById("btn-draft-reply").addEventListener("click", async () => {
    const out = document.getElementById("popup-out");
    const note = (text) => {
      const card = document.createElement("div");
      card.className = "oma-card oma-muted";
      card.textContent = text;
      out.innerHTML = "";
      out.appendChild(card);
    };
    note(t("popup_analyzing"));
    if (!inExtension) return;
    try {
      const res = await api.runtime.sendMessage({
        type: "omathunder:analyzeMessage",
        mode: "draft_reply",
        language: window.OMA_I18N ? window.OMA_I18N.getLang() : "",
        targetLanguage: targetLang,
      });
      const prop = (res.proposals || []).find((p) => p.kind === "draft_reply");
      if (!prop) return note(res.answer || t("popup_no_draft"));
      out.innerHTML = "";
      const box = document.createElement("div");
      box.className = "oma-card";
      box.style.cssText = "white-space:pre-wrap; max-height:200px; overflow-y:auto; font-size:12px;";
      box.textContent = prop.body;
      const row = document.createElement("div");
      row.style.cssText = "display:flex; gap:6px; margin-top:8px;";
      for (const [label, replyAll] of [[t("ai_reply"), false], [t("ai_reply_all"), true]]) {
        const b = document.createElement("button");
        b.className = replyAll ? "oma-btn" : "oma-btn primary";
        b.style.cssText = "flex:1; justify-content:center;";
        b.textContent = label;
        b.addEventListener("click", async () => {
          try {
            await api.runtime.sendMessage({ type: "omathunder:applyProposal", proposal: prop, replyAll });
            window.close();
          } catch (err) {
            note(errText(err));
          }
        });
        row.appendChild(b);
      }
      out.append(box, row);
    } catch (err) {
      note(errText(err));
    }
  });

  const qInput = document.getElementById("popup-question");
  document.getElementById("btn-ask-mail").addEventListener("click", () => {
    runSelectionAnalysis("summarize", qInput.value.trim());
  });
  qInput.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      runSelectionAnalysis("summarize", qInput.value.trim());
    }
  });
  document.getElementById("btn-analyze-mail").addEventListener("click", () => {
    runSelectionAnalysis("summarize", qInput.value.trim());
  });
  document.getElementById("btn-extract-ics").addEventListener("click", () => {
    runSelectionAnalysis("extract_events");
  });
  document.getElementById("btn-suggest-filter").addEventListener("click", () => {
    runSelectionAnalysis("suggest_filter", qInput.value.trim());
  });

  if (assistantEnabled && (autoMode === "summarize" || autoMode === "extract_events" || autoMode === "suggest_filter")) {
    await runSelectionAnalysis(autoMode);
  }
}

init();
