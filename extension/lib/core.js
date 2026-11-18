/* OmaThunder core: pure decision logic without Thunderbird API calls, so it can be unit-tested.
 * Loaded before background.js; exposes globalThis.OmaCore. */
(function (root) {
  // `code` is a stable machine-readable reason (e.g. "read_only"); `detail` is optional context.
  // The message is "code" or "code: detail", so callers that only look at the message still see the code first.
  class PolicyError extends Error {
    constructor(code, detail = "") {
      super(detail ? `${code}: ${detail}` : code);
      this.code = code;
      this.detail = detail ? String(detail) : "";
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

  // Which folder change a bridge messages.actions request asks for: "trash", "archive", "move" or
  // "" for none. At most one per request, so a mail never lands somewhere the caller did not mean.
  // "archive" uses Thunderbird's own archive (per-identity folder and granularity settings).
  function messageMoveKind(params) {
    const p = params || {};
    for (const key of ["trash", "archive"]) {
      if (p[key] !== undefined && typeof p[key] !== "boolean") throw new PolicyError("invalid_argument", `${key} must be a boolean`);
    }
    const kinds = [];
    if (p.trash === true) kinds.push("trash");
    if (p.archive === true) kinds.push("archive");
    if (p.moveToFolderId || p.moveToPath) kinds.push("move");
    if (kinds.length > 1) throw new PolicyError("invalid_argument", "use only one of trash, archive or moveToFolderId/moveToPath");
    return kinds[0] || "";
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

  function folderMatchesSubtree(folderPath, rootPath) {
    const p = cleanPath(folderPath).toLowerCase();
    const r = cleanPath(rootPath).toLowerCase();
    return Boolean(r) && (p === r || p.startsWith(`${r}/`));
  }

  function findAccountInbox(folders, accountId) {
    if (!accountId) return null;
    return (folders || []).find((f) => f.accountId === accountId && isInboxFolder(f)) || null;
  }

  // Returns direct level-1 subfolders under the account's INBOX (e.g. INBOX/atlassian, INBOX/avono).
  function listInboxLevel1Folders(folders, accountId) {
    const inbox = findAccountInbox(folders, accountId);
    if (!inbox) return [];
    const inboxClean = cleanPath(inbox.path);
    const inboxLower = inboxClean.toLowerCase();
    if (!inboxLower) return [];
    const prefix = `${inboxLower}/`;
    const inboxSegCount = inboxClean.split("/").filter(Boolean).length;
    return (folders || [])
      .filter((f) => {
        if (f.accountId !== accountId || isInboxFolder(f)) return false;
        const p = cleanPath(f.path);
        const lower = p.toLowerCase();
        if (!lower.startsWith(prefix)) return false;
        const segs = p.split("/").filter(Boolean);
        return segs.length === inboxSegCount + 1;
      })
      .sort((a, b) => String(a.name || a.path).localeCompare(String(b.name || b.path), undefined, { sensitivity: "base" }));
  }

  // Returns the level-1 folder display name when a mail lives inside INBOX/<level1>/...
  function level1FolderName(folderPath) {
    const clean = cleanPath(folderPath);
    if (!clean) return "";
    const parts = clean.split("/").filter(Boolean);
    if (parts.length >= 2 && parts[0].toLowerCase() === "inbox") return parts[1];
    return "";
  }

  function relativeFolderLabel(folderPath) {
    const clean = cleanPath(folderPath);
    if (!clean) return "";
    const parts = clean.split("/").filter(Boolean);
    if (parts.length >= 2 && parts[0].toLowerCase() === "inbox") return parts.slice(1).join("/");
    return clean;
  }

  // Folders a Kanban triage scan may read:
  // - Per-account kanbanFolders (INBOX matches only itself; level-1 folders match recursively),
  //   or INBOX only when kanbanFolders has no entry for that account yet.
  // - Plus rule folders with includeInScan (with subfolders), strictly within kanban-enabled accounts.
  function selectScanFolders(cfg, folders) {
    const enabled = kanbanAccountIds(cfg);
    const scan = new Map();
    const kanbanFoldersMap = cfg?.mailbox?.kanbanFolders;
    const hasConfiguredMap = kanbanFoldersMap && typeof kanbanFoldersMap === "object";

    for (const accId of enabled) {
      const configured = hasConfiguredMap && Object.prototype.hasOwnProperty.call(kanbanFoldersMap, accId)
        ? kanbanFoldersMap[accId]
        : null;
      const accFolders = (folders || []).filter((f) => f.accountId === accId);
      if (Array.isArray(configured)) {
        const inbox = findAccountInbox(accFolders, accId);
        const inboxLower = inbox ? cleanPath(inbox.path).toLowerCase() : "inbox";
        for (const rawTarget of configured) {
          const target = cleanPath(rawTarget).toLowerCase();
          if (!target) continue;
          if (target === "inbox" || target === inboxLower) {
            if (inbox) scan.set(inbox.id, inbox);
            continue;
          }
          for (const f of accFolders) {
            if (folderMatchesSubtree(f.path, target)) scan.set(f.id, f);
          }
        }
      } else {
        for (const f of accFolders) {
          if (isInboxFolder(f)) scan.set(f.id, f);
        }
      }
    }
    for (const rule of cfg?.rules || []) {
      if (!rule.includeInScan) continue;
      const base = cleanPath(rule.folderPath).toLowerCase();
      if (!base) continue;
      for (const f of folders) {
        if (!enabled.has(f.accountId)) continue;
        if (rule.accountId && f.accountId !== rule.accountId) continue;
        if (folderMatchesSubtree(f.path, base)) scan.set(f.id, f);
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

  // Selects ALL unread mails (newest-first, ignoring limit/since) plus up to `limit` newest read mails within `since`.
  function selectScanMessages(messages, since, limit) {
    const byDateDesc = (a, b) => new Date(b.date || 0) - new Date(a.date || 0);
    const seen = new Set();
    const deduped = [];
    for (const m of messages || []) {
      if (!m) continue;
      const key = m.id !== undefined && m.id !== null ? String(m.id) : "";
      if (key && seen.has(key)) continue;
      if (key) seen.add(key);
      deduped.push(m);
    }
    const unread = deduped.filter((m) => !m.read).sort(byDateDesc);
    const read = deduped
      .filter((m) => Boolean(m.read) && (!m.date || new Date(m.date) >= since))
      .sort(byDateDesc)
      .slice(0, Math.max(0, Number(limit) || 0));
    return [...unread, ...read];
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

  function escapeMdHtml(s) {
    return String(s ?? "")
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#39;");
  }

  function safeMdUrl(raw) {
    const u = String(raw || "").trim();
    if (/^(https?:|mailto:)/i.test(u)) return u;
    return "";
  }

  function renderMdInline(raw) {
    // Protect inline code spans first so *, _, ** inside `code` aren't parsed as emphasis.
    const codeTokens = [];
    let str = String(raw ?? "").replace(/`([^`\n]+)`/g, (_, code) => {
      const idx = codeTokens.length;
      codeTokens.push(`<code>${escapeMdHtml(code)}</code>`);
      return `\u0000CODE${idx}\u0000`;
    });

    // Protect markdown links [label](url) before escaping.
    const linkTokens = [];
    str = str.replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, (_, label, url) => {
      const safe = safeMdUrl(url);
      const idx = linkTokens.length;
      if (safe) {
        linkTokens.push(`<a href="${escapeMdHtml(safe)}" target="_blank" rel="noopener noreferrer">${escapeMdHtml(label)}</a>`);
      } else {
        linkTokens.push(escapeMdHtml(label));
      }
      return `\u0000LINK${idx}\u0000`;
    });

    str = escapeMdHtml(str);

    str = str
      .replace(/\*\*\*([^*\n]+)\*\*\*/g, "<strong><em>$1</em></strong>")
      .replace(/\*\*([^*\n]+)\*\*/g, "<strong>$1</strong>")
      .replace(/__([^_\n]+)__/g, "<strong>$1</strong>")
      .replace(/~~([^~\n]+)~~/g, "<del>$1</del>")
      .replace(/(^|[^*\w])\*([^*\n]+)\*(?!\*)/g, "$1<em>$2</em>")
      .replace(/(^|[^\w])_([^_\n]+)_(?=[^\w]|$)/g, "$1<em>$2</em>");

    str = str
      .replace(/\u0000LINK(\d+)\u0000/g, (_, i) => linkTokens[Number(i)] || "")
      .replace(/\u0000CODE(\d+)\u0000/g, (_, i) => codeTokens[Number(i)] || "");

    return str;
  }

  function isTableRow(line) {
    return /^\s*\|.*\|\s*$/.test(line);
  }

  function isTableSeparator(line) {
    return /^\s*\|(?:\s*:?-+:?\s*\|)+\s*$/.test(line);
  }

  function parseTableCells(line) {
    return line.trim().replace(/^\|/, "").replace(/\|$/, "").split("|").map((c) => c.trim());
  }

  function renderMarkdownHtml(markdown) {
    const src = String(markdown ?? "").replace(/\r\n?/g, "\n").trim();
    if (!src) return "";

    // Extract fenced code blocks ```lang ... ``` before block parsing.
    const fenced = [];
    const prepared = src.replace(/```([a-zA-Z0-9_-]*)\n([\s\S]*?)```/g, (_, lang, body) => {
      const idx = fenced.length;
      const cls = lang ? ` class="language-${escapeMdHtml(lang)}"` : "";
      fenced.push(`<pre><code${cls}>${escapeMdHtml(body.replace(/\n$/, ""))}</code></pre>`);
      return `\n\u0000FENCE${idx}\u0000\n`;
    });

    const lines = prepared.split("\n");
    const out = [];
    let i = 0;

    while (i < lines.length) {
      const line = lines[i];
      if (!line.trim()) {
        i++;
        continue;
      }

      const fenceMatch = line.trim().match(/^\u0000FENCE(\d+)\u0000$/);
      if (fenceMatch) {
        out.push(fenced[Number(fenceMatch[1])] || "");
        i++;
        continue;
      }

      const hMatch = line.match(/^\s*(#{1,4})\s+(.+?)\s*#*$/);
      if (hMatch) {
        const level = hMatch[1].length;
        out.push(`<h${level}>${renderMdInline(hMatch[2])}</h${level}>`);
        i++;
        continue;
      }

      if (/^\s*(?:-{3,}|\*{3,}|_{3,})\s*$/.test(line)) {
        out.push("<hr />");
        i++;
        continue;
      }

      if (/^\s*>\s?/.test(line)) {
        const qLines = [];
        while (i < lines.length && /^\s*>\s?/.test(lines[i])) {
          qLines.push(lines[i].replace(/^\s*>\s?/, ""));
          i++;
        }
        out.push(`<blockquote><p>${qLines.map(renderMdInline).join("<br />")}</p></blockquote>`);
        continue;
      }

      if (i + 1 < lines.length && isTableRow(line) && isTableSeparator(lines[i + 1])) {
        const headers = parseTableCells(line);
        i += 2;
        const rows = [];
        while (i < lines.length && isTableRow(lines[i])) {
          rows.push(parseTableCells(lines[i]));
          i++;
        }
        const thead = `<thead><tr>${headers.map((h) => `<th>${renderMdInline(h)}</th>`).join("")}</tr></thead>`;
        const tbody = rows.length
          ? `<tbody>${rows.map((r) => `<tr>${headers.map((_, idx) => `<td>${renderMdInline(r[idx] || "")}</td>`).join("")}</tr>`).join("")}</tbody>`
          : "";
        out.push(`<table>${thead}${tbody}</table>`);
        continue;
      }

      const ulMatch = line.match(/^(\s*)[-*+]\s+(.*)$/);
      const olMatch = line.match(/^(\s*)\d+\.\s+(.*)$/);
      if (ulMatch || olMatch) {
        const tag = ulMatch ? "ul" : "ol";
        const itemRe = ulMatch ? /^(\s*)[-*+]\s+(.*)$/ : /^(\s*)\d+\.\s+(.*)$/;
        const items = [];
        while (i < lines.length) {
          const m = lines[i].match(itemRe);
          if (m) {
            items.push({ indent: m[1].length, text: m[2] });
            i++;
          } else if (/^\s{2,}\S/.test(lines[i]) && items.length > 0 && !/^\s*[-*+]\s+/.test(lines[i]) && !/^\s*\d+\.\s+/.test(lines[i])) {
            items[items.length - 1].text += " " + lines[i].trim();
            i++;
          } else {
            break;
          }
        }
        const minIndent = items.reduce((min, it) => Math.min(min, it.indent), items[0].indent);
        const lis = [];
        for (let k = 0; k < items.length; k++) {
          const it = items[k];
          if (it.indent > minIndent && lis.length > 0) {
            const nested = [];
            while (k < items.length && items[k].indent > minIndent) {
              nested.push(`<li>${renderMdInline(items[k].text)}</li>`);
              k++;
            }
            k--;
            lis[lis.length - 1] = lis[lis.length - 1].replace(/<\/li>$/, `<ul>${nested.join("")}</ul></li>`);
          } else {
            lis.push(`<li>${renderMdInline(it.text)}</li>`);
          }
        }
        out.push(`<${tag}>${lis.join("")}</${tag}>`);
        continue;
      }

      const pLines = [];
      while (
        i < lines.length &&
        lines[i].trim() &&
        !/^\s*(?:#{1,4}\s+|>\s?|(?:[-*+]\s+)|(?:\d+\.\s+)|(?:-{3,}|\*{3,}|_{3,})\s*$|\u0000FENCE\d+\u0000$)/.test(lines[i]) &&
        !(i + 1 < lines.length && isTableRow(lines[i]) && isTableSeparator(lines[i + 1]))
      ) {
        pLines.push(lines[i]);
        i++;
      }
      out.push(`<p>${pLines.map(renderMdInline).join("<br />")}</p>`);
    }

    return out.join("\n");
  }

  function isOpenKanbanCard(c) {
    return Boolean(c) && (c.kanbanColumn || "incoming") !== "done";
  }

  function countOpenKanban(items, accountId = "") {
    let n = 0;
    for (const c of items || []) {
      if (!isOpenKanbanCard(c)) continue;
      if (accountId && c.accountId !== accountId) continue;
      n++;
    }
    return n;
  }

  function toggleCardSelection(selectedSet, cardId) {
    const next = new Set(selectedSet || []);
    if (!cardId) return next;
    if (next.has(cardId)) next.delete(cardId);
    else next.add(cardId);
    return next;
  }

  function selectCardRange(selectedSet, orderedIds, anchorId, targetId, { additive = true } = {}) {
    const next = additive ? new Set(selectedSet || []) : new Set();
    const list = Array.isArray(orderedIds) ? orderedIds : [];
    const targetIdx = list.indexOf(targetId);
    const anchorIdx = anchorId ? list.indexOf(anchorId) : -1;
    if (targetIdx === -1) return next;
    if (anchorIdx === -1) {
      next.add(targetId);
      return next;
    }
    const start = Math.min(anchorIdx, targetIdx);
    const end = Math.max(anchorIdx, targetIdx);
    for (let i = start; i <= end; i++) {
      if (list[i]) next.add(list[i]);
    }
    return next;
  }

  function toggleColumnSelection(selectedSet, colIds) {
    const next = new Set(selectedSet || []);
    const ids = (colIds || []).filter(Boolean);
    if (ids.length === 0) return next;
    const allSelected = ids.every((id) => next.has(id));
    if (allSelected) {
      for (const id of ids) next.delete(id);
    } else {
      for (const id of ids) next.add(id);
    }
    return next;
  }

  function resolveDraggedCardIds(draggedId, selectedSet, visibleIds) {
    if (!draggedId) return [];
    const sel = selectedSet instanceof Set ? selectedSet : new Set(selectedSet || []);
    if (sel.has(draggedId) && sel.size > 0) {
      const visible = new Set(visibleIds || []);
      const picked = [...sel].filter((id) => visible.size === 0 || visible.has(id));
      if (picked.length > 0) return picked;
    }
    return [draggedId];
  }

  // --- Local bridge (REST/SSE for the native OmaThunder app) -------------------
  // The host forwards bridge calls as reverse-RPC (tb_rpc). Every call is checked centrally against
  // the host config (cfg.bridge + cfg.mailbox) before a handler runs; handlers add the per-object
  // checks (account of the message, of the identity, of the target folder).
  // A missing or malformed config means: bridge off, nothing readable, nothing writable.

  // Error codes the Go bridge maps to HTTP statuses (see CONTRACT §2).
  const BRIDGE_ERROR_CODES = new Set([
    "read_only", "account_not_enabled", "account_not_readable", "send_disabled", "cross_account_move",
    "not_found", "invalid_argument", "ambiguous", "message_changed", "conflict",
  ]);

  function isBridgeErrorCode(code) {
    const c = String(code || "");
    return BRIDGE_ERROR_CODES.has(c) || /^feature_disabled:[a-z_]+$/.test(c);
  }

  // Normalized view of cfg.bridge. accountIds === null means "all accounts readable".
  function bridgeSettings(cfg) {
    const b = cfg && typeof cfg === "object" && cfg.bridge && typeof cfg.bridge === "object" ? cfg.bridge : null;
    if (!b) return { enabled: false, allowSend: false, accountIds: [] };
    let accountIds = null;
    if (Array.isArray(b.accountIds)) {
      const ids = b.accountIds.filter((id) => typeof id === "string" && id.trim()).map((id) => id.trim());
      accountIds = ids.length > 0 ? ids : null;
    } else if (b.accountIds !== undefined && b.accountIds !== null) {
      accountIds = []; // malformed restriction: nothing is readable
    }
    return { enabled: b.enabled === true, allowSend: b.allowSend === true, accountIds };
  }

  function isBridgeReadable(cfg, accountId) {
    const { accountIds } = bridgeSettings(cfg);
    if (accountIds === null) return true;
    return Boolean(accountId) && accountIds.includes(String(accountId));
  }

  function requireBridgeEnabled(cfg) {
    if (!bridgeSettings(cfg).enabled) throw new PolicyError("feature_disabled:bridge");
  }

  function requireBridgeReadable(cfg, accountId) {
    if (!isBridgeReadable(cfg, accountId)) throw new PolicyError("account_not_readable", accountId ? String(accountId) : "");
  }

  // Mail/filter writes via the bridge: mailbox writable for the account AND the account readable for the bridge.
  function requireBridgeWritable(cfg, accountId) {
    requireWritable(cfg, accountId);
    requireBridgeReadable(cfg, accountId);
  }

  function requireBridgeMovable(cfg, header, targetFolder) {
    requireBridgeWritable(cfg, header?.folder?.accountId);
    requireMovable(cfg, header, targetFolder);
  }

  function requireBridgeSend(cfg, accountId) {
    requireBridgeWritable(cfg, accountId);
    if (cfg?.bridge?.allowSend !== true) throw new PolicyError("send_disabled");
  }

  // Calendar and address book writes are not account-scoped: they only need mailbox.readOnly=false.
  function requireBridgeGlobalWrite(cfg) {
    if (cfg?.mailbox?.readOnly !== false) throw new PolicyError("read_only");
  }

  // One entry per reverse-RPC method. kind: read | write | send | filter | calendar | contacts | ui | meta.
  //   accountParam: params.accountId (when given) must be bridge-readable before the handler runs.
  //   message:      the handler resolves a mail and checks its account.
  //   identity:     the handler resolves the compose identity and checks the account owning it.
  //   write:        calendar/contacts/filter calls that change data.
  //   uiParam:      boolean param that turns a write into a user-visible UI action (compose window).
  const BRIDGE_RPC_POLICY = Object.freeze({
    ping: { kind: "meta" },
    "accounts.list": { kind: "read" },
    "folders.list": { kind: "read", accountParam: true },
    "messages.list": { kind: "read", accountParam: true },
    "messages.get": { kind: "read", message: true },
    "messages.thread": { kind: "read", message: true },
    "messages.actions": { kind: "write", message: true },
    "compose.draft": { kind: "write", identity: true, uiParam: "openWindow" },
    "compose.send": { kind: "send", identity: true },
    "calendar.list": { kind: "calendar" },
    "calendar.events": { kind: "calendar" },
    "calendar.create": { kind: "calendar", write: true },
    "calendar.update": { kind: "calendar", write: true },
    "calendar.delete": { kind: "calendar", write: true },
    "contacts.search": { kind: "contacts" },
    "contacts.upsert": { kind: "contacts", write: true },
    "filters.list": { kind: "read", accountParam: true },
    "filters.upsert": { kind: "filter", accountParam: true, write: true },
    "filters.run": { kind: "filter", accountParam: true, write: true },
    "filters.openDialog": { kind: "ui", accountParam: true },
  });

  const BRIDGE_FILTER_MODES = new Set(["create", "replace", "toggle", "fix_match_all", "append_conditions"]);
  const BRIDGE_FILTER_ACTIONS = new Set(["moveToFolder", "copyToFolder", "markRead", "stopExecution"]);

  function hasNonEmptyCondition(conditions) {
    return (Array.isArray(conditions) ? conditions : []).some((c) => c && String(c.value ?? "").trim());
  }

  // Validates a filters.upsert spec coming from the bridge and returns the spec to pass to omaFilters:
  // delete actions are refused, target folders must stay in the filter's account.
  function sanitizeBridgeFilterSpec(spec) {
    const s = spec && typeof spec === "object" ? spec : {};
    const accountId = String(s.accountId || "").trim();
    if (!accountId) throw new PolicyError("invalid_argument", "accountId is required");
    const mode = String(s.mode || "create");
    if (!BRIDGE_FILTER_MODES.has(mode)) throw new PolicyError("invalid_argument", `unknown filter mode ${mode}`);
    const actions = Array.isArray(s.actions) ? s.actions : [];
    if (mode === "create" || mode === "replace") {
      if (!hasNonEmptyCondition(s.conditions)) throw new PolicyError("invalid_argument", "at least one non-empty condition is required");
      if (actions.length === 0) throw new PolicyError("invalid_argument", "at least one action is required");
      if (mode === "replace" && !String(s.existingName || "").trim()) throw new PolicyError("invalid_argument", "existingName is required for replace");
    }
    for (const a of actions) {
      const type = String(a?.type || "");
      if (type === "delete" || type === "deleteFromServer") throw new PolicyError("invalid_argument", "delete actions are not allowed via the bridge");
      if (!BRIDGE_FILTER_ACTIONS.has(type)) throw new PolicyError("invalid_argument", `unsupported filter action ${type || "(empty)"}`);
      if ((type === "moveToFolder" || type === "copyToFolder") && !a.targetFolderUri && !a.targetFolderPath) {
        throw new PolicyError("invalid_argument", "target folder missing for filter action");
      }
    }
    return { ...s, accountId, mode, allowDelete: false, sameAccountOnly: true };
  }

  // Central pre-check of one reverse-RPC call. Returns the policy entry; throws PolicyError.
  function checkBridgeRPC(cfg, method, params = {}) {
    const policy = Object.prototype.hasOwnProperty.call(BRIDGE_RPC_POLICY, method) ? BRIDGE_RPC_POLICY[method] : null;
    if (!policy) throw new PolicyError("invalid_argument", `unknown method ${String(method || "")}`);
    if (policy.kind === "meta") return policy;
    requireBridgeEnabled(cfg);
    const p = params && typeof params === "object" ? params : {};
    const accountId = typeof p.accountId === "string" ? p.accountId.trim() : "";
    if (policy.accountParam && accountId) requireBridgeReadable(cfg, accountId);
    switch (policy.kind) {
      case "write":
        if (policy.uiParam && p[policy.uiParam] === true) break; // user-visible window only
        requireBridgeGlobalWrite(cfg);
        break;
      case "send":
        requireBridgeGlobalWrite(cfg);
        if (cfg?.bridge?.allowSend !== true) throw new PolicyError("send_disabled");
        break;
      case "filter":
        if (!accountId) throw new PolicyError("invalid_argument", "accountId is required");
        requireBridgeWritable(cfg, accountId);
        break;
      case "calendar":
      case "contacts":
        if (policy.write) requireBridgeGlobalWrite(cfg);
        break;
      default:
        break;
    }
    return policy;
  }

  // Keeps only items whose account the bridge may read.
  function filterBridgeReadable(cfg, items, accountOf = (x) => x?.accountId) {
    return (Array.isArray(items) ? items : []).filter((item) => isBridgeReadable(cfg, accountOf(item)));
  }

  // Converts any error to the "code: detail" string the Go bridge expects (CONTRACT §2).
  const LEGACY_POLICY_CODES = {
    message_not_found: ["not_found", "message"],
    target_folder_missing: ["not_found", "target folder"],
    contact_not_found: ["not_found", "contact"],
    target_addressbook_missing: ["not_found", "no writable address book"],
    local_folder_required: ["cross_account_move", "target must be a local folder"],
    message_moved_meanwhile: ["conflict", "message was moved meanwhile"],
  };

  function bridgeErrorString(err) {
    const code = err && typeof err === "object" && err.code ? String(err.code) : "";
    const msg = String((err && typeof err === "object" ? err.message : err) || "").trim();
    if (code && LEGACY_POLICY_CODES[code]) {
      const [mapped, detail] = LEGACY_POLICY_CODES[code];
      return `${mapped}: ${err.detail || detail}`;
    }
    if (code && isBridgeErrorCode(code)) {
      if (msg === code || msg.startsWith(`${code}: `)) return msg;
      return err.detail ? `${code}: ${err.detail}` : code;
    }
    const prefix = msg.match(/^([a-z_]+(?::[a-z_]+)?)(?::\s|$)/);
    if (prefix && isBridgeErrorCode(prefix[1])) return msg;
    if (prefix && LEGACY_POLICY_CODES[prefix[1]]) {
      const [mapped, detail] = LEGACY_POLICY_CODES[prefix[1]];
      return `${mapped}: ${detail}`;
    }
    if (code) return msg && msg !== code ? `${code}: ${msg}` : code;
    return msg || "internal_error";
  }

  // --- Bridge message resolution (CONTRACT §4) ---
  function normalizeHeaderMessageId(v) {
    return String(v ?? "").trim().replace(/^<+|>+$/g, "");
  }

  // `id` is a numeric Thunderbird message ID or an RFC Message-ID (with or without <>).
  function parseBridgeMessageRef(params = {}, idKey = "id") {
    const raw = String(params?.[idKey] ?? "").trim();
    if (!raw) throw new PolicyError("invalid_argument", "message id is required");
    const numericId = /^\d+$/.test(raw) ? Number(raw) : 0;
    const expectedMessageId = normalizeHeaderMessageId(params?.expectedMessageId);
    return {
      numericId,
      headerMessageId: numericId ? expectedMessageId : normalizeHeaderMessageId(raw),
      expectedMessageId,
      folderId: String(params?.folderId || "").trim(),
      accountId: String(params?.accountId || "").trim(),
    };
  }

  // Decides which live mail a bridge call addresses.
  //   direct:     messages.get(numericId) result (or null)
  //   candidates: all mails carrying ref.headerMessageId (copies in other folders/accounts)
  // Throws not_found / message_changed / ambiguous / account_not_readable.
  function pickBridgeMessage(ref, direct, candidates, cfg) {
    if (ref?.numericId && direct) {
      // A numeric ID is only trusted as far as the caller lets us verify it.
      if (!acceptDirectHit({ headerMessageId: ref.expectedMessageId }, direct, false)) {
        throw new PolicyError("message_changed", `message ${ref.numericId} now has a different Message-ID`);
      }
      requireBridgeReadable(cfg, direct.folder?.accountId);
      return direct;
    }
    if (ref?.numericId && !ref.expectedMessageId) throw new PolicyError("not_found", `message ${ref.numericId}`);
    const readable = filterBridgeReadable(cfg, candidates, (m) => m?.folder?.accountId);
    const hit = pickMessageCandidate(ref, readable, true);
    if (hit) return hit;
    const scoped = ref?.accountId ? readable.filter((m) => m.folder?.accountId === ref.accountId) : readable;
    if (scoped.length === 0) {
      if ((candidates || []).length > 0 && readable.length === 0) throw new PolicyError("account_not_readable");
      throw new PolicyError("not_found", `message ${ref?.headerMessageId || ref?.numericId || ""}`);
    }
    throw new PolicyError("ambiguous", `${scoped.length} copies of this Message-ID; pass folderId`);
  }

  // --- Compose identity (CONTRACT §5) ---
  // Returns the identity that will actually be used and the account owning it:
  // identityId, else the default (first) identity of accountId, else of the first preferred/listed account.
  function pickComposeIdentity(accounts, { identityId = "", accountId = "", preferredAccountIds = [] } = {}) {
    const list = Array.isArray(accounts) ? accounts : [];
    const wantIdentity = String(identityId || "").trim();
    if (wantIdentity) {
      for (const acc of list) {
        if ((acc?.identities || []).some((idn) => idn?.id === wantIdentity)) return { identityId: wantIdentity, accountId: acc.id };
      }
      throw new PolicyError("not_found", `identity ${wantIdentity}`);
    }
    const wantAccount = String(accountId || "").trim();
    if (wantAccount) {
      const acc = list.find((a) => a?.id === wantAccount);
      if (!acc) throw new PolicyError("not_found", `account ${wantAccount}`);
      const idn = (acc.identities || [])[0];
      if (!idn?.id) throw new PolicyError("invalid_argument", `account ${wantAccount} has no identity`);
      return { identityId: idn.id, accountId: acc.id };
    }
    for (const pref of preferredAccountIds || []) {
      const acc = list.find((a) => a?.id === pref && (a.identities || [])[0]?.id);
      if (acc) return { identityId: acc.identities[0].id, accountId: acc.id };
    }
    const first = list.find((a) => (a?.identities || [])[0]?.id);
    if (!first) throw new PolicyError("not_found", "no account with a sending identity");
    return { identityId: first.identities[0].id, accountId: first.id };
  }

  // --- Idempotency (CONTRACT §5) ---
  // Remembers the outcome of compose calls per idempotencyKey; a repeated key gets the first outcome
  // (or waits for the call still running). Failures are forgotten so the caller may retry.
  function createIdempotencyCache({ ttlMs = 15 * 60 * 1000, maxEntries = 500, now = () => Date.now() } = {}) {
    const entries = new Map();
    function prune() {
      const t = now();
      for (const [k, e] of entries) {
        if (e.done && t - e.at > ttlMs) entries.delete(k);
      }
      while (entries.size > maxEntries) entries.delete(entries.keys().next().value);
    }
    return {
      run(scope, key, fn) {
        const k = String(key ?? "").trim();
        if (!k) return Promise.resolve().then(fn);
        if (k.length > 200) return Promise.reject(new PolicyError("invalid_argument", "idempotencyKey too long"));
        prune();
        const cacheKey = `${scope}\u0000${k}`;
        const hit = entries.get(cacheKey);
        if (hit) return hit.promise.then((res) => (res && typeof res === "object" ? { ...res, replayed: true } : res));
        const entry = { done: false, at: now(), promise: null };
        entry.promise = Promise.resolve()
          .then(fn)
          .then(
            (res) => {
              entry.done = true;
              entry.at = now();
              return res;
            },
            (err) => {
              entries.delete(cacheKey);
              throw err;
            },
          );
        entries.set(cacheKey, entry);
        return entry.promise;
      },
      size: () => entries.size,
    };
  }

  // --- Bridge events (CONTRACT §3) ---
  const BRIDGE_EVENT_ID_CAP = 500;
  const MAIL_NEW_MAX_MESSAGES = 20;

  function bridgeMailSummary(h) {
    return {
      id: String(h?.id ?? ""),
      messageId: normalizeHeaderMessageId(h?.headerMessageId),
      subject: String(h?.subject || ""),
      from: String(h?.author || ""),
      date: h?.date ? new Date(h.date).toISOString() : "",
      accountId: String(h?.folder?.accountId || ""),
      folderId: String(h?.folder?.id || ""),
      unread: !h?.read,
    };
  }

  // mail.new payload for one folder, or null when the bridge must not see it.
  function buildMailNewEvent(cfg, folder, headers, totalCount) {
    const accountId = String(folder?.accountId || "");
    if (!bridgeSettings(cfg).enabled || !isBridgeReadable(cfg, accountId)) return null;
    const list = (Array.isArray(headers) ? headers : [])
      .slice()
      .sort((a, b) => new Date(b?.date || 0) - new Date(a?.date || 0))
      .slice(0, MAIL_NEW_MAX_MESSAGES)
      .map(bridgeMailSummary);
    return {
      accountId,
      folderId: String(folder?.id || ""),
      folder: cleanPath(folder?.path || folder?.name || ""),
      count: Math.max(Number(totalCount) || 0, list.length),
      messages: list,
    };
  }

  // Collects high-volume change notifications and turns them into few, capped, account-filtered events.
  // The caller drains it on a timer (~500 ms after the first change).
  function createBridgeEventQueue({ cap = BRIDGE_EVENT_ID_CAP } = {}) {
    const storeCap = cap * 4;
    let st = null;
    const reset = () => {
      st = { updated: new Map(), deleted: new Map(), moved: [], calendar: new Map(), overflow: { updated: false, deleted: false, moved: false } };
    };
    reset();
    const put = (kind, id, accountId) => {
      const key = String(id ?? "");
      if (!key) return;
      if (st[kind].has(key)) return;
      if (st[kind].size >= storeCap) {
        st.overflow[kind] = true;
        return;
      }
      st[kind].set(key, String(accountId || ""));
    };
    function idsEvent(map, overflow, cfg) {
      const ids = [];
      const accounts = new Set();
      let truncated = overflow;
      for (const [id, acc] of map) {
        if (!isBridgeReadable(cfg, acc)) continue;
        if (ids.length >= cap) {
          truncated = true;
          break;
        }
        ids.push(id);
        if (acc) accounts.add(acc);
      }
      if (ids.length === 0 && !truncated) return null;
      return { ids, accountIds: [...accounts].sort(), truncated };
    }
    return {
      addUpdated(id, accountId) { put("updated", id, accountId); },
      addDeleted(id, accountId) { put("deleted", id, accountId); },
      addMoved(entry) {
        if (st.moved.length >= storeCap) {
          st.overflow.moved = true;
          return;
        }
        st.moved.push({
          oldId: String(entry?.oldId ?? ""),
          id: String(entry?.id ?? ""),
          fromFolderId: String(entry?.fromFolderId || ""),
          toFolderId: String(entry?.toFolderId || ""),
          fromAccountId: String(entry?.fromAccountId || ""),
          toAccountId: String(entry?.toAccountId || ""),
        });
      },
      // The source list had more entries than were read: tell the client to resync.
      markTruncated(kind) {
        if (kind in st.overflow) st.overflow[kind] = true;
      },
      addCalendar(calendarId, id) {
        const calId = String(calendarId || "");
        if (!calId) return;
        if (!st.calendar.has(calId)) st.calendar.set(calId, { ids: new Set(), truncated: false });
        const c = st.calendar.get(calId);
        if (!id) return;
        if (c.ids.size >= cap) c.truncated = true;
        else c.ids.add(String(id));
      },
      isEmpty() {
        return st.updated.size === 0 && st.deleted.size === 0 && st.moved.length === 0 && st.calendar.size === 0
          && !st.overflow.updated && !st.overflow.deleted && !st.overflow.moved;
      },
      // Returns [{event, data}] and empties the queue. Nothing is returned while the bridge is off.
      drain(cfg) {
        const cur = st;
        reset();
        if (!bridgeSettings(cfg).enabled) return [];
        const out = [];
        // Moves across the readability boundary look like a deletion or an update to the client.
        const moved = { oldIds: [], ids: [], fromFolderIds: new Set(), toFolderIds: new Set(), accountIds: new Set() };
        let movedTruncated = cur.overflow.moved;
        for (const m of cur.moved) {
          const fromOk = isBridgeReadable(cfg, m.fromAccountId);
          const toOk = isBridgeReadable(cfg, m.toAccountId);
          if (fromOk && toOk) {
            if (moved.ids.length >= cap) {
              movedTruncated = true;
              continue;
            }
            moved.oldIds.push(m.oldId);
            moved.ids.push(m.id);
            if (m.fromFolderId) moved.fromFolderIds.add(m.fromFolderId);
            if (m.toFolderId) moved.toFolderIds.add(m.toFolderId);
            if (m.fromAccountId) moved.accountIds.add(m.fromAccountId);
            if (m.toAccountId) moved.accountIds.add(m.toAccountId);
          } else if (fromOk) {
            if (!cur.deleted.has(m.oldId) && m.oldId) cur.deleted.set(m.oldId, m.fromAccountId);
          } else if (toOk) {
            if (!cur.updated.has(m.id) && m.id) cur.updated.set(m.id, m.toAccountId);
          }
        }
        const upd = idsEvent(cur.updated, cur.overflow.updated, cfg);
        if (upd) out.push({ event: "mail.updated", data: upd });
        const del = idsEvent(cur.deleted, cur.overflow.deleted, cfg);
        if (del) out.push({ event: "mail.deleted", data: del });
        if (moved.ids.length > 0 || movedTruncated) {
          out.push({
            event: "mail.moved",
            data: {
              oldIds: moved.oldIds,
              ids: moved.ids,
              fromFolderIds: [...moved.fromFolderIds],
              toFolderIds: [...moved.toFolderIds],
              accountIds: [...moved.accountIds].sort(),
              truncated: movedTruncated,
            },
          });
        }
        for (const [calendarId, c] of cur.calendar) {
          out.push({ event: "calendar.changed", data: { calendarId, ids: [...c.ids], truncated: c.truncated } });
        }
        return out;
      },
    };
  }

  // Native host reconnect: exponential backoff 1 s -> 30 s; a missing host is not retried automatically.
  function nativeReconnectDelay(attempt, { baseMs = 1000, maxMs = 30000 } = {}) {
    const n = Math.max(0, Number(attempt) || 0);
    return Math.min(maxMs, baseMs * 2 ** Math.min(n, 10));
  }

  function isNativeHostMissingError(message) {
    const msg = String(message || "").toLowerCase();
    return msg.includes("no such native application") || msg.includes("native messaging host not found")
      || msg.includes("not found in the registry") || msg.includes("access to the specified native messaging host is forbidden");
  }

  function summarizeMessageHeader(h) {
    if (!h) return null;
    const folderPath = cleanPath(h.folder?.path || h.folder?.name || "");
    return {
      id: String(h.id),
      messageId: normalizeHeaderMessageId(h.headerMessageId),
      threadId: String(h.headerMessageId || ""),
      accountId: String(h.folder?.accountId || ""),
      folderId: String(h.folder?.id || ""),
      folder: folderPath,
      inInbox: isInboxFolder(h.folder),
      subject: String(h.subject || ""),
      from: String(h.author || ""),
      to: Array.isArray(h.recipients) ? h.recipients.map(String) : [],
      cc: Array.isArray(h.ccList) ? h.ccList.map(String) : [],
      date: h.date ? new Date(h.date).toISOString() : "",
      read: Boolean(h.read),
      unread: !h.read,
      flagged: Boolean(h.flagged),
      tags: Array.isArray(h.tags) ? h.tags.map(String) : [],
      size: Number(h.size || 0),
      external: Boolean(h.external),
    };
  }

  function matchMessageQuery(h, rawQuery) {
    const q = String(rawQuery || "").trim().toLowerCase();
    if (!q) return true;
    if (!h) return false;
    const subj = String(h.subject || "").toLowerCase();
    const from = String(h.author || "").toLowerCase();
    const to = (Array.isArray(h.recipients) ? h.recipients.join(" ") : "").toLowerCase();
    const cc = (Array.isArray(h.ccList) ? h.ccList.join(" ") : "").toLowerCase();
    const all = `${subj} ${from} ${to} ${cc}`;
    const tokens = q.split(/\s+/).filter(Boolean);
    for (const tok of tokens) {
      if (tok.startsWith("from:") && tok.length > 5) {
        if (!from.includes(tok.slice(5))) return false;
      } else if (tok.startsWith("subject:") && tok.length > 8) {
        if (!subj.includes(tok.slice(8))) return false;
      } else if (tok.startsWith("to:") && tok.length > 3) {
        if (!to.includes(tok.slice(3)) && !cc.includes(tok.slice(3))) return false;
      } else if (!all.includes(tok)) {
        return false;
      }
    }
    return true;
  }

  const api = {
    textToHtml, htmlToText, renderMarkdownHtml, splitDraft, draftOwnText, replaceDraftOwn, isLightHex, buildThemeVars,
    PolicyError, cleanPath, isInboxFolder, findFolderInAccount, requireFeature, triageAccountIds, kanbanAccountIds,
    folderMatchesSubtree, findAccountInbox, listInboxLevel1Folders, level1FolderName, relativeFolderLabel,
    isOpenKanbanCard, countOpenKanban,
    toggleCardSelection, selectCardRange, toggleColumnSelection, resolveDraggedCardIds,
    requireWritable, requireMovable, messageMoveKind, requireBridgeSend, summarizeMessageHeader, matchMessageQuery,
    BRIDGE_RPC_POLICY, isBridgeErrorCode, bridgeSettings, isBridgeReadable, requireBridgeEnabled, requireBridgeReadable,
    requireBridgeWritable, requireBridgeMovable, requireBridgeGlobalWrite, checkBridgeRPC, filterBridgeReadable,
    sanitizeBridgeFilterSpec, hasNonEmptyCondition, bridgeErrorString, normalizeHeaderMessageId, parseBridgeMessageRef,
    pickBridgeMessage, pickComposeIdentity, createIdempotencyCache, bridgeMailSummary, buildMailNewEvent,
    createBridgeEventQueue, nativeReconnectDelay, isNativeHostMissingError,
    acceptDirectHit, pickMessageCandidate, selectScanFolders,
    newestSince, selectScanMessages, mergeTags, normalizeSubjectForThread, parseMessageIds, selectThread,
    buildNativeFilterSpecFromSuggestion, buildNativeFilterSpecFromRule,
    isLocalAccount, findLocalAccount, buildArchiveRelativeParts, requireLocalArchiveMove,
    requireArchiveDelete, matchArchiveCandidate,
  };
  root.OmaCore = api;
  if (typeof module !== "undefined" && module.exports) module.exports = api;
})(typeof globalThis !== "undefined" ? globalThis : this);
