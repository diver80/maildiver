/* global browser, messenger, OmaCore */
// OmaThunder add-on: the bridge between Thunderbird and the OmaThunder host (and through it the
// OmaThunder app), plus the Omarchy theme for Thunderbird itself. Mail, calendar, contacts, AI,
// triage scans and archive runs are used in the app; the host runs the automatic work and reaches
// Thunderbird through the RPC methods below, each checked against the policy in lib/core.js.

const api = typeof messenger !== "undefined" ? messenger : browser;

// Pure decision logic (unit-tested in tests/core.test.js); this file only talks to Thunderbird.
const {
  PolicyError, isInboxFolder, findFolderInAccount, triageAccountIds,
  requireBridgeSend, summarizeMessageHeader, matchMessageQuery, messageMoveKind,
  acceptDirectHit, pickMessageCandidate, selectScanMessages,
  bridgeSettings, isBridgeReadable, requireBridgeReadable, requireBridgeWritable, requireBridgeMovable,
  checkBridgeRPC, filterBridgeReadable, sanitizeBridgeFilterSpec, bridgeErrorString, normalizeHeaderMessageId,
  parseBridgeMessageRef, pickBridgeMessage, pickComposeIdentity, createIdempotencyCache, buildMailNewEvent,
  createBridgeEventQueue, nativeReconnectDelay, isNativeHostMissingError,
  normalizeSubjectForThread, parseMessageIds, selectThread, htmlToText,
  isLocalAccount, findLocalAccount, buildArchiveRelativeParts,
  requireLocalArchiveMove, requireArchiveDelete, matchArchiveCandidate,
  describeMessageSecurity, securityReadable, planComposeSecurity, verifyComposeSecurity, checkHostRPC,
} = OmaCore;

// Must match ProtocolVersion in internal/native/handler.go.
const PROTOCOL = 4;
const HOST_NAME = "dev.omathunder.host";
let nativePort = null;
let hostConnected = false;
let reqCounter = 0;
const pendingRequests = new Map();

// Last host config seen (get_config); used for bridge events and the bridge policy.
let bridgeCfgCache = { cfg: null, at: 0 };

function rememberHostConfig(cfg) {
  if (cfg && typeof cfg === "object") bridgeCfgCache = { cfg, at: Date.now() };
}

let currentThemePayload = null;
let bodyScriptHandle = null;
let composeScriptHandle = null;

// Whether the theme is on (features.theme) and also recolors message bodies and the compose
// editor (theme.styleBody).
let themeOn = false;
let styleBody = false;

// The native port stays open while Thunderbird runs: the host serves the local bridge only while
// it is connected. After a disconnect it is reopened with backoff (1 s -> 30 s); a host that is not
// installed is not retried automatically (the next UI action tries again).
let nativeReconnectTimer = null;
let nativeReconnectAttempt = 0;

async function connectNative() {
  if (nativePort) return nativePort;
  clearTimeout(nativeReconnectTimer);
  nativeReconnectTimer = null;
  const port = api.runtime.connectNative(HOST_NAME);
  nativePort = port;

  port.onMessage.addListener((msg) => {
    if (nativePort === port) nativeReconnectAttempt = 0; // the host answers: reset the backoff
    if (msg && msg.event === "tb_rpc" && msg.id && msg.result) {
      handleThunderbirdRPC(port, msg.id, msg.result.method, msg.result.params, msg.result.origin).catch(() => {});
      return;
    }
    if (msg && msg.event === "theme_updated" && msg.result) {
      if (!themeOn) return;
      applyThemePayload(msg.result);
      broadcastToUI({ type: "omathunder:themeChanged", payload: msg.result });
      return;
    }
    if (msg && msg.id && pendingRequests.has(msg.id)) {
      const { resolve, reject, timer } = pendingRequests.get(msg.id);
      clearTimeout(timer);
      pendingRequests.delete(msg.id);
      if (msg.ok) resolve(msg.result);
      else reject(new Error(msg.error || "Native host error"));
    }
  });

  port.onDisconnect.addListener((p) => {
    const errMsg = p?.error?.message || api.runtime.lastError?.message || "Host disconnected";
    if (nativePort !== port) return;
    nativePort = null;
    hostConnected = false;
    bridgeCfgCache = { cfg: null, at: 0 };
    for (const [, pending] of pendingRequests) {
      clearTimeout(pending.timer);
      pending.reject(new Error(errMsg));
    }
    pendingRequests.clear();
    broadcastToUI({ type: "omathunder:hostStatus", connected: false, error: errMsg });
    scheduleNativeReconnect(errMsg);
  });

  return port;
}

function scheduleNativeReconnect(errMsg) {
  if (isNativeHostMissingError(errMsg)) return; // not installed: no retry loop
  if (nativeReconnectTimer || nativePort) return;
  const delay = nativeReconnectDelay(nativeReconnectAttempt++);
  nativeReconnectTimer = setTimeout(async () => {
    nativeReconnectTimer = null;
    if (nativePort) return;
    try {
      const cfg = await callNative("get_config", {}, 15000);
      hostConnected = true;
      rememberHostConfig(cfg);
      await syncTheme(cfg);
      broadcastToUI({ type: "omathunder:hostStatus", connected: true });
    } catch (_) {
      // A disconnect schedules the next attempt itself; other errors leave the (open) port alone.
    }
  }, delay);
}

async function callNative(method, params = {}, timeoutMs = 45000) {
  const port = await connectNative();
  const id = `req-${++reqCounter}`;
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      pendingRequests.delete(id);
      reject(new Error(`Timeout calling ${method}`));
    }, timeoutMs);
    pendingRequests.set(id, { resolve, reject, timer });
    port.postMessage({ id, method, params });
  });
}

async function applyThemePayload(payload) {
  if (!payload || !themeOn) return;
  currentThemePayload = payload;

  if (api.theme?.update && payload.thunderbird) {
    await api.theme.update(payload.thunderbird).catch(() => {});
  }
  if (bodyScriptHandle) {
    await bodyScriptHandle.unregister().catch(() => {});
    bodyScriptHandle = null;
  }
  if (composeScriptHandle) {
    await composeScriptHandle.unregister().catch(() => {});
    composeScriptHandle = null;
  }
  if (!styleBody) return;
  if (api.messageDisplayScripts?.register && payload.bodyCss) {
    bodyScriptHandle = await api.messageDisplayScripts.register({
      css: [{ code: payload.bodyCss }],
      js: [{ file: "/content/message-theme.js" }],
    }).catch(() => null);
  }
  if (api.composeScripts?.register && payload.composeCss) {
    composeScriptHandle = await api.composeScripts.register({
      css: [{ code: payload.composeCss }],
      js: [{ file: "/content/compose-theme.js" }],
    }).catch(() => null);
  }
}

async function removeTheme() {
  currentThemePayload = null;
  if (api.theme?.reset) await api.theme.reset().catch(() => {});
  if (bodyScriptHandle) {
    await bodyScriptHandle.unregister().catch(() => {});
    bodyScriptHandle = null;
  }
  if (composeScriptHandle) {
    await composeScriptHandle.unregister().catch(() => {});
    composeScriptHandle = null;
  }
}

// Applies the theme settings of a host config: switches the theme on or off.
async function syncTheme(cfg, themePayload = null) {
  const wasOn = themeOn;
  const styleBodyChanged = styleBody !== Boolean(cfg?.theme?.styleBody);
  themeOn = Boolean(cfg?.features?.theme);
  styleBody = Boolean(cfg?.theme?.styleBody);
  if (!themeOn) {
    if (wasOn || currentThemePayload) await removeTheme();
    return;
  }
  let payload = themePayload;
  if (!payload && (!wasOn || styleBodyChanged || !currentThemePayload)) {
    payload = await callNative("get_theme").catch(() => null);
  }
  if (payload) await applyThemePayload(payload);
}

// The host's config.json. Without the host there is nothing to do: the add-on is its bridge.
async function loadConfig() {
  const cfg = await callNative("get_config");
  hostConnected = true;
  rememberHostConfig(cfg);
  await syncTheme(cfg);
  return cfg;
}

// ---------------------------------------------------------------------------
// Thunderbird data helpers
// ---------------------------------------------------------------------------

async function listAccountFolders() {
  const accounts = await api.accounts.list(true);
  const out = [];
  const roots = new Map();
  function walk(folders, accountName) {
    for (const f of folders || []) {
      const cleanPath = (f.path || f.name || "").replace(/^\/+/, "");
      out.push({
        id: f.id,
        accountId: f.accountId,
        accountName,
        name: f.name,
        path: cleanPath,
        type: f.type || "",
        specialUse: f.specialUse || [],
      });
      if (Array.isArray(f.subFolders)) walk(f.subFolders, accountName);
    }
  }
  for (const acc of accounts) {
    if (acc.rootFolder?.id) roots.set(acc.id, acc.rootFolder.id);
    else if (Array.isArray(acc.folders) && acc.folders[0]?.id) roots.set(acc.id, acc.folders[0].id);
    walk(acc.folders, acc.name);
  }
  return { folders: out, accounts, roots };
}

async function listAllFolders() {
  const { folders } = await listAccountFolders();
  return folders;
}

function findAllInboxes(folders) {
  return folders.filter(isInboxFolder);
}

// Reads a mail's MIME tree. Without decrypt an encrypted mail stays encrypted: Thunderbird asks for
// no key password or PIN, and nothing decrypted reaches automatic processing.
async function getFullMessage(id, decrypt = false) {
  return api.messages.getFull(id, { decrypt: Boolean(decrypt) }).catch(() => null);
}

function findPartText(part, mimePrefix) {
  if (!part) return "";
  if (part.contentType && part.contentType.toLowerCase().startsWith(mimePrefix) && part.body) {
    return part.body;
  }
  if (Array.isArray(part.parts)) {
    for (const child of part.parts) {
      const found = findPartText(child, mimePrefix);
      if (found) return found;
    }
  }
  return "";
}

function extractPlainText(part) {
  const plain = findPartText(part, "text/plain");
  if (plain) return plain;
  const html = findPartText(part, "text/html");
  return html ? htmlToText(html) : "";
}

function isReadableTextAttachment(att) {
  const name = String(att?.name || "").toLowerCase();
  const mime = String(att?.contentType || "").toLowerCase();
  if (mime.startsWith("text/") || mime === "application/json" || mime === "application/xml" || mime === "text/calendar") {
    return !name.endsWith(".html") && !name.endsWith(".htm");
  }
  return /\.(txt|md|csv|tsv|ics|json|log|xml|eml|yaml|yml)$/i.test(name);
}

async function readReadableAttachments(messageId, attachments) {
  if (!api.messages?.getAttachmentFile || !Array.isArray(attachments) || attachments.length === 0) return "";
  const chunks = [];
  let total = 0;
  for (const att of attachments) {
    if (chunks.length >= 3 || total >= 4500) break;
    if (!att?.partName || !isReadableTextAttachment(att)) continue;
    if (att.size && att.size > 256 * 1024) continue;
    const file = await api.messages.getAttachmentFile(messageId, att.partName).catch(() => null);
    if (!file || typeof file.text !== "function") continue;
    const text = (await file.text().catch(() => "")).trim();
    if (!text) continue;
    const snippet = text.slice(0, Math.min(2000, 4500 - total));
    total += snippet.length;
    chunks.push(`--- Attachment: ${att.name || att.partName} ---\n${snippet}`);
  }
  return chunks.join("\n\n");
}

// Mail input for AI and triage. Encrypted mails are only decrypted when the caller asks for it
// (decrypt: an explicit user action on that mail); otherwise their text and attachments stay out.
async function buildMessageInput(header, { includeAttachmentText = false, decrypt = false } = {}) {
  const full = await getFullMessage(header.id, decrypt);
  const plain = extractPlainText(full);
  const security = describeMessageSecurity(full, { decrypt, bodyText: plain });
  const readable = securityReadable(security);
  // listAttachments always decrypts, so an encrypted mail read without decrypt lists none.
  const attachments = readable ? await api.messages.listAttachments(header.id).catch(() => []) : [];
  const headers = {};
  if (full?.headers) {
    for (const [k, vals] of Object.entries(full.headers)) {
      if (Array.isArray(vals) && vals.length > 0) headers[k] = String(vals[0]);
    }
  }
  let bodyText = readable ? plain.slice(0, 6000) : "";
  if (includeAttachmentText && readable) {
    const attText = await readReadableAttachments(header.id, attachments);
    if (attText) {
      bodyText = `${bodyText}\n\n${attText}`.slice(0, 9000);
    }
  }
  return {
    id: String(header.id),
    threadId: header.headerMessageId || "",
    accountId: header.folder?.accountId || "",
    folder: (header.folder?.path || "").replace(/^\/+/, ""),
    folderId: header.folder?.id || "",
    inInbox: isInboxFolder(header.folder),
    subject: header.subject || "(Ohne Betreff)",
    from: header.author || "",
    to: header.recipients || [],
    date: header.date ? new Date(header.date).toISOString() : "",
    headers,
    bodyText,
    unread: !header.read,
    attachments: attachments.map((a) => a.name).filter(Boolean),
    ...(security.encrypted ? { encrypted: true } : {}),
  };
}

// Resolves a stored message reference to a live MessageHeader.
// Thunderbird's numeric message IDs are only valid within one session, so the RFC Message-ID
// (headerMessageId) is the source of truth. A numeric ID is only trusted if its Message-ID matches.
// strict=true (default, required before any write) returns null when the match is ambiguous.
async function resolveMessage(ref, { strict = true } = {}) {
  const hdrId = ref?.headerMessageId || "";
  const numId = Number(ref?.messageId);
  if (Number.isInteger(numId) && numId > 0) {
    const direct = await api.messages.get(numId).catch(() => null);
    if (acceptDirectHit(ref, direct, strict)) return direct;
  }
  if (!hdrId || !api.messages?.query) return null;
  const res = await api.messages.query({ headerMessageId: hdrId }).catch(() => null);
  return pickMessageCandidate(ref, res?.messages || [], strict);
}

async function collectPagedMessages(firstPagePromise, maxPages = 20) {
  let page = await firstPagePromise.catch(() => null);
  const out = [];
  let pages = 0;
  while (page) {
    out.push(...(page.messages || []));
    if (!page.id || ++pages >= maxPages || !api.messages?.continueList) break;
    page = await api.messages.continueList(page.id).catch(() => null);
  }
  return out;
}

// Returns ALL unread mails of a folder plus the newest `limit` read mails received within the last `days` days.
async function listRecentMessages(folder, limit, days) {
  const since = new Date(Date.now() - days * 86400000);
  if (api.messages?.query) {
    const [unreadMsgs, recentMsgs] = await Promise.all([
      collectPagedMessages(api.messages.query({ folderId: folder.id, unread: true }), 50),
      collectPagedMessages(api.messages.query({ folderId: folder.id, fromDate: since }), 20),
    ]);
    if (unreadMsgs.length > 0 || recentMsgs.length > 0) {
      return selectScanMessages([...unreadMsgs, ...recentMsgs], since, limit);
    }
  }
  const listed = api.messages?.list
    ? await collectPagedMessages(api.messages.list(folder.id), 20)
    : [];
  return selectScanMessages(listed, since, limit);
}

const MAX_SELECTED_MESSAGES = 25;

async function collectMessageList(list, limit = MAX_SELECTED_MESSAGES) {
  const out = [];
  if (!list) return out;
  let page = list;
  while (page && Array.isArray(page.messages)) {
    for (const h of page.messages) {
      out.push(h);
      if (out.length >= limit) return out;
    }
    if (!page.id || !api.messages?.continueList) break;
    page = await api.messages.continueList(page.id).catch(() => null);
  }
  return out;
}

// Collects the conversation of a mail. Membership is decided by the References / In-Reply-To
// headers; the subject search only supplies candidates, so unrelated mails with the same
// subject ("Rechnung", "Angebot") never end up in the AI prompt.
async function collectThreadMessages(primary, max = 8, { decrypt = false } = {}) {
  if (!primary || !api.messages?.query) return primary ? [primary] : [];
  // Candidate budget follows the requested size (bridge: up to 50 mails), so a large limit really
  // returns more mails; every candidate costs one getFull().
  const want = Math.max(1, Math.min(Number(max) || 8, 50));
  const headers = new Map();
  const add = (list) => {
    for (const h of list || []) {
      const key = h.headerMessageId || "";
      if (key && key !== primary.threadId && !headers.has(key)) headers.set(key, h);
    }
  };

  // Earlier mails named in the primary mail's own headers.
  const ancestors = parseMessageIds(primary.headers?.references, primary.headers?.["in-reply-to"]).slice(-Math.max(8, want));
  for (const id of ancestors) {
    const res = await api.messages.query({ headerMessageId: id }).catch(() => null);
    add((res?.messages || []).slice(0, 1));
  }
  // Later replies cannot be looked up by header, so search by subject and verify the link below.
  const baseSubj = normalizeSubjectForThread(primary.subject);
  if (baseSubj) {
    const res = await api.messages.query({ accountId: primary.accountId || undefined, subject: baseSubj }).catch(() => null);
    add((res?.messages || []).filter((h) => normalizeSubjectForThread(h.subject).toLowerCase() === baseSubj.toLowerCase()).slice(0, Math.max(16, want + 16)));
  }

  const candidates = [];
  for (const h of [...headers.values()].slice(0, Math.min(100, Math.max(24, want * 2)))) candidates.push(await buildMessageInput(h, { decrypt }));
  return selectThread(primary, candidates, want);
}

async function listNativeFiltersSafe(accountId = "") {
  if (!api.omaFilters?.listFilters) {
    return { supported: false, accounts: [], filters: [] };
  }
  try {
    return await api.omaFilters.listFilters(accountId || undefined);
  } catch (err) {
    return { supported: false, accounts: [], filters: [], error: String(err?.message || err) };
  }
}

// ---------------------------------------------------------------------------
// Archive & cleanup (run by the host, see host.archive.*)
// ---------------------------------------------------------------------------

function bytesToBase64(bytes) {
  const chunks = [];
  const step = 0x8000;
  for (let i = 0; i < bytes.length; i += step) {
    chunks.push(String.fromCharCode.apply(null, bytes.subarray(i, i + step)));
  }
  return btoa(chunks.join(""));
}

async function readRawEmlBase64(messageId) {
  let raw;
  try {
    raw = await api.messages.getRaw(messageId, { decrypt: false });
  } catch (_) {
    raw = await api.messages.getRaw(messageId);
  }
  let bytes;
  if (typeof raw === "string") {
    let allLatin1 = true;
    for (let i = 0; i < raw.length; i++) {
      if (raw.charCodeAt(i) > 255) {
        allLatin1 = false;
        break;
      }
    }
    if (allLatin1) {
      bytes = new Uint8Array(raw.length);
      for (let i = 0; i < raw.length; i++) bytes[i] = raw.charCodeAt(i) & 0xff;
    } else {
      bytes = new TextEncoder().encode(raw);
    }
  } else if (raw && typeof raw.arrayBuffer === "function") {
    bytes = new Uint8Array(await raw.arrayBuffer());
  } else if (raw instanceof ArrayBuffer || ArrayBuffer.isView(raw)) {
    bytes = raw instanceof ArrayBuffer ? new Uint8Array(raw) : new Uint8Array(raw.buffer, raw.byteOffset, raw.byteLength);
  } else {
    throw new Error("messages.getRaw returned unsupported payload");
  }
  if (bytes.length === 0) throw new Error("empty raw eml");
  if (bytes.length > 45 * 1024 * 1024) throw new PolicyError("mail_too_large");
  return { base64: bytesToBase64(bytes), byteLength: bytes.length };
}

async function ensureLocalArchiveFolder(cfg, localAccountId, pathSegments, folderState) {
  if (cfg?.mailbox?.readOnly !== false) throw new PolicyError("read_only");
  const localAcc = (folderState.accounts || []).find((a) => a.id === localAccountId);
  if (!isLocalAccount(localAcc)) throw new PolicyError("local_folder_required");

  let parentId = folderState.roots.get(localAccountId);
  if (!parentId) throw new PolicyError("target_folder_missing");

  let builtPath = "";
  let currentFolder = null;
  for (const seg of pathSegments.filter(Boolean)) {
    builtPath = builtPath ? `${builtPath}/${seg}` : seg;
    let found = findFolderInAccount(folderState.folders, localAccountId, "", builtPath);
    if (!found) {
      if (!api.folders?.create) throw new PolicyError("target_folder_missing");
      const created = await api.folders.create(parentId, seg);
      found = {
        id: created.id,
        accountId: created.accountId || localAccountId,
        name: created.name || seg,
        path: (created.path || builtPath).replace(/^\/+/, ""),
        type: created.type || "",
      };
      folderState.folders.push(found);
    }
    parentId = found.id;
    currentFolder = found;
  }
  if (!currentFolder) throw new PolicyError("target_folder_missing");
  return currentFolder;
}

const EXCLUDED_CLEANUP_FOLDER_TYPES = new Set(["trash", "junk", "drafts", "outbox", "templates"]);

async function collectCleanupCandidates(cfg, params = {}) {
  const enabled = triageAccountIds(cfg);
  if (enabled.size === 0) throw new PolicyError("no_triage_accounts");

  const accountId = String(params.accountId || "");
  if (!accountId || !enabled.has(accountId)) throw new PolicyError("account_not_enabled");

  const folderState = await listAccountFolders();
  const acc = folderState.accounts.find((a) => a.id === accountId);
  const accountName = acc?.name || accountId;
  const scope = params.scope || "inbox"; // "inbox" | "folder" | "all"
  const olderThanDays = Math.max(0, Number(params.olderThanDays ?? 365));
  const onlyRead = params.onlyRead !== false;
  const excludeStarred = params.excludeStarred !== false;
  const maxMessages = Math.min(1000, Math.max(1, Number(params.maxMessages || 200)));

  let targetFolders = [];
  const accFolders = folderState.folders.filter((f) => f.accountId === accountId);
  if (scope === "inbox") {
    targetFolders = accFolders.filter(isInboxFolder);
  } else if (scope === "folder") {
    const cleanTarget = String(params.folderPath || "").replace(/^\/+|\/+$/g, "").toLowerCase();
    const includeSub = params.includeSubfolders !== false;
    targetFolders = accFolders.filter((f) => {
      if (params.folderId && f.id === params.folderId) return true;
      const p = String(f.path || "").replace(/^\/+|\/+$/g, "").toLowerCase();
      if (!cleanTarget) return false;
      return p === cleanTarget || (includeSub && p.startsWith(`${cleanTarget}/`));
    });
  } else {
    targetFolders = accFolders.filter((f) => !EXCLUDED_CLEANUP_FOLDER_TYPES.has(String(f.type || "").toLowerCase()));
  }

  const nowMs = Date.now();
  const toDate = olderThanDays > 0 ? new Date(nowMs - olderThanDays * 86400000) : null;
  const matched = [];
  const criteria = { olderThanDays, onlyRead, excludeStarred };

  for (const folder of targetFolders) {
    let page = toDate && api.messages?.query
      ? await api.messages.query({ folderId: folder.id, toDate }).catch(() => null)
      : null;
    if (!page) page = await api.messages.list(folder.id).catch(() => null);

    let pages = 0;
    while (page) {
      for (const h of page.messages || []) {
        if (matchArchiveCandidate(h, criteria, nowMs)) {
          matched.push({ header: h, folder });
        }
      }
      if (!page.id || ++pages >= 50) break;
      page = await api.messages.continueList(page.id).catch(() => null);
    }
  }

  matched.sort((a, b) => new Date(a.header.date || 0) - new Date(b.header.date || 0));
  const totalMatched = matched.length;
  const totalBytesAll = matched.reduce((sum, x) => sum + Number(x.header.size || 0), 0);
  const sliced = matched.slice(0, maxMessages);

  const candidates = sliced.map(({ header: h, folder }) => {
    const dateIso = h.date ? new Date(h.date).toISOString() : "";
    const folderPath = (folder.path || h.folder?.path || "INBOX").replace(/^\/+/, "");
    return {
      messageId: String(h.id),
      headerMessageId: h.headerMessageId || "",
      accountId,
      accountName,
      folderId: folder.id,
      folderPath,
      subject: h.subject || "(Ohne Betreff)",
      from: h.author || "",
      date: dateIso,
      size: Number(h.size || 0),
      read: Boolean(h.read),
      flagged: Boolean(h.flagged),
      archiveRelPath: buildArchiveRelativeParts(accountName, folderPath, dateIso).join("/"),
    };
  });

  return {
    candidates,
    totalMatched,
    totalBytesAll,
    scannedFolders: targetFolders.length,
    truncated: totalMatched > candidates.length,
    folderState,
  };
}

// ---------------------------------------------------------------------------
// Settings page and events
// ---------------------------------------------------------------------------

function broadcastToUI(message) {
  api.runtime.sendMessage(message).catch(() => {});
}

// The settings page: status, accounts & permissions, theme & language. Everything else is set
// in the OmaThunder app.
async function optionsSnapshot() {
  const extensionVersion = api.runtime.getManifest().version;
  const accounts = ((await api.accounts.list(false).catch(() => [])) || []).map((a) => ({ id: a.id, name: a.name, type: a.type }));
  try {
    const cfg = await loadConfig();
    const [ping, theme, themes, doctor] = await Promise.all([
      callNative("ping"),
      callNative("get_theme").catch(() => null),
      callNative("list_themes").catch(() => []),
      callNative("doctor").catch(() => null),
    ]);
    return {
      connected: true,
      extensionVersion,
      host: { version: ping?.version || "?", protocol: ping?.protocol ?? 0, expectedProtocol: PROTOCOL },
      protocolOk: ping?.protocol === PROTOCOL,
      config: cfg,
      theme,
      themes,
      doctor,
      accounts,
    };
  } catch (err) {
    hostConnected = false;
    return { connected: false, extensionVersion, error: String(err?.message || err), accounts };
  }
}

api.runtime.onMessage.addListener((msg) => {
  if (!msg || !msg.type) return false;
  switch (msg.type) {
    case "omathunder:getSnapshot":
      return optionsSnapshot();
    case "omathunder:saveConfig":
      return (async () => {
        const res = await callNative("save_config", msg.config || {});
        rememberHostConfig(res?.config);
        await syncTheme(res?.config, res?.theme);
        broadcastToUI({ type: "omathunder:themeChanged", payload: currentThemePayload });
        return res;
      })();
    default:
      return false;
  }
});

// The toolbar button opens the settings page.
api.browserAction?.onClicked?.addListener(() => {
  api.runtime.openOptionsPage();
});

// New mail: the bridge gets a mail.new event; the host decides whether a triage scan runs
// (mailbox.scanOnNewMail) and coalesces bursts itself.
api.messages?.onNewMailReceived?.addListener((folder, messages) => {
  if (bridgeEventsWanted()) emitMailNew(folder, messages).catch(() => {});
  if (!nativePort) return; // never starts the host just for this
  try {
    nativePort.postMessage({ method: "triage_new_mail", params: { accountId: folder?.accountId || "" } });
  } catch (_) {
    // port closing
  }
});

// Upper bound of mails read from one onDeleted/onMoved list (more is reported as truncated).
const MAX_EVENT_LIST = 2000;

// Mail changes become bridge events (batched); they only cover readable accounts.
api.messages?.onUpdated?.addListener((message) => {
  // Bulk changes (e.g. mark 5000 mails read) arrive one by one: batch them into a few mail.updated events.
  if (message) queueBridgeEvent((q) => q.addUpdated(message.id, message.folder?.accountId));
});

api.messages?.onDeleted?.addListener(async (messages) => {
  if (!bridgeEventsWanted()) return;
  const list = await collectMessageList(messages, MAX_EVENT_LIST + 1).catch(() => []);
  if (list.length === 0) return;
  queueBridgeEvent((q) => {
    for (const m of list.slice(0, MAX_EVENT_LIST)) q.addDeleted(m.id, m.folder?.accountId);
    if (list.length > MAX_EVENT_LIST) q.markTruncated("deleted");
  });
});

api.messages?.onMoved?.addListener(async (originalMessages, movedMessages) => {
  if (!bridgeEventsWanted()) return;
  const [originals, moved] = await Promise.all([
    collectMessageList(originalMessages, MAX_EVENT_LIST + 1).catch(() => []),
    collectMessageList(movedMessages, MAX_EVENT_LIST + 1).catch(() => []),
  ]);
  if (moved.length === 0) return;
  queueBridgeEvent((q) => {
    // Thunderbird passes both lists in the same order.
    for (let i = 0; i < Math.min(moved.length, MAX_EVENT_LIST); i++) {
      const before = originals[i];
      const after = moved[i];
      q.addMoved({
        oldId: before?.id,
        id: after?.id,
        fromFolderId: before?.folder?.id,
        toFolderId: after?.folder?.id,
        fromAccountId: before?.folder?.accountId,
        toAccountId: after?.folder?.accountId,
      });
    }
    if (moved.length > MAX_EVENT_LIST) q.markTruncated("moved");
  });
});

api.omaCalendar?.onChanged?.addListener((payload) => {
  queueBridgeEvent((q) => q.addCalendar(payload?.calendarId, payload?.id));
});

// ---------------------------------------------------------------------------
// Local bridge: reverse-RPC (tb_rpc) and live events (tb_event) for the native OmaThunder app.
// Every call is checked centrally (OmaCore.checkBridgeRPC) against the host config before a handler
// runs; handlers add the per-object checks. Errors are returned as "code: detail" (CONTRACT §2).
// ---------------------------------------------------------------------------

const bridgeEventQueue = createBridgeEventQueue();
let bridgeEventTimer = null;
const BRIDGE_EVENT_BATCH_MS = 500;
const BRIDGE_CFG_MAX_AGE_MS = 5000;

// Host config for bridge decisions. maxAgeMs=0 always asks the host. Never starts the host itself.
async function hostConfigForBridge(maxAgeMs = 0) {
  if (maxAgeMs > 0 && bridgeCfgCache.cfg && Date.now() - bridgeCfgCache.at <= maxAgeMs) return bridgeCfgCache.cfg;
  if (!nativePort) return null;
  try {
    const cfg = await callNative("get_config", {}, 10000);
    rememberHostConfig(cfg);
    return cfg;
  } catch (_) {
    return null;
  }
}

// Cheap pre-check so bulk operations do not page through message lists for nobody.
function bridgeEventsWanted() {
  if (!nativePort) return false;
  const { cfg, at } = bridgeCfgCache;
  if (cfg && Date.now() - at <= BRIDGE_CFG_MAX_AGE_MS) return bridgeSettings(cfg).enabled;
  return true; // unknown: queue; the flush decides with a fresh config
}

function queueBridgeEvent(add) {
  if (!bridgeEventsWanted()) return;
  add(bridgeEventQueue);
  if (!bridgeEventTimer) bridgeEventTimer = setTimeout(flushBridgeEvents, BRIDGE_EVENT_BATCH_MS);
}

async function flushBridgeEvents() {
  bridgeEventTimer = null;
  if (bridgeEventQueue.isEmpty()) return;
  const cfg = await hostConfigForBridge(BRIDGE_CFG_MAX_AGE_MS);
  // drain() filters by bridge.enabled and bridge.accountIds and always empties the queue.
  for (const ev of bridgeEventQueue.drain(cfg)) emitBridgeEvent(ev.event, ev.data);
}

async function emitMailNew(folder, messages) {
  const cfg = await hostConfigForBridge(BRIDGE_CFG_MAX_AGE_MS);
  if (!bridgeSettings(cfg).enabled || !isBridgeReadable(cfg, folder?.accountId)) return;
  const headers = await collectMessageList(messages, 500).catch(() => []);
  const payload = buildMailNewEvent(cfg, folder, headers, headers.length);
  if (payload) emitBridgeEvent("mail.new", payload);
}

function emitBridgeEvent(eventName, data) {
  if (!nativePort || !eventName) return;
  try {
    nativePort.postMessage({
      method: "tb_event",
      params: { event: eventName, data: data || {} },
    });
  } catch (_) {
    // Ignore when host port is closing.
  }
}

// origin "host": a call of the host's own jobs (host.* methods, mailbox policy); otherwise a call
// of a bridge client (bridge policy). Only the host writes to this port, and its bridge server
// never sets an origin.
async function handleThunderbirdRPC(port, rpcId, method, params, origin = "") {
  let reply;
  try {
    const p = params && typeof params === "object" ? params : {};
    const result = origin === "host"
      ? await dispatchHostRPC(String(method || ""), p)
      : await dispatchThunderbirdRPC(String(method || ""), p);
    reply = { id: rpcId, ok: true, result: result === undefined ? null : result };
  } catch (err) {
    reply = { id: rpcId, ok: false, error: bridgeErrorString(err) };
  }
  // The host may have gone away while the call ran: nobody waits for this result any more.
  if (port !== nativePort) return;
  try {
    port.postMessage({ method: "tb_rpc_result", params: reply });
  } catch (_) {
    // port closed meanwhile
  }
}

// Resolves the mail a bridge call addresses (CONTRACT §4): numeric ID (verified against
// expectedMessageId when given) or RFC Message-ID (unique copy, narrowed by folderId/accountId).
async function resolveBridgeMessage(cfg, params, idKey = "id") {
  const ref = parseBridgeMessageRef(params, idKey);
  let direct = null;
  if (ref.numericId) direct = await api.messages.get(ref.numericId).catch(() => null);
  let candidates = [];
  if ((!ref.numericId || !direct) && ref.headerMessageId && api.messages?.query) {
    candidates = await collectPagedMessages(api.messages.query({ headerMessageId: ref.headerMessageId }), 5);
  }
  return pickBridgeMessage(ref, direct, candidates, cfg);
}

function requireExperiment(fn, name) {
  if (typeof fn !== "function") throw new PolicyError(`feature_disabled:${name}`, "experiment API unavailable");
}

function serializeAccount(acc) {
  return {
    id: acc.id,
    name: acc.name,
    type: acc.type,
    identities: (acc.identities || []).map((idn) => ({
      id: idn.id,
      email: idn.email,
      name: idn.name || "",
    })),
  };
}

function bridgeMessageInput(m) {
  return { ...m, messageId: normalizeHeaderMessageId(m?.threadId) };
}

async function dispatchThunderbirdRPC(method, params) {
  const cfg = method === "ping" ? null : await hostConfigForBridge(0);
  checkBridgeRPC(cfg, method, params);

  switch (method) {
    case "ping":
      return { pong: true, timestamp: new Date().toISOString() };

    case "accounts.list": {
      const accounts = filterBridgeReadable(cfg, (await api.accounts.list(false)) || [], (a) => a?.id);
      return { accounts: accounts.map(serializeAccount) };
    }

    case "folders.list": {
      const folders = filterBridgeReadable(cfg, await listAllFolders());
      const wantAcc = String(params.accountId || "").trim();
      return { folders: wantAcc ? folders.filter((f) => f.accountId === wantAcc) : folders };
    }

    case "messages.list":
      return rpcListMessages(cfg, params);

    case "messages.get": {
      const header = await resolveBridgeMessage(cfg, params);
      // Decrypted only on request: the app's reading pane and explicit AI actions ask for it,
      // scripts and automatic paths get encrypted mails without their content.
      const decrypt = params.decrypt === true;
      const full = await getFullMessage(header.id, decrypt);
      const plain = extractPlainText(full);
      const security = describeMessageSecurity(full, { decrypt, bodyText: plain });
      const readable = securityReadable(security);
      const attachments = readable ? await api.messages.listAttachments(header.id).catch(() => []) : [];
      const headers = {};
      if (full?.headers) {
        for (const [k, vals] of Object.entries(full.headers)) {
          if (Array.isArray(vals) && vals.length > 0) headers[k] = String(vals[0]);
        }
      }
      const format = String(params.format || "text").toLowerCase();
      const out = {
        ...summarizeMessageHeader(header),
        headers,
        bodyText: readable ? plain : "",
        security,
        attachments: (attachments || []).map((a) => ({
          name: a.name || "",
          contentType: a.contentType || "",
          size: Number(a.size || 0),
          partName: a.partName || "",
        })),
      };
      if (readable && (format === "html" || format === "both")) out.bodyHtml = findPartText(full, "text/html");
      return out;
    }

    case "messages.thread": {
      const header = await resolveBridgeMessage(cfg, params);
      const limit = Math.max(1, Math.min(Number(params.limit) || 20, 50));
      const decrypt = params.decrypt === true;
      const primary = await buildMessageInput(header, { includeAttachmentText: true, decrypt });
      const msgs = await collectThreadMessages(primary, limit, { decrypt });
      return { messages: filterBridgeReadable(cfg, msgs).map(bridgeMessageInput) };
    }

    case "messages.open": {
      // Shows the mail in Thunderbird, e.g. to check its signature or decrypt it there.
      const header = await resolveBridgeMessage(cfg, params);
      if (!api.messageDisplay?.open) return { opened: false };
      await api.messageDisplay.open({ messageId: header.id, location: "tab", active: true });
      return { opened: true };
    }

    case "messages.actions":
      return rpcMessageActions(cfg, params);

    case "compose.draft":
    case "compose.send":
      return composeIdempotency.run(method, params.idempotencyKey, () => rpcCompose(cfg, method, params));

    case "calendar.list":
      requireExperiment(api.omaCalendar?.listCalendars, "calendar");
      return api.omaCalendar.listCalendars();

    case "calendar.events":
      requireExperiment(api.omaCalendar?.getEvents, "calendar");
      return api.omaCalendar.getEvents(params);

    case "calendar.create":
      requireExperiment(api.omaCalendar?.createEvent, "calendar");
      return api.omaCalendar.createEvent(params);

    case "calendar.update":
      requireExperiment(api.omaCalendar?.updateEvent, "calendar");
      return api.omaCalendar.updateEvent(params);

    case "calendar.delete":
      requireExperiment(api.omaCalendar?.deleteEvent, "calendar");
      return api.omaCalendar.deleteEvent(params);

    case "contacts.search":
      return rpcSearchContacts(params);

    case "contacts.upsert":
      return rpcUpsertContact(params);

    case "filters.list": {
      const data = await listNativeFiltersSafe(String(params.accountId || ""));
      return {
        ...data,
        accounts: filterBridgeReadable(cfg, data.accounts || []),
        filters: filterBridgeReadable(cfg, data.filters || []),
      };
    }

    case "filters.upsert": {
      requireExperiment(api.omaFilters?.upsertFilter, "filters");
      // No delete actions, target folders only inside the (writable) account.
      return api.omaFilters.upsertFilter(sanitizeBridgeFilterSpec(params));
    }

    case "filters.run":
      requireExperiment(api.omaFilters?.runFilters, "filters");
      return api.omaFilters.runFilters(
        String(params.accountId || "").trim(),
        Array.isArray(params.filterNames) ? params.filterNames.map(String) : undefined,
      );

    case "filters.openDialog":
      if (!api.omaFilters?.openFilterDialog) return { opened: false };
      return api.omaFilters.openFilterDialog(String(params.accountId || "").trim() || undefined);

    default:
      throw new PolicyError("invalid_argument", `unknown method ${method}`);
  }
}

// ---------------------------------------------------------------------------
// Host jobs: triage scans and archive runs run in the host; these methods only read and change
// mails for them. Encrypted mails are never decrypted here.
// ---------------------------------------------------------------------------

async function dispatchHostRPC(method, params) {
  const cfg = await hostConfigForBridge(0);
  checkHostRPC(cfg, method, params);
  switch (method) {
    case "host.triage.collect":
      return hostCollectScanMessages(cfg, params);
    case "host.archive.candidates": {
      const data = await collectCleanupCandidates(cfg, params);
      const localAcc = findLocalAccount(data.folderState.accounts);
      return {
        candidates: data.candidates,
        totalMatched: data.totalMatched,
        totalBytesAll: data.totalBytesAll,
        scannedFolders: data.scannedFolders,
        truncated: data.truncated,
        hasLocalAccount: Boolean(localAcc),
        localAccountName: localAcc?.name || "",
      };
    }
    case "host.archive.raw":
      return hostArchiveRaw(params);
    case "host.archive.finish":
      return hostArchiveFinish(cfg, params);
    default:
      throw new PolicyError("invalid_argument", `unknown host method ${method}`);
  }
}

// The inboxes of the triage accounts: all unread mails plus the newest `limit` read ones of the
// last `days` days, at most `max` in all, as AI input without decryption.
async function hostCollectScanMessages(cfg, params = {}) {
  const enabled = triageAccountIds(cfg);
  const accountId = String(params.accountId || "");
  const limit = Math.max(1, Math.min(Number(params.limit) || 20, 200));
  const days = Math.max(1, Math.min(Number(params.days) || 30, 365));
  const max = Math.max(1, Math.min(Number(params.max) || 300, 1000));
  const inboxes = findAllInboxes(await listAllFolders())
    .filter((f) => enabled.has(f.accountId) && (!accountId || f.accountId === accountId));
  const messages = [];
  for (const f of inboxes) {
    const headers = (await listRecentMessages(f, limit, days)).slice(0, Math.max(0, max - messages.length));
    for (let i = 0; i < headers.length; i += 6) {
      messages.push(...(await Promise.all(headers.slice(i, i + 6).map((h) => buildMessageInput(h, { decrypt: false })))));
    }
    if (messages.length >= max) break;
  }
  return { messages, scannedFolders: inboxes.length };
}

// Re-finds an archive candidate and checks that it is still where it was found.
async function liveArchiveCandidate(c, messageId = "") {
  const live = await resolveMessage({
    messageId: messageId || c.messageId,
    headerMessageId: c.headerMessageId,
    accountId: c.accountId,
    folderId: c.folderId,
  }, { strict: true });
  if (!live) throw new PolicyError("message_not_found");
  if (live.folder?.id !== c.folderId) throw new PolicyError("message_moved_meanwhile");
  return live;
}

// The stored message (encrypted mails stay encrypted) for a verified .eml backup.
async function hostArchiveRaw(c) {
  const live = await liveArchiveCandidate(c);
  const { base64, byteLength } = await readRawEmlBase64(live.id);
  return { messageId: String(live.id), base64, byteLength };
}

// Runs after the host verified the backup: moves the mail into Local Folders or deletes it.
async function hostArchiveFinish(cfg, params) {
  const c = params.candidate || {};
  const live = await liveArchiveCandidate(c, String(params.messageId || ""));
  const folderState = await listAccountFolders();
  if (params.action === "local") {
    const localAcc = findLocalAccount(folderState.accounts);
    if (!localAcc) throw new PolicyError("local_folder_required");
    const parts = ["OmaThunder-Archiv", ...buildArchiveRelativeParts(c.accountName, c.folderPath, c.date)];
    const target = await ensureLocalArchiveFolder(cfg, localAcc.id, parts, folderState);
    requireLocalArchiveMove(cfg, live, target, folderState.accounts);
    await api.messages.move([live.id], target.id);
    return { moved: true, targetFolder: target.path };
  }
  if (params.action === "delete") {
    requireArchiveDelete(cfg, live);
    await api.messages.delete([live.id], Boolean(params.skipTrash));
    return { deleted: true };
  }
  throw new PolicyError("invalid_argument", "action must be local or delete");
}

async function rpcListMessages(cfg, params) {
  const limit = Math.max(1, Math.min(Number(params.limit) || 50, 200));
  const offset = Math.max(0, Number(params.cursor) || 0);
  const days = Math.max(1, Math.min(Number(params.days) || 30, 365));
  const since = new Date(Date.now() - days * 86400000);
  const folderId = String(params.folderId || "").trim();
  const accountId = String(params.accountId || "").trim();
  const q = String(params.q || "").trim();
  const hasUnreadFilter = typeof params.unread === "boolean";

  const folders = await listAllFolders();
  let targets;
  if (folderId) {
    const folder = folders.find((f) => f.id === folderId);
    if (!folder) throw new PolicyError("not_found", `folder ${folderId}`);
    if (accountId && folder.accountId !== accountId) throw new PolicyError("invalid_argument", "folderId belongs to another account");
    requireBridgeReadable(cfg, folder.accountId);
    targets = [folder];
  } else {
    const readable = filterBridgeReadable(cfg, folders).filter((f) => !accountId || f.accountId === accountId);
    targets = readable.filter((f) => isInboxFolder(f));
    if (targets.length === 0) {
      targets = readable.filter((f) => f.type !== "trash" && f.type !== "junk").slice(0, 10);
    }
  }

  const headers = [];
  for (const f of targets) {
    const query = { folderId: f.id, fromDate: since };
    if (hasUnreadFilter) query.unread = params.unread;
    headers.push(...(await collectPagedMessages(api.messages.query(query), folderId ? 20 : 10)));
  }

  const filtered = headers
    .filter((h) => isBridgeReadable(cfg, h.folder?.accountId))
    .filter((h) => (!hasUnreadFilter || Boolean(!h.read) === params.unread) && matchMessageQuery(h, q))
    .sort((a, b) => new Date(b.date || 0) - new Date(a.date || 0));

  const slice = filtered.slice(offset, offset + limit).map(summarizeMessageHeader);
  const nextCursor = offset + slice.length < filtered.length ? String(offset + slice.length) : "";
  return { messages: slice, total: filtered.length, nextCursor };
}

async function rpcMessageActions(cfg, params) {
  const header = await resolveBridgeMessage(cfg, params);
  const accountId = header.folder?.accountId || "";
  requireBridgeWritable(cfg, accountId);

  // Validate the whole request before changing anything.
  const patch = {};
  if (params.read !== undefined) {
    if (typeof params.read !== "boolean") throw new PolicyError("invalid_argument", "read must be a boolean");
    patch.read = params.read;
  }
  if (params.flagged !== undefined) {
    if (typeof params.flagged !== "boolean") throw new PolicyError("invalid_argument", "flagged must be a boolean");
    patch.flagged = params.flagged;
  }
  for (const key of ["addTags", "removeTags"]) {
    if (params[key] !== undefined && !Array.isArray(params[key])) throw new PolicyError("invalid_argument", `${key} must be an array`);
  }
  if (Array.isArray(params.addTags) || Array.isArray(params.removeTags)) {
    const cur = new Set(Array.isArray(header.tags) ? header.tags : []);
    for (const t of params.addTags || []) if (t) cur.add(String(t));
    for (const t of params.removeTags || []) cur.delete(String(t));
    patch.tags = [...cur];
  }

  const moveKind = messageMoveKind(params);
  if (moveKind === "archive" && typeof api.messages.archive !== "function") {
    throw new PolicyError("invalid_argument", "this Thunderbird version cannot archive messages");
  }

  let target = null;
  let deleteToTrash = false;
  if (moveKind === "trash" || moveKind === "move") {
    const folders = await listAllFolders();
    if (moveKind === "trash") {
      target = folders.find((f) => f.accountId === accountId && f.type === "trash") || null;
      deleteToTrash = !target;
    } else {
      target = findFolderInAccount(folders, accountId, String(params.moveToFolderId || ""), String(params.moveToPath || ""));
      if (!target) {
        const elsewhere = params.moveToFolderId ? folders.find((f) => f.id === params.moveToFolderId) : null;
        if (elsewhere) throw new PolicyError("cross_account_move", "target folder belongs to another account");
        throw new PolicyError("not_found", "target folder");
      }
    }
    if (target) requireBridgeMovable(cfg, header, target);
  }

  if (Object.keys(patch).length > 0) await api.messages.update(header.id, patch);

  let movedTo = "";
  let newId = String(header.id);
  if (target && target.id !== header.folder?.id) {
    await api.messages.move([header.id], target.id);
    movedTo = target.path;
    // IMAP moves assign a new numeric ID.
    if (header.headerMessageId) {
      const post = await api.messages.query({ headerMessageId: header.headerMessageId, folderId: target.id }).catch(() => null);
      if (post?.messages?.length === 1) newId = String(post.messages[0].id);
    }
  } else if (deleteToTrash) {
    await api.messages.delete([header.id], false);
    movedTo = "Trash";
  } else if (moveKind === "archive") {
    // Thunderbird files the mail by the identity's archive settings (folder, yearly/monthly).
    await api.messages.archive([header.id]);
    movedTo = "Archive";
    newId = "";
  }
  return {
    ok: true,
    id: String(header.id),
    newId,
    messageId: normalizeHeaderMessageId(header.headerMessageId),
    movedTo,
  };
}

// compose.draft / compose.send (CONTRACT §5). Policy is checked against the account owning the
// identity that is actually used; that identity is passed to Thunderbird explicitly.
const composeIdempotency = createIdempotencyCache({ ttlMs: 15 * 60 * 1000 });
const REPLY_TYPES = new Set(["replyToSender", "replyToList", "replyToAll"]);

async function rpcCompose(cfg, method, params) {
  const isSend = method === "compose.send";
  const openWindow = !isSend && params.openWindow === true;
  if (!api.compose?.beginNew) throw new PolicyError("feature_disabled:compose", "compose API unavailable");

  let replyHeader = null;
  if (params.replyToMessageId) {
    replyHeader = await resolveBridgeMessage(cfg, {
      id: params.replyToMessageId,
      expectedMessageId: params.replyToExpectedMessageId || "",
    });
  }
  const replyType = String(params.replyType || "replyToSender");
  if (replyHeader && !REPLY_TYPES.has(replyType)) throw new PolicyError("invalid_argument", `unknown replyType ${replyType}`);

  const accounts = (await api.accounts.list(false)) || [];
  let defaultAccountId = "";
  try {
    defaultAccountId = (await api.accounts.getDefault?.())?.id || "";
  } catch (_) {
    // older Thunderbird
  }
  const used = pickComposeIdentity(accounts, {
    identityId: String(params.identityId || ""),
    accountId: String(params.accountId || ""),
    preferredAccountIds: [replyHeader?.folder?.accountId, defaultAccountId].filter(Boolean),
  });
  if (isSend) requireBridgeSend(cfg, used.accountId);
  else if (openWindow) requireBridgeReadable(cfg, used.accountId); // only a visible window, nothing saved
  else requireBridgeWritable(cfg, used.accountId); // silent draft save writes into the Drafts folder

  const details = { identityId: used.identityId };
  if (params.to) details.to = params.to;
  if (params.cc) details.cc = params.cc;
  if (params.bcc) details.bcc = params.bcc;
  if (params.subject !== undefined) details.subject = String(params.subject);
  if (params.body !== undefined) {
    if (params.isHtml) details.body = String(params.body);
    else details.plainTextBody = String(params.body);
  }

  const sign = String(params.sign || "");
  if (sign && sign !== "on" && sign !== "off") throw new PolicyError("invalid_argument", 'sign must be "on", "off" or empty');
  // A reply quotes the original: a reply to an encrypted mail stays encrypted.
  let requireEncryption = false;
  if (replyHeader) {
    const original = await getFullMessage(replyHeader.id, false);
    requireEncryption = describeMessageSecurity(original, { decrypt: false, bodyText: extractPlainText(original) }).encrypted;
  }

  if (isSend && !api.compose?.sendMessage) throw new PolicyError("feature_disabled:compose", "compose.sendMessage unavailable");
  const tab = replyHeader
    ? await api.compose.beginReply(replyHeader.id, replyType, details)
    : await api.compose.beginNew(details);
  const base = { ok: true, identityId: used.identityId, accountId: used.accountId };

  let security;
  try {
    security = await applyComposeSecurity(tab.id, { sign, requireEncryption });
  } catch (err) {
    await api.tabs?.remove?.(tab.id).catch(() => {});
    throw err;
  }

  if (isSend) {
    // On failure (e.g. a recipient key is missing) the window stays open, so nothing is lost.
    const sent = await api.compose.sendMessage(tab.id, { mode: "sendNow" });
    return { ...base, sent, security };
  }
  if (openWindow) return { ...base, tabId: tab.id, windowOpened: true, security };
  // Whether Thunderbird stores this draft encrypted cannot be checked from here, so a reply to an
  // encrypted mail is never saved silently: its window stays open instead.
  if (requireEncryption) return { ...base, tabId: tab.id, windowOpened: true, reason: "encrypted_reply", security };
  let saved = null;
  if (api.compose?.saveMessage) {
    saved = await api.compose.saveMessage(tab.id, { mode: "draft" });
    await api.tabs?.remove?.(tab.id).catch(() => {});
  }
  return { ...base, tabId: tab.id, saved, security };
}

// Applies the requested signing and the encryption a reply needs to a compose tab and checks that
// Thunderbird took it. Returns { technology, signed, encrypted } (technology "" = identity without key).
async function applyComposeSecurity(tabId, { sign = "", requireEncryption = false } = {}) {
  const details = await api.compose.getComposeDetails(tabId).catch(() => null);
  const tech = details?.selectedEncryptionTechnology;
  const plan = planComposeSecurity(tech, { sign, requireEncryption });
  if (plan.patch) {
    await api.compose.setComposeDetails(tabId, { selectedEncryptionTechnology: plan.patch });
    const after = await api.compose.getComposeDetails(tabId).catch(() => null);
    verifyComposeSecurity(after?.selectedEncryptionTechnology, plan.result);
  }
  return plan.result;
}

function normalizeContactNode(node, fallbackBookId = "") {
  const p = node?.properties || {};
  const addrParts = [p.WorkAddress, p.WorkZipCode, p.WorkCity, p.WorkCountry].filter(Boolean);
  return {
    id: String(node?.id || ""),
    addressBookId: String(node?.parentId || fallbackBookId || ""),
    displayName: String(p.DisplayName || p.displayName || ""),
    firstName: String(p.FirstName || p.firstName || ""),
    lastName: String(p.LastName || p.lastName || ""),
    email: String(p.PrimaryEmail || p.primaryEmail || ""),
    secondEmail: String(p.SecondEmail || p.secondEmail || ""),
    phone: String(p.WorkPhone || p.HomePhone || p.workPhone || ""),
    mobile: String(p.CellularNumber || p.cellularNumber || ""),
    company: String(p.Company || p.company || ""),
    jobTitle: String(p.JobTitle || p.jobTitle || ""),
    website: String(p.WebPage1 || p.webPage1 || ""),
    address: addrParts.join(", "),
    notes: String(p.Notes || p.notes || ""),
  };
}

async function rpcSearchContacts(params = {}) {
  if (!api.addressBooks || !api.contacts) {
    return { supported: false, addressBooks: [], contacts: [] };
  }
  const books = (await api.addressBooks.list(false).catch(() => [])) || [];
  const addressBooks = books.map((b) => ({
    id: String(b.id || ""),
    name: String(b.name || ""),
    readOnly: Boolean(b.readOnly),
    type: String(b.type || ""),
  }));

  const q = String(params?.q || params?.query || "").trim().toLowerCase();
  const seen = new Map();

  if (q && api.contacts.quickSearch) {
    let hits = await api.contacts.quickSearch(q).catch(() => null);
    if (!hits) hits = await api.contacts.quickSearch({ searchString: q }).catch(() => []);
    for (const c of hits || []) {
      const norm = normalizeContactNode(c);
      if (norm.id && !seen.has(norm.id)) seen.set(norm.id, norm);
    }
  }

  if (seen.size === 0) {
    for (const b of books) {
      const list = (await api.contacts.list(b.id).catch(() => [])) || [];
      for (const c of list.slice(0, 300)) {
        const norm = normalizeContactNode(c, b.id);
        if (!norm.id || seen.has(norm.id)) continue;
        if (q) {
          const hay = [norm.displayName, norm.firstName, norm.lastName, norm.email, norm.secondEmail, norm.company]
            .join(" ")
            .toLowerCase();
          if (!hay.includes(q)) continue;
        }
        seen.set(norm.id, norm);
      }
    }
  }

  return {
    supported: true,
    addressBooks,
    contacts: [...seen.values()],
  };
}

async function rpcUpsertContact(params = {}) {
  if (!api.addressBooks || !api.contacts) {
    throw new PolicyError("feature_disabled:contacts", "addressBooks API unavailable");
  }
  const c = params?.contact && typeof params.contact === "object" ? params.contact : {};
  const books = (await api.addressBooks.list(false).catch(() => [])) || [];
  const writableBooks = books.filter((b) => !b.readOnly);
  if (writableBooks.length === 0) {
    throw new PolicyError("not_found", "no writable address book");
  }

  let bookId = String(params?.addressBookId || "").trim();
  if (!bookId || !writableBooks.some((b) => b.id === bookId)) {
    bookId = writableBooks[0].id;
  }

  const fullName = String(c.displayName || [c.firstName, c.lastName].filter(Boolean).join(" ") || c.email || "").trim();
  const desired = {
    DisplayName: fullName,
    FirstName: String(c.firstName || "").trim(),
    LastName: String(c.lastName || "").trim(),
    PrimaryEmail: String(c.email || "").trim(),
    WorkPhone: String(c.phone || "").trim(),
    CellularNumber: String(c.mobile || "").trim(),
    Company: String(c.company || "").trim(),
    JobTitle: String(c.jobTitle || "").trim(),
    WebPage1: String(c.website || "").trim(),
    WorkAddress: String(c.address || "").trim(),
    Notes: String(c.notes || "").trim(),
  };

  const contactId = String(params?.contactId || "").trim();
  if (contactId) {
    const existing = await api.contacts.get(contactId).catch(() => null);
    if (!existing) throw new PolicyError("not_found", `contact ${contactId}`);
    if (books.find((b) => b.id === existing.parentId)?.readOnly) {
      throw new PolicyError("read_only", "the contact's address book is read-only");
    }
    // Only fill fields that are still empty. contacts.update() merges the given properties into the
    // contact; the existing properties (which include the full `vCard`) must NOT be sent back,
    // because a `vCard` property makes Thunderbird ignore every other property of the update.
    const curProps = existing.properties || {};
    const changes = {};
    for (const [k, v] of Object.entries(desired)) {
      if (v && !String(curProps[k] || "").trim()) changes[k] = v;
    }
    if (Object.keys(changes).length > 0) await api.contacts.update(contactId, changes);
    const updated = await api.contacts.get(contactId).catch(() => null);
    return {
      ok: true,
      action: "enriched",
      changed: Object.keys(changes),
      contact: normalizeContactNode(updated || { id: contactId, parentId: existing.parentId, properties: { ...curProps, ...changes } }, existing.parentId || bookId),
    };
  }

  const createProps = {};
  for (const [k, v] of Object.entries(desired)) {
    if (v) createProps[k] = v;
  }
  if (Object.keys(createProps).length === 0) throw new PolicyError("invalid_argument", "contact has no data");
  const created = await api.contacts.create(bookId, createProps);
  let node = null;
  if (typeof created === "string") {
    node = await api.contacts.get(created).catch(() => ({ id: created, parentId: bookId, properties: createProps }));
  } else if (created && typeof created === "object") {
    node = created;
  } else {
    node = { id: "", parentId: bookId, properties: createProps };
  }
  return {
    ok: true,
    action: "imported",
    contact: normalizeContactNode(node, bookId),
  };
}

// Opens the native port at startup (not only on the first settings visit), so the host, its jobs
// and its local bridge run as soon as Thunderbird does. Lost connections are retried
// (scheduleNativeReconnect); a host that is not installed is not.
async function bootstrap() {
  try {
    await loadConfig();
  } catch (err) {
    console.info("OmaThunder: host not reachable:", err?.message || err);
  }
}

bootstrap();
