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

  // Folders a triage scan may read: INBOX of enabled accounts plus rule folders (with subfolders).
  function selectScanFolders(cfg, folders) {
    const enabled = triageAccountIds(cfg);
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

  const api = {
    PolicyError, cleanPath, isInboxFolder, findFolderInAccount, requireFeature, triageAccountIds,
    requireWritable, requireMovable, acceptDirectHit, pickMessageCandidate, selectScanFolders,
    newestSince, mergeTags, normalizeSubjectForThread, parseMessageIds, selectThread,
  };
  root.OmaCore = api;
  if (typeof module !== "undefined" && module.exports) module.exports = api;
})(typeof globalThis !== "undefined" ? globalThis : this);
