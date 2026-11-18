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
    "messages.open": { kind: "ui", message: true },
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

  // ---------------------------------------------------------------------------
  // Encryption and signatures. Thunderbird (125+) decrypts in messages.getFull() unless told not to
  // and reports the result on the root part (decryptionStatus). Signatures are never verified by the
  // WebExtension API; describeMessageSecurity only says that a mail carries one.
  // ---------------------------------------------------------------------------

  const DECRYPTION_STATES = new Set(["none", "success", "fail", "skipped"]);

  function mimePartTypes(part, out = [], depth = 0) {
    if (!part || typeof part !== "object" || depth > 32) return out;
    if (part.contentType) out.push(String(part.contentType).toLowerCase());
    for (const child of Array.isArray(part.parts) ? part.parts : []) mimePartTypes(child, out, depth + 1);
    return out;
  }

  function firstHeader(full, name) {
    const v = full?.headers?.[name];
    return String(Array.isArray(v) ? v[0] || "" : v || "").toLowerCase();
  }

  // { encrypted, decryption: "none" | "success" | "fail" | "skipped", signed } of a getFull() result.
  // bodyText is the text found in it: inline PGP ("-----BEGIN PGP MESSAGE-----") shows up only there.
  function describeMessageSecurity(full, { decrypt = false, bodyText = "" } = {}) {
    const types = mimePartTypes(full);
    const outer = firstHeader(full, "content-type") || types[0] || "";
    const text = String(bodyText || "");
    const isSmime = (t) => /^application\/(x-)?pkcs7-mime/.test(t);
    const smimeSigned = isSmime(outer) && /smime-type\s*=\s*"?signed-data/.test(outer);
    const inlineEncrypted = text.includes("-----BEGIN PGP MESSAGE-----");
    const status = DECRYPTION_STATES.has(full?.decryptionStatus) ? full.decryptionStatus : "";

    let encrypted;
    if (status) {
      encrypted = status !== "none" || inlineEncrypted;
    } else {
      encrypted = inlineEncrypted || outer.startsWith("multipart/encrypted") || (isSmime(outer) && !smimeSigned) ||
        types.some((t) => t.startsWith("multipart/encrypted") || t.startsWith("application/pgp-encrypted"));
    }
    let decryption = "none";
    if (encrypted) {
      if (inlineEncrypted) decryption = decrypt ? "fail" : "skipped";
      else if (status && status !== "none") decryption = status;
      else if (!decrypt) decryption = "skipped";
      else decryption = text.trim() ? "success" : "fail";
    }
    const signed = smimeSigned || outer.startsWith("multipart/signed") || text.includes("-----BEGIN PGP SIGNED MESSAGE-----") ||
      types.some((t) => t.startsWith("multipart/signed") || /^application\/(pgp-signature|(x-)?pkcs7-signature)/.test(t));
    return { encrypted, decryption, signed };
  }

  // Whether the content of a described mail may be handed on (decrypted or never encrypted).
  function securityReadable(sec) {
    return !sec?.encrypted || sec.decryption === "success";
  }

  // Plans the signing/encryption of a compose tab from its selectedEncryptionTechnology (Thunderbird
  // 128+; undefined when the identity has no OpenPGP or S/MIME key). sign: "on", "off" or "" (keep the
  // identity's default). requireEncryption keeps a reply to an encrypted mail encrypted, because it
  // quotes the decrypted original. Returns the patch to apply (null: nothing to change) and the result.
  function planComposeSecurity(tech, { sign = "", requireEncryption = false } = {}) {
    const want = String(sign || "");
    if (want && want !== "on" && want !== "off") throw new PolicyError("invalid_argument", 'sign must be "on", "off" or empty');
    const name = tech && typeof tech === "object" ? String(tech.name || "") : "";
    if (!name) {
      if (requireEncryption) {
        throw new PolicyError("encryption_unavailable", "a reply to an encrypted mail is only sent encrypted, and this identity has no OpenPGP or S/MIME key");
      }
      if (want === "on") throw new PolicyError("signing_unavailable", "this identity has no OpenPGP or S/MIME key");
      return { patch: null, result: { technology: "", signed: false, encrypted: false } };
    }
    const next = { ...tech, name, signMessage: Boolean(tech.signMessage), encryptBody: Boolean(tech.encryptBody) };
    if (want === "on") next.signMessage = true;
    if (want === "off") next.signMessage = false;
    if (requireEncryption) next.encryptBody = true;
    const changed = next.signMessage !== Boolean(tech.signMessage) || next.encryptBody !== Boolean(tech.encryptBody);
    return { patch: changed ? next : null, result: { technology: name, signed: next.signMessage, encrypted: next.encryptBody } };
  }

  // After setComposeDetails: throws when Thunderbird did not take the planned state, so that a mail
  // never goes out unsigned or unencrypted against the request.
  function verifyComposeSecurity(tech, planned) {
    if (!planned || !planned.technology) return;
    const got = tech && typeof tech === "object" ? tech : {};
    if (Boolean(got.signMessage) !== planned.signed || Boolean(got.encryptBody) !== planned.encrypted) {
      throw new PolicyError("compose_security_unsupported", "Thunderbird did not apply the requested signing/encryption");
    }
  }

  // ---------------------------------------------------------------------------
  // Host-only RPC: the host's own jobs (triage scans, archive runs) reach Thunderbird through these
  // methods. They arrive with origin "host", which only the host can set (the bridge server never
  // does), and are checked against the mailbox policy instead of the bridge policy.
  // ---------------------------------------------------------------------------

  const HOST_RPC_POLICY = Object.freeze({
    "host.triage.collect": { kind: "read", feature: "triage" },
    "host.archive.candidates": { kind: "read" },
    "host.archive.raw": { kind: "read" },
    "host.archive.finish": { kind: "write" },
  });

  function checkHostRPC(cfg, method, params = {}) {
    const policy = Object.prototype.hasOwnProperty.call(HOST_RPC_POLICY, method) ? HOST_RPC_POLICY[method] : null;
    if (!policy) throw new PolicyError("invalid_argument", `unknown host method ${String(method || "")}`);
    if (!cfg) throw new PolicyError("host_config_unavailable");
    if (policy.feature) requireFeature(cfg, policy.feature);
    const enabled = triageAccountIds(cfg);
    if (enabled.size === 0) throw new PolicyError("no_triage_accounts");
    const p = params && typeof params === "object" ? params : {};
    const accountId = String(p.accountId || p.candidate?.accountId || "").trim();
    if (accountId && !enabled.has(accountId)) throw new PolicyError("account_not_enabled");
    if (method !== "host.triage.collect" && !accountId) throw new PolicyError("invalid_argument", "accountId is required");
    if (policy.kind === "write" && cfg?.mailbox?.readOnly !== false) throw new PolicyError("read_only");
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
    htmlToText, PolicyError, cleanPath, isInboxFolder, findFolderInAccount, requireFeature, triageAccountIds,
    requireWritable, requireMovable, messageMoveKind, requireBridgeSend, summarizeMessageHeader,
    matchMessageQuery, BRIDGE_RPC_POLICY, isBridgeErrorCode, bridgeSettings, isBridgeReadable,
    requireBridgeEnabled, requireBridgeReadable, requireBridgeWritable, requireBridgeMovable,
    requireBridgeGlobalWrite, checkBridgeRPC, filterBridgeReadable, sanitizeBridgeFilterSpec,
    hasNonEmptyCondition, bridgeErrorString, normalizeHeaderMessageId, parseBridgeMessageRef,
    pickBridgeMessage, pickComposeIdentity, createIdempotencyCache, bridgeMailSummary, buildMailNewEvent,
    createBridgeEventQueue, nativeReconnectDelay, isNativeHostMissingError, acceptDirectHit,
    pickMessageCandidate, selectScanMessages, normalizeSubjectForThread, parseMessageIds, selectThread,
    isLocalAccount, findLocalAccount, buildArchiveRelativeParts, requireLocalArchiveMove,
    requireArchiveDelete, matchArchiveCandidate, describeMessageSecurity, securityReadable,
    planComposeSecurity, verifyComposeSecurity, HOST_RPC_POLICY, checkHostRPC,
  };
  root.OmaCore = api;
  if (typeof module !== "undefined" && module.exports) module.exports = api;
})(typeof globalThis !== "undefined" ? globalThis : this);
