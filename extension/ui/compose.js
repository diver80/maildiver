/* global browser, messenger */
// Popup of the button in the compose window: works on the draft the user is writing right now.
const api = typeof messenger !== "undefined" ? messenger : typeof browser !== "undefined" ? browser : null;
const inExtension = Boolean(api?.runtime?.sendMessage);
const t = (k, vars) => (window.OMA_I18N ? window.OMA_I18N.t(k, vars) : k);
const errText = (err) => (window.OMA_I18N ? window.OMA_I18N.errText(err) : String(err?.message || err));
const $ = (id) => document.getElementById(id);

let mode = "improve_draft";
let tone = "freundlich";
let tabId = null;
let context = { ownText: "", hasRelated: false };

function card(text, muted = false) {
  const el = document.createElement("div");
  el.className = `oma-card oma-pre${muted ? " oma-muted" : ""}`;
  el.textContent = text;
  return el;
}

function updateHint() {
  const hints = {
    improve_draft: context.ownText ? "cmp_hint_improve" : "cmp_hint_empty",
    review_draft: context.ownText ? "cmp_hint_review" : "cmp_hint_empty",
    draft_reply: context.hasRelated ? "cmp_hint_reply" : "cmp_hint_no_related",
    compose_new: "cmp_hint_new",
  };
  $("cmp-hint").textContent = t(hints[mode]);
  const needsDraft = mode === "improve_draft" || mode === "review_draft";
  $("cmp-run").disabled = (needsDraft && !context.ownText) || (mode === "draft_reply" && !context.hasRelated);
}

function showResult(res) {
  const out = $("cmp-out");
  out.innerHTML = "";
  if (res.answer) out.appendChild(card(res.answer));
  if (res.reviewIssues.length) {
    const ul = document.createElement("ul");
    ul.className = "issues";
    for (const issue of res.reviewIssues) {
      const li = document.createElement("li");
      li.textContent = issue;
      ul.appendChild(li);
    }
    out.appendChild(ul);
  }
  if (!res.text) return;
  const box = document.createElement("div");
  box.className = "result";
  box.textContent = res.text;
  box.style.marginTop = "8px";
  out.appendChild(box);

  const row = document.createElement("div");
  row.style.cssText = "display:flex; gap:8px; margin-top:8px;";
  const apply = document.createElement("button");
  apply.className = "oma-btn primary";
  apply.style.cssText = "flex:1; justify-content:center;";
  apply.textContent = t(context.ownText ? "cmp_replace" : "cmp_insert");
  apply.addEventListener("click", async () => {
    try {
      await api.runtime.sendMessage({ type: "omathunder:composeApply", tabId, text: res.text });
      window.close();
    } catch (err) {
      out.appendChild(card(errText(err), true));
    }
  });
  const copy = document.createElement("button");
  copy.className = "oma-btn ghost";
  copy.textContent = t("cmp_copy");
  copy.addEventListener("click", () => navigator.clipboard?.writeText(res.text));
  row.append(apply, copy);
  out.appendChild(row);
}

async function init() {
  if (window.OMA_I18N) {
    window.OMA_I18N.setLang("auto");
    window.OMA_I18N.applyDom();
  }
  document.querySelectorAll(".mode").forEach((btn) => {
    btn.addEventListener("click", () => {
      document.querySelectorAll(".mode").forEach((b) => b.classList.remove("active"));
      btn.classList.add("active");
      mode = btn.dataset.mode;
      updateHint();
    });
  });
  document.querySelectorAll(".tone").forEach((btn) => {
    btn.addEventListener("click", () => {
      document.querySelectorAll(".tone").forEach((b) => b.classList.remove("active"));
      btn.classList.add("active");
      tone = btn.dataset.tone;
    });
  });

  if (inExtension) {
    try {
      const tabs = await api.tabs.query({ active: true, currentWindow: true });
      tabId = tabs[0]?.id ?? null;
      context = await api.runtime.sendMessage({ type: "omathunder:composeContext", tabId });
    } catch (err) {
      $("cmp-out").appendChild(card(errText(err), true));
    }
  } else {
    context = { ownText: "Preview", hasRelated: true };
  }
  updateHint();

  $("cmp-run").addEventListener("click", async () => {
    const out = $("cmp-out");
    out.innerHTML = "";
    out.appendChild(card(t("ai_working", { mode: t(`cmp_mode_${{ improve_draft: "improve", review_draft: "review", draft_reply: "reply", compose_new: "new" }[mode]}`) }), true));
    if (!inExtension) return;
    $("cmp-run").disabled = true;
    try {
      const res = await api.runtime.sendMessage({
        type: "omathunder:composeAssist",
        tabId,
        mode,
        tone,
        question: $("cmp-notes").value,
        language: window.OMA_I18N ? window.OMA_I18N.getLang() : "",
      });
      showResult(res);
    } catch (err) {
      out.innerHTML = "";
      out.appendChild(card(errText(err), true));
    } finally {
      $("cmp-run").disabled = false;
    }
  });
}

init();
