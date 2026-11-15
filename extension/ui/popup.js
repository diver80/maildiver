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
  if (sug.delete) lines.push(t("filter_action_delete"));
  return lines.join("\n");
}

function renderConditionChips(condResults) {
  const list = document.createElement("div");
  list.className = "cond-list";
  for (const c of condResults || []) {
    const chip = document.createElement("span");
    chip.className = `cond-chip ${c.matched ? "hit" : "miss"}`;
    const fieldLabel = c.headerName ? `${c.field}(${c.headerName})` : c.field;
    const hintSuffix = c.hint ? ` (${t(`diag_hint_${c.hint}`)})` : "";
    chip.textContent = `${c.matched ? "✓" : "✗"} ${fieldLabel} ${c.op} "${c.value}"${hintSuffix}`;
    list.appendChild(chip);
  }
  return list;
}

function filterActionBadgeText(sug) {
  const target = sug?.targetFilterName || "";
  if (sug?.action === "append_or" && target) return t("filter_act_append_or", { name: target });
  if (sug?.action === "fix_and_to_or" && target) return t("filter_act_fix_and", { name: target });
  if (sug?.action === "replace" && target) return t("filter_act_replace", { name: target });
  return t("filter_act_create");
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

async function runFilterDiagnosis() {
  const out = document.getElementById("popup-out");
  out.innerHTML = `<div class="oma-card oma-muted">${t("popup_analyzing")}</div>`;
  if (!inExtension) {
    out.innerHTML = `<div class="oma-card">${t("popup_preview_diagnose")}</div>`;
    return;
  }
  const usePending = pendingAvailable;
  pendingAvailable = false;
  try {
    const res = await api.runtime.sendMessage({
      type: "omathunder:diagnoseFilters",
      usePendingSelection: usePending,
    });
    const diag = res?.diagnostic || {};
    const msg = res?.message || {};
    const accountId = msg.accountId || diag.messageAccountId || "";
    out.innerHTML = "";
    const wrap = document.createElement("div");
    wrap.className = "oma-card";

    const hdr = document.createElement("div");
    hdr.style.cssText = "font-weight:700; font-size:12px;";
    hdr.textContent = t("diag_title");
    wrap.appendChild(hdr);

    const meta = document.createElement("div");
    meta.className = "oma-muted";
    meta.style.cssText = "font-size:11px; margin-top:2px;";
    meta.textContent = t("diag_checked_meta", {
      count: diag.checkedFilters ?? 0,
      domain: diag.senderDomain || "—",
    });
    wrap.appendChild(meta);

    if (!res?.supported) {
      const warn = document.createElement("div");
      warn.className = "oma-badge warn";
      warn.style.marginTop = "8px";
      warn.textContent = t("diag_api_unavailable");
      wrap.appendChild(warn);
      out.appendChild(wrap);
      return;
    }

    const matched = Array.isArray(diag.matchedFilters) ? diag.matchedFilters : [];
    const nearMisses = Array.isArray(diag.nearMisses) ? diag.nearMisses : [];
    const issues = Array.isArray(diag.issues) ? diag.issues : [];

    for (const m of matched) {
      const box = document.createElement("div");
      box.className = "sug-box";
      const top = document.createElement("div");
      top.style.cssText = "display:flex; justify-content:space-between; align-items:center; gap:6px;";
      const name = document.createElement("strong");
      name.textContent = m.filterName;
      const badge = document.createElement("span");
      badge.className = "oma-badge ok";
      badge.textContent = t("diag_badge_matched");
      top.append(name, badge);
      box.appendChild(top);
      if (m.actionsSummary) {
        const act = document.createElement("div");
        act.className = "oma-muted oma-mono";
        act.style.cssText = "font-size:10.5px; margin-top:3px;";
        act.textContent = m.actionsSummary;
        box.appendChild(act);
      }
      box.appendChild(renderConditionChips(m.conditionResults));
      if (accountId) {
        const runRow = document.createElement("div");
        runRow.style.cssText = "display:flex; gap:6px; margin-top:6px;";
        const runBtn = document.createElement("button");
        runBtn.className = "oma-btn";
        runBtn.style.cssText = "padding:4px 8px; font-size:11px;";
        runBtn.textContent = t("diag_run_inbox");
        runBtn.addEventListener("click", async () => {
          runBtn.disabled = true;
          try {
            const r = await api.runtime.sendMessage({
              type: "omathunder:runNativeFilters",
              accountId,
              filterNames: [m.filterName],
            });
            runBtn.textContent = t("diag_run_done", { count: r?.executed ?? 1 });
          } catch (err) {
            runBtn.textContent = errText(err);
          }
        });
        runRow.appendChild(runBtn);
        box.appendChild(runRow);
      }
      wrap.appendChild(box);
    }

    for (const nm of nearMisses) {
      const box = document.createElement("div");
      box.className = "sug-box";
      const top = document.createElement("div");
      top.style.cssText = "display:flex; justify-content:space-between; align-items:center; gap:6px;";
      const name = document.createElement("strong");
      name.textContent = nm.filterName;
      const badge = document.createElement("span");
      badge.className = "oma-badge warn";
      badge.textContent = t(`diag_reason_${nm.reasonCode || "near_miss"}`);
      top.append(name, badge);
      box.appendChild(top);

      if (nm.reason) {
        const reason = document.createElement("div");
        reason.className = "oma-muted";
        reason.style.cssText = "font-size:11px; margin-top:3px;";
        reason.textContent = nm.reason;
        box.appendChild(reason);
      }
      box.appendChild(renderConditionChips(nm.conditionResults));

      if (nm.quickFix && accountId) {
        const fixRow = document.createElement("div");
        fixRow.style.cssText = "display:flex; gap:6px; margin-top:6px;";
        const fixBtn = document.createElement("button");
        fixBtn.className = "oma-btn primary";
        fixBtn.style.cssText = "padding:4px 8px; font-size:11px;";
        fixBtn.textContent = nm.quickFix === "enable" ? t("diag_fix_enable") : t("diag_fix_set_or");
        fixBtn.addEventListener("click", async () => {
          fixBtn.disabled = true;
          try {
            await api.runtime.sendMessage({
              type: "omathunder:applyNativeFilter",
              spec: nm.quickFix === "enable"
                ? { accountId, mode: "toggle", existingName: nm.filterName, enabled: true }
                : { accountId, mode: "fix_match_all", existingName: nm.filterName, matchAll: false, enable: true },
            });
            await runFilterDiagnosis();
          } catch (err) {
            fixBtn.textContent = errText(err);
          }
        });
        fixRow.appendChild(fixBtn);
        box.appendChild(fixRow);
      }
      wrap.appendChild(box);
    }

    if (matched.length === 0 && nearMisses.length === 0) {
      const none = document.createElement("div");
      none.className = "oma-muted";
      none.style.cssText = "font-size:11.5px; margin-top:8px;";
      none.textContent = t("diag_no_match");
      wrap.appendChild(none);
    }

    if (issues.length > 0) {
      const issHdr = document.createElement("div");
      issHdr.style.cssText = "font-weight:600; font-size:11.5px; margin-top:10px;";
      issHdr.textContent = t("diag_issues_hdr", { count: issues.length });
      wrap.appendChild(issHdr);

      for (const iss of issues.slice(0, 4)) {
        const row = document.createElement("div");
        row.className = "sug-box";
        const top = document.createElement("div");
        top.style.cssText = "display:flex; justify-content:space-between; align-items:center; gap:6px;";
        const name = document.createElement("strong");
        name.textContent = iss.filterName;
        const badge = document.createElement("span");
        badge.className = `oma-badge ${iss.severity === "error" ? "err" : "warn"}`;
        badge.textContent = iss.code;
        top.append(name, badge);
        const det = document.createElement("div");
        det.className = "oma-muted";
        det.style.cssText = "font-size:11px; margin-top:2px;";
        det.textContent = iss.detail;
        row.append(top, det);
        if (iss.quickFix && accountId) {
          const fixBtn = document.createElement("button");
          fixBtn.className = "oma-btn";
          fixBtn.style.cssText = "padding:3px 7px; font-size:10.5px; margin-top:5px;";
          fixBtn.textContent = iss.quickFix === "enable" ? t("diag_fix_enable") : t("diag_fix_set_or");
          fixBtn.addEventListener("click", async () => {
            fixBtn.disabled = true;
            try {
              await api.runtime.sendMessage({
                type: "omathunder:applyNativeFilter",
                spec: iss.quickFix === "enable"
                  ? { accountId, mode: "toggle", existingName: iss.filterName, enabled: true }
                  : { accountId, mode: "fix_match_all", existingName: iss.filterName, matchAll: false, enable: true },
              });
              await runFilterDiagnosis();
            } catch (err) {
              fixBtn.textContent = errText(err);
            }
          });
          row.appendChild(fixBtn);
        }
        wrap.appendChild(row);
      }
    }

    const footer = document.createElement("div");
    footer.style.cssText = "display:flex; gap:6px; margin-top:10px; flex-wrap:wrap;";
    const aiBtn = document.createElement("button");
    aiBtn.className = "oma-btn primary";
    aiBtn.style.cssText = "flex:1; justify-content:center; font-size:11.5px;";
    aiBtn.textContent = t("diag_ai_solve");
    aiBtn.addEventListener("click", () => {
      const q = document.getElementById("popup-question")?.value.trim() || "";
      runSelectionAnalysis("suggest_filter", q);
    });
    footer.appendChild(aiBtn);

    if (accountId) {
      const dlgBtn = document.createElement("button");
      dlgBtn.className = "oma-btn ghost";
      dlgBtn.style.cssText = "font-size:11px; padding:5px 8px;";
      dlgBtn.textContent = t("diag_open_tb_filters");
      dlgBtn.addEventListener("click", () => {
        api.runtime.sendMessage({ type: "omathunder:openNativeFilters", accountId }).catch(() => {});
      });
      footer.appendChild(dlgBtn);
    }
    wrap.appendChild(footer);
    out.appendChild(wrap);
  } catch (err) {
    const card = document.createElement("div");
    card.className = "oma-card oma-muted";
    card.textContent = errText(err);
    out.innerHTML = "";
    out.appendChild(card);
  }
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
      const diag = res.filterDiagnostic;
      if (diag && ((diag.nearMisses || []).length > 0 || (diag.issues || []).length > 0)) {
        const info = document.createElement("div");
        info.className = "oma-badge warn";
        info.style.cssText = "margin-top:6px; display:inline-flex;";
        info.textContent = t("filter_diag_banner", {
          near: (diag.nearMisses || []).length,
          issues: (diag.issues || []).length,
        });
        wrap.appendChild(info);
      }
      const suggestions = Array.isArray(res.filterSuggestions) ? res.filterSuggestions : [];
      const accountId = res.accountId || selectionInfo.accountId || "";
      for (const sug of suggestions) {
        const box = document.createElement("div");
        box.className = "sug-box";

        const badge = document.createElement("span");
        badge.className = `oma-badge ${sug.action === "fix_and_to_or" ? "warn" : "accent"}`;
        badge.style.cssText = "margin-bottom:4px; display:inline-flex;";
        badge.textContent = filterActionBadgeText(sug);
        box.appendChild(badge);

        const title = document.createElement("div");
        title.style.fontWeight = "600";
        title.textContent = sug.name || sug.targetFilterName || t("popup_suggest_filter");
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
        let appliedFilterName = sug.targetFilterName || "";

        if (accountId && res.nativeFiltersSupported !== false && window.OmaCore?.buildNativeFilterSpecFromSuggestion) {
          const applyBtn = document.createElement("button");
          applyBtn.className = "oma-btn primary";
          applyBtn.style.cssText = "padding:4px 8px; font-size:11px;";
          applyBtn.textContent = t("popup_apply_tb_filter");

          const runBtn = document.createElement("button");
          runBtn.className = "oma-btn";
          runBtn.style.cssText = "padding:4px 8px; font-size:11px; display:none;";
          runBtn.textContent = t("diag_run_inbox");

          applyBtn.addEventListener("click", async () => {
            applyBtn.disabled = true;
            try {
              const spec = window.OmaCore.buildNativeFilterSpecFromSuggestion(sug, accountId);
              const applied = await api.runtime.sendMessage({
                type: "omathunder:applyNativeFilter",
                spec,
              });
              appliedFilterName = applied?.filter?.name || appliedFilterName || spec.name;
              applyBtn.textContent = t("popup_tb_filter_applied", { name: appliedFilterName });
              runBtn.style.display = "";
            } catch (err) {
              applyBtn.disabled = false;
              applyBtn.textContent = errText(err);
            }
          });

          runBtn.addEventListener("click", async () => {
            runBtn.disabled = true;
            try {
              const r = await api.runtime.sendMessage({
                type: "omathunder:runNativeFilters",
                accountId,
                filterNames: appliedFilterName ? [appliedFilterName] : undefined,
              });
              runBtn.textContent = t("diag_run_done", { count: r?.executed ?? 1 });
            } catch (err) {
              runBtn.disabled = false;
              runBtn.textContent = errText(err);
            }
          });

          row.append(applyBtn, runBtn);
        }

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
                  space: sug.name || sug.targetFilterName || "Filter",
                  accountId,
                  folderPath: sug.suggestedFolder,
                  domains: sug.domains || [],
                  fromContains: sug.fromContains || [],
                  subjectContains: sug.subjectContains || [],
                  nativeFilterNames: appliedFilterName ? [appliedFilterName] : [],
                  autoMove: false,
                  includeInScan: true,
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
        document.getElementById("popup-ai-extras").style.display = features.assistant || snap.nativeFiltersSupported ? "" : "none";
        document.getElementById("btn-extract-ics").style.display = features.assistant ? "" : "none";
        document.getElementById("btn-suggest-filter").style.display = features.assistant ? "" : "none";
        document.getElementById("btn-diagnose-filter").style.display = snap.nativeFiltersSupported === false ? "none" : "";
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
  document.getElementById("btn-diagnose-filter").addEventListener("click", () => {
    runFilterDiagnosis();
  });

  if (autoMode === "diagnose_filter") {
    await runFilterDiagnosis();
  } else if (assistantEnabled && (autoMode === "summarize" || autoMode === "extract_events" || autoMode === "suggest_filter")) {
    await runSelectionAnalysis(autoMode);
  }
}

init();
