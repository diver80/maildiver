/* OmaThunder core: pure decision logic without Thunderbird API calls, so it can be unit-tested.
 * Loaded before background.js; exposes globalThis.OmaCore. */
(function (root) {
  class PolicyError extends Error {
    constructor(code) {
      super(code);
      this.code = code;
    }
  }

  const cleanPath = (p) => String(p || "").replace(/^\/+|\/+$/g, "");

  function isInboxFolder(f) {
    if (!f) return false;
    if (f.type === "inbox" || (f.specialUse || []).includes("inbox")) return true;
    return cleanPath(f.path).toLowerCase() === "inbox";
  }

  // Finds a folder strictly inside one account (never falls back to another account).
  function findFolderInAccount(folders, accountId, folderId, folderPath) {
    if (!accountId) return null;
    if (folderId) {
      const byId = folders.find((f) => f.id === folderId && f.accountId === accountId);
      if (byId) return byId;
    }
    const needle = cleanPath(folderPath).toLowerCase();
    if (!needle) return null;
    return folders.find((f) => f.accountId === accountId && cleanPath(f.path).toLowerCase() === needle) || null;
  }

  function requireFeature(cfg, name) {
    if (!cfg?.features?.[name]) throw new PolicyError(`feature_disabled:${name}`);
  }

  function triageAccountIds(cfg) {
    return new Set(cfg?.mailbox?.triageAccountIds || []);
  }

  function kanbanAccountIds(cfg) {
    const enabled = triageAccountIds(cfg);
    return new Set((cfg?.mailbox?.kanbanAccountIds || []).filter((id) => enabled.has(id)));
  }

  // Throws unless OmaThunder may change mails/folders/tags in this account.
  // A missing or malformed config counts as read-only.
  function requireWritable(cfg, accountId) {
    if (cfg?.mailbox?.readOnly !== false) throw new PolicyError("read_only");
    if (!accountId || !triageAccountIds(cfg).has(accountId)) throw new PolicyError("account_not_enabled");
  }

  // Throws unless `header` may be moved to `targetFolder` (same account, writable).
  function requireMovable(cfg, header, targetFolder) {
    const accountId = header?.folder?.accountId;
    requireWritable(cfg, accountId);
    if (!targetFolder || targetFolder.accountId !== accountId) throw new PolicyError("cross_account_move");
  }

  // Decides whether the mail found under a stored numeric ID is really the stored mail.
  // Numeric IDs are only valid within one Thunderbird session, so the Message-ID must match.
  function acceptDirectHit(ref, direct, strict) {
    if (!direct) return false;
    const hdrId = ref?.headerMessageId || "";
    if (hdrId) return direct.headerMessageId === hdrId;
    return !strict;
  }

  // Picks the stored mail among all mails sharing its Message-ID (copies in other folders/accounts).
  // strict: return null instead of guessing when the match is ambiguous.
  function pickMessageCandidate(ref, candidates, strict) {
    let list = candidates || [];
    if (ref?.folderId) {
      const inFolder = list.filter((m) => m.folder?.id === ref.folderId);
      if (inFolder.length === 1) return inFolder[0];
    }
    if (ref?.accountId) list = list.filter((m) => m.folder?.accountId === ref.accountId);
    if (list.length === 1) return list[0];
    return strict ? null : list[0] || null;
  }

  // Folders a Kanban triage scan may read: INBOX of kanban-enabled accounts plus rule folders (with subfolders).
  function selectScanFolders(cfg, folders) {
    const enabled = kanbanAccountIds(cfg);
    const scan = new Map();
    for (const f of folders) {
      if (enabled.has(f.accountId) && isInboxFolder(f)) scan.set(f.id, f);
    }
    for (const rule of cfg?.rules || []) {
      if (!rule.includeInScan) continue;
      const base = cleanPath(rule.folderPath).toLowerCase();
      if (!base) continue;
      for (const f of folders) {
        if (!enabled.has(f.accountId)) continue;
        if (rule.accountId && f.accountId !== rule.accountId) continue;
        const path = cleanPath(f.path).toLowerCase();
        if (path === base || path.startsWith(`${base}/`)) scan.set(f.id, f);
      }
    }
    return [...scan.values()];
  }

  // Newest `limit` mails received since `since`.
  function newestSince(messages, since, limit) {
    return messages
      .filter((m) => !m.date || new Date(m.date) >= since)
      .sort((a, b) => new Date(b.date || 0) - new Date(a.date || 0))
      .slice(0, limit);
  }

  // Adds tags without dropping the user's own. replacePrefix removes OmaThunder's old status tags.
  function mergeTags(existing, add, replacePrefix = "") {
    const kept = (existing || []).filter((t) => !(replacePrefix && t.startsWith(replacePrefix)));
    return [...new Set([...kept, ...add])];
  }

  function normalizeSubjectForThread(subj) {
    let s = String(subj || "").trim();
    let prev;
    do {
      prev = s;
      s = s.replace(/^(?:re|aw|fwd?|wg|antw)\s*:\s*/i, "");
    } while (s !== prev);
    return s;
  }

  // Extracts Message-IDs from References / In-Reply-To header values.
  function parseMessageIds(...values) {
    const ids = [];
    for (const v of values) {
      for (const m of String(v || "").matchAll(/<([^<>\s]+)>/g)) ids.push(m[1]);
    }
    return [...new Set(ids)];
  }

  function refsOf(msg) {
    const h = msg?.headers || {};
    return parseMessageIds(h.references || h.References, h["in-reply-to"] || h["In-Reply-To"]);
  }

  // Keeps only candidates that belong to the primary mail's conversation, linked through the
  // References / In-Reply-To headers. Mails that merely share the subject are dropped.
  function selectThread(primary, candidates, max = 8) {
    const idOf = (m) => m.threadId || "";
    const thread = new Set([idOf(primary), ...refsOf(primary)].filter(Boolean));
    const linked = new Map();
    let grew = true;
    while (grew) {
      grew = false;
      for (const c of candidates) {
        const id = idOf(c);
        if (!id || id === idOf(primary) || linked.has(id)) continue;
        const refs = refsOf(c);
        if (thread.has(id) || refs.some((r) => thread.has(r))) {
          linked.set(id, c);
          thread.add(id);
          refs.forEach((r) => thread.add(r));
          grew = true;
        }
      }
    }
    const byDate = (a, b) => new Date(a.date || 0) - new Date(b.date || 0);
    const others = [...linked.values()].sort(byDate).slice(-(max - 1));
    return [primary, ...others].sort(byDate);
  }

  // --- Compose drafts -------------------------------------------------------
  // A draft consists of the text the user wrote, followed by signature and/or quoted original.
  // The assistant only ever reads and replaces the user's own part.

  // Separator Thunderbird and other clients put above an inline-forwarded or quoted original.
  const FORWARD_LINE = /^\s*-{3,}\s*(original message|forwarded message|weitergeleitete nachricht|ursprüngliche nachricht|original-nachricht)\s*-*\s*$/i;

  function escapeHtml(str) {
    return String(str || "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
  }

  function textToHtml(text) {
    return String(text || "")
      .replace(/\r\n?/g, "\n")
      .trim()
      .split(/\n{2,}/)
      .map((para) => `<p>${escapeHtml(para).replace(/\n/g, "<br>")}</p>`)
      .join("");
  }

  function htmlToText(html) {
    return String(html || "")
      .replace(/<(style|script)[^>]*>[\s\S]*?<\/\1>/gi, "")
      .replace(/<br\s*\/?>/gi, "\n")
      .replace(/<\/(p|div|li|h[1-6])>/gi, "\n")
      .replace(/<[^>]+>/g, "")
      .replace(/&nbsp;/g, " ")
      .replace(/&lt;/g, "<")
      .replace(/&gt;/g, ">")
      .replace(/&quot;/g, '"')
      .replace(/&amp;/g, "&")
      .replace(/\n{3,}/g, "\n\n")
      .trim();
  }

  // Splits a draft body into head (HTML up to <body>), the user's own part, and the rest
  // (signature, quote, closing tags). head + own + rest is always the unchanged body.
  function splitDraft(body, isPlainText) {
    const text = String(body || "");
    if (isPlainText) {
      const lines = text.split("\n");
      let cut = lines.length;
      for (let i = 0; i < lines.length; i++) {
        if (lines[i] === "-- " || lines[i].startsWith(">") || FORWARD_LINE.test(lines[i])) {
          cut = i;
          break;
        }
      }
      // Pull the "On … wrote:" line above the quote into the quoted part.
      if (cut < lines.length && lines[cut].startsWith(">")) {
        let j = cut - 1;
        while (j >= 0 && lines[j].trim() === "") j--;
        if (j >= 0 && /:\s*$/.test(lines[j])) cut = j;
      }
      const own = lines.slice(0, cut).join("\n") + (cut > 0 && cut < lines.length ? "\n" : "");
      return { head: "", own, rest: lines.slice(cut).join("\n") };
    }
    const bodyOpen = text.match(/<body[^>]*>/i);
    const start = bodyOpen ? bodyOpen.index + bodyOpen[0].length : 0;
    const after = text.slice(start);
    const markers = [
      /<div[^>]*class="[^"]*moz-cite-prefix/i,
      /<blockquote[^>]*type="cite"/i,
      /<div[^>]*class="[^"]*moz-signature/i,
      /<pre[^>]*class="[^"]*moz-signature/i,
      /<div[^>]*class="[^"]*moz-forward-container/i,
      /<\/body>/i,
    ];
    let cut = after.length;
    for (const re of markers) {
      const m = after.match(re);
      if (m && m.index < cut) cut = m.index;
    }
    return { head: text.slice(0, start), own: after.slice(0, cut), rest: after.slice(cut) };
  }

  // The user's own text of a draft, as plain text.
  function draftOwnText(body, isPlainText) {
    const { own } = splitDraft(body, isPlainText);
    return isPlainText ? own.trim() : htmlToText(own);
  }

  // Returns the draft body with the user's own part replaced; signature and quote stay untouched.
  function replaceDraftOwn(body, isPlainText, newText) {
    const { head, rest } = splitDraft(body, isPlainText);
    if (isPlainText) {
      const text = String(newText || "").trim();
      return rest ? `${text}\n\n${rest}` : text;
    }
    // A body without <body> (editor not loaded yet) still needs a document around the text.
    if (!head && !/<\/body>/i.test(rest)) return `<html><body>${textToHtml(newText)}${rest}</body></html>`;
    return `${head}${textToHtml(newText)}${rest}`;
  }

  function isLightHex(hex) {
    const clean = String(hex || "").trim().replace(/^#/, "");
    if (!/^[0-9a-f]{6}$/i.test(clean)) return false;
    const r = parseInt(clean.slice(0, 2), 16);
    const g = parseInt(clean.slice(2, 4), 16);
    const b = parseInt(clean.slice(4, 6), 16);
    return (0.299 * r + 0.587 * g + 0.114 * b) / 255 > 0.55;
  }

  function buildThemeVars(p) {
    if (!p) return "";
    const contrast = isLightHex(p.accent) ? "#101218" : "#ffffff";
    return `:root {
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
  --oma-blue: ${p.blue || p.accent};
  color-scheme: ${p.isLight ? "light" : "dark"};
}`;
  }

  function suggestionConditions(sug) {
    const conds = [];
    for (const d of sug?.domains || []) {
      const clean = String(d || "").trim().replace(/^@+/, "").toLowerCase();
      if (clean) conds.push({ field: "from", op: "endsWith", value: `@${clean}` });
    }
    for (const f of sug?.fromContains || []) {
      const clean = String(f || "").trim();
      if (clean) conds.push({ field: "from", op: "contains", value: clean });
    }
    for (const s of sug?.subjectContains || []) {
      const clean = String(s || "").trim();
      if (clean) conds.push({ field: "subject", op: "contains", value: clean });
    }
    return conds;
  }

  function buildNativeFilterSpecFromSuggestion(sug, accountId) {
    const action = String(sug?.action || "create");
    const targetName = String(sug?.targetFilterName || "").trim();
    if (action === "fix_and_to_or" && targetName) {
      return {
        accountId: accountId || "",
        mode: "fix_match_all",
        existingName: targetName,
        matchAll: false,
        enable: true,
      };
    }
    const conditions = suggestionConditions(sug);
    if (action === "append_or" && targetName) {
      return {
        accountId: accountId || "",
        mode: "append_conditions",
        existingName: targetName,
        matchAll: false,
        conditions,
        enable: true,
      };
    }
    const actions = [];
    if (sug?.delete) {
      actions.push({ type: "delete" });
    } else if (sug?.suggestedFolder) {
      actions.push({ type: "moveToFolder", targetFolderPath: cleanPath(sug.suggestedFolder) });
    }
    if (sug?.markRead) {
      actions.push({ type: "markRead" });
    }
    return {
      accountId: accountId || "",
      mode: action === "replace" && targetName ? "replace" : "create",
      existingName: targetName,
      name: String(sug?.name || targetName || "OmaThunder Filter").trim(),
      enabled: true,
      matchAll: Boolean(sug?.matchAll),
      conditions,
      actions,
      createFolder: true,
    };
  }

  function buildNativeFilterSpecFromRule(rule) {
    const conditions = suggestionConditions(rule);
    const existingName = Array.isArray(rule?.nativeFilterNames) && rule.nativeFilterNames[0]
      ? rule.nativeFilterNames[0]
      : `OmaThunder: ${rule?.space || "Space"}`;
    const folder = cleanPath(rule?.folderPath);
    const actions = folder ? [{ type: "moveToFolder", targetFolderPath: folder }] : [];
    const hasDomains = (rule?.domains || []).length > 0;
    const hasSubject = (rule?.subjectContains || []).length > 0;
    const hasFrom = (rule?.fromContains || []).length > 0;
    // Domain-only rules must be OR so any of the space's domains triggers the move.
    const matchAll = hasSubject && (hasDomains || hasFrom);
    return {
      accountId: rule?.accountId || "",
      mode: Array.isArray(rule?.nativeFilterNames) && rule.nativeFilterNames[0] ? "replace" : "create",
      existingName,
      name: existingName,
      enabled: true,
      matchAll,
      conditions,
      actions,
      createFolder: true,
    };
  }

  function isLocalAccount(acc) {
    return String(acc?.type || "").toLowerCase() === "none";
  }

  function findLocalAccount(accounts) {
    return (accounts || []).find(isLocalAccount) || null;
  }

  function sanitizeArchivePart(raw, fallback = "mail") {
    const s = String(raw || "")
      .replace(/[\\/:*?"<>|\u0000-\u001f]/g, "_")
      .replace(/^[.\s]+|[.\s]+$/g, "")
      .replace(/\s{2,}/g, " ")
      .replace(/_{2,}/g, "_");
    if (!s || s === "." || s === "..") return fallback;
    return s.slice(0, 64);
  }

  function buildArchiveRelativeParts(accountName, folderPath, dateValue) {
    const accSeg = sanitizeArchivePart(accountName, "account");
    const rawParts = String(folderPath || "").replace(/\\/g, "/").split("/").filter((p) => p && p !== "." && p !== "..");
    const folderSegs = [];
    for (const p of rawParts) {
      const seg = sanitizeArchivePart(p, "");
      if (!seg) continue;
      if (folderSegs.length === 0 && seg.toLowerCase() === "inbox") folderSegs.push("Incoming");
      else folderSegs.push(seg);
    }
    if (folderSegs.length === 0) folderSegs.push("Incoming");
    const dt = dateValue ? new Date(dateValue) : new Date();
    const valid = !Number.isNaN(dt.getTime()) ? dt : new Date();
    const yyyy = String(valid.getUTCFullYear());
    const mm = String(valid.getUTCMonth() + 1).padStart(2, "0");
    return [accSeg, ...folderSegs, yyyy, mm];
  }

  // Cross-account move guard for Archive: only allows moving from an enabled, writable account
  // into a Thunderbird Local Folders account (type === "none"), never into another IMAP account.
  function requireLocalArchiveMove(cfg, header, targetFolder, accounts) {
    const accountId = header?.folder?.accountId;
    requireWritable(cfg, accountId);
    if (!targetFolder || !targetFolder.id) throw new PolicyError("target_folder_missing");
    const targetAcc = (accounts || []).find((a) => a.id === targetFolder.accountId);
    if (!isLocalAccount(targetAcc)) throw new PolicyError("local_folder_required");
  }

  function requireArchiveDelete(cfg, header) {
    requireWritable(cfg, header?.folder?.accountId);
  }

  function matchArchiveCandidate(header, criteria = {}, nowMs = Date.now()) {
    if (!header) return false;
    const days = Number(criteria.olderThanDays || 0);
    if (days > 0) {
      const msgTime = header.date ? new Date(header.date).getTime() : NaN;
      if (Number.isNaN(msgTime)) return false;
      const cutoff = nowMs - days * 86400000;
      if (msgTime > cutoff) return false;
    }
    if (criteria.onlyRead && !header.read) return false;
    if (criteria.excludeStarred && header.flagged) return false;
    return true;
  }

  const api = {
    textToHtml, htmlToText, splitDraft, draftOwnText, replaceDraftOwn, isLightHex, buildThemeVars,
    PolicyError, cleanPath, isInboxFolder, findFolderInAccount, requireFeature, triageAccountIds, kanbanAccountIds,
    requireWritable, requireMovable, acceptDirectHit, pickMessageCandidate, selectScanFolders,
    newestSince, mergeTags, normalizeSubjectForThread, parseMessageIds, selectThread,
    buildNativeFilterSpecFromSuggestion, buildNativeFilterSpecFromRule,
    isLocalAccount, findLocalAccount, buildArchiveRelativeParts, requireLocalArchiveMove,
    requireArchiveDelete, matchArchiveCandidate,
  };
  root.OmaCore = api;
  if (typeof module !== "undefined" && module.exports) module.exports = api;
})(typeof globalThis !== "undefined" ? globalThis : this);
