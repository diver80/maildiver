/* global browser, messenger, OmaCore, OmaStandalone */
const api = typeof messenger !== "undefined" ? messenger : browser;
// Pure decision logic (unit-tested in tests/core.test.js); this file only talks to Thunderbird.
const {
  PolicyError, isInboxFolder, findFolderInAccount, requireFeature, triageAccountIds, kanbanAccountIds,
  requireWritable, requireMovable, messageMoveKind, requireBridgeSend, summarizeMessageHeader, matchMessageQuery,
  acceptDirectHit, pickMessageCandidate, selectScanFolders, newestSince,
  bridgeSettings, isBridgeReadable, requireBridgeReadable, requireBridgeWritable, requireBridgeMovable,
  checkBridgeRPC, filterBridgeReadable, sanitizeBridgeFilterSpec, bridgeErrorString, normalizeHeaderMessageId,
  parseBridgeMessageRef, pickBridgeMessage, pickComposeIdentity, createIdempotencyCache, buildMailNewEvent,
  createBridgeEventQueue, nativeReconnectDelay, isNativeHostMissingError,
  selectScanMessages, mergeTags, normalizeSubjectForThread, parseMessageIds, selectThread, draftOwnText,
  replaceDraftOwn, htmlToText, isLocalAccount, findLocalAccount, buildArchiveRelativeParts,
  requireLocalArchiveMove, requireArchiveDelete, matchArchiveCandidate,
} = OmaCore;
// Must match ProtocolVersion in internal/native/handler.go.
const PROTOCOL = 3;
const HOST_NAME = "dev.omathunder.host";
const STANDALONE_CFG_KEY = "oma_standalone_config";
const STANDALONE_SECRETS_KEY = "oma_standalone_secrets";
const standaloneMemory = { config: null, secrets: { geminiApiKey: "", anthropicApiKey: "" } };

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
// Last known feature flags; refreshed from the host config or standalone storage.
let features = { theme: false, assistant: false, triage: false };
// Whether the theme also recolors message bodies and the compose editor (theme.styleBody).
let styleBody = false;

async function storageGet(key) {
  if (!api.storage?.local?.get) return null;
  try {
    const res = await api.storage.local.get(key);
    return res?.[key] ?? null;
  } catch (_) {
    return null;
  }
}

async function storageSet(key, val) {
  if (!api.storage?.local?.set) return;
  await api.storage.local.set({ [key]: val }).catch(() => {});
}

async function loadStandaloneConfig() {
  const stored = await storageGet(STANDALONE_CFG_KEY);
  const merged = OmaStandalone.mergeStandaloneConfig(stored || standaloneMemory.config, null);
  standaloneMemory.config = merged;
  return merged;
}

async function saveStandaloneConfig(patch) {
  const cur = await loadStandaloneConfig();
  const next = OmaStandalone.mergeStandaloneConfig(cur, patch);
  standaloneMemory.config = next;
  await storageSet(STANDALONE_CFG_KEY, next);
  return next;
}

async function loadStandaloneSecrets() {
  const stored = await storageGet(STANDALONE_SECRETS_KEY);
  const merged = {
    geminiApiKey: String(stored?.geminiApiKey ?? standaloneMemory.secrets.geminiApiKey ?? ""),
    anthropicApiKey: String(stored?.anthropicApiKey ?? standaloneMemory.secrets.anthropicApiKey ?? ""),
  };
  standaloneMemory.secrets = merged;
  return merged;
}

async function saveStandaloneSecrets(patch) {
  if (!patch || typeof patch !== "object") return loadStandaloneSecrets();
  const cur = await loadStandaloneSecrets();
  const next = { ...cur };
  if (typeof patch.geminiApiKey === "string") next.geminiApiKey = patch.geminiApiKey.trim();
  if (typeof patch.anthropicApiKey === "string") next.anthropicApiKey = patch.anthropicApiKey.trim();
  standaloneMemory.secrets = next;
  await storageSet(STANDALONE_SECRETS_KEY, next);
  return next;
}

function buildStandaloneTheme(cfg) {
  const presetName = cfg?.theme?.preset || cfg?.theme?.darkFallback || "tokyo-night";
  return OmaStandalone.buildThemePayload(OmaStandalone.getPreset(presetName), "standalone-preset");
}

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
      handleThunderbirdRPC(port, msg.id, msg.result.method, msg.result.params).catch(() => {});
      return;
    }
    if (msg && msg.event === "theme_updated" && msg.result) {
      if (!features.theme) return;
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
      // Plain get_config (not loadConfig): a failed attempt must not switch the theme to standalone.
      const cfg = await callNative("get_config", {}, 15000);
      hostConnected = true;
      rememberHostConfig(cfg);
      await syncFeatures(cfg, null, { standalone: false });
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
  if (!payload || !features.theme) return;
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

// Applies the feature flags of a host or standalone config: switches the theme on/off and caches flags.
async function syncFeatures(cfg, themePayload = null, { standalone = false } = {}) {
  const next = {
    theme: Boolean(cfg?.features?.theme),
    assistant: Boolean(cfg?.features?.assistant),
    triage: Boolean(cfg?.features?.triage),
  };
  const themeWasOn = features.theme;
  const styleBodyChanged = styleBody !== Boolean(cfg?.theme?.styleBody);
  styleBody = Boolean(cfg?.theme?.styleBody);
  features = next;
  if (!next.theme) {
    if (themeWasOn || currentThemePayload) await removeTheme();
    return;
  }
  let payload = themePayload;
  if (!payload) {
    if (standalone || !hostConnected) {
      payload = buildStandaloneTheme(cfg);
    } else if (!themeWasOn || styleBodyChanged) {
      payload = await callNative("get_theme").catch(() => null);
    }
  }
  if (payload) await applyThemePayload(payload);
}

// ---------------------------------------------------------------------------
// Safety layer: every change to mails, folders or tags goes through these helpers.
// ---------------------------------------------------------------------------

async function loadConfig() {
  try {
    const cfg = await callNative("get_config");
    hostConnected = true;
    rememberHostConfig(cfg);
    await syncFeatures(cfg, null, { standalone: false });
    return cfg;
  } catch (err) {
    if (!OmaStandalone.isNativeHostUnavailableError(err)) throw err;
    hostConnected = false;
    const cfg = await loadStandaloneConfig();
    await syncFeatures(cfg, null, { standalone: true });
    return cfg;
  }
}

async function runAnalyzeThread(cfg, req) {
  if (hostConnected) {
    try {
      return await callNative("analyze_thread", req, 120000);
    } catch (err) {
      if (!OmaStandalone.isNativeHostUnavailableError(err)) throw err;
      hostConnected = false;
    }
  }
  const apiKeys = await loadStandaloneSecrets();
  return OmaStandalone.analyzeStandalone({ cfg, apiKeys, req });
}

const mailbox = {
  async move(cfg, header, targetFolder) {
    requireMovable(cfg, header, targetFolder);
    await api.messages.move([header.id], targetFolder.id);
  },
  async addTags(cfg, header, tagDefs, { replacePrefix = "" } = {}) {
    requireWritable(cfg, header?.folder?.accountId);
    const known = new Set((await api.messages.tags.list().catch(() => [])).map((t) => t.key));
    for (const def of tagDefs) {
      if (!known.has(def.key) && api.messages.tags.create) {
        await api.messages.tags.create(def.key, def.label, def.color || "#7aa2f7");
      }
    }
    // Keep the user's own tags; only replace OmaThunder's status tags when asked to.
    const merged = mergeTags(header.tags, tagDefs.map((d) => d.key), replacePrefix);
    await api.messages.update(header.id, { tags: merged });
  },
  async markRead(cfg, header, read = true) {
    requireWritable(cfg, header?.folder?.accountId);
    await api.messages.update(header.id, { read: Boolean(read) });
  },
  async createFolder(cfg, accountId, parentId, name) {
    requireWritable(cfg, accountId);
    return api.folders.create(parentId, name);
  },
};

async function ensureSpacesTab() {
  const tabUrl = api.runtime.getURL("ui/space.html");
  try {
    if (api.spaces?.create) {
      const existing = await api.spaces.query?.({ name: "omathunder" }).catch(() => []);
      if (!existing || existing.length === 0) {
        await api.spaces.create("omathunder", tabUrl, {
          title: "OmaThunder",
          defaultIcons: { 16: "icons/icon.svg", 32: "icons/icon.svg" },
        });
      }
      return;
    }
    if (api.spacesToolbar?.addButton) {
      await api.spacesToolbar.addButton("omathunder", {
        title: "OmaThunder",
        defaultIcons: { 16: "icons/icon.svg", 32: "icons/icon.svg" },
        url: tabUrl,
      });
    }
  } catch (err) {
    console.warn("OmaThunder spaces registration note:", err);
  }
}

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

// Creates missing folders of a path inside one account (only when writes are allowed there).
async function ensureFolderPath(cfg, accountId, folderPath) {
  const clean = (folderPath || "").replace(/^\/+|\/+$/g, "");
  if (!clean || !accountId) return null;
  const { folders, roots } = await listAccountFolders();
  const existing = findFolderInAccount(folders, accountId, "", clean);
  if (existing) return existing;

  requireWritable(cfg, accountId);
  if (!api.folders?.create) return null;
  const targetAccountId = accountId;

  let parentId = roots.get(targetAccountId);
  if (!parentId) return null;

  const segments = clean.split("/").filter(Boolean);
  let builtPath = "";
  let currentFolder = null;
  const refreshed = [...folders];

  for (const seg of segments) {
    builtPath = builtPath ? `${builtPath}/${seg}` : seg;
    let found = findFolderInAccount(refreshed, targetAccountId, "", builtPath);
    if (!found) {
      const created = await mailbox.createFolder(cfg, targetAccountId, parentId, seg).catch(() => null);
      if (!created) return null;
      found = {
        id: created.id,
        accountId: created.accountId || targetAccountId,
        name: created.name || seg,
        path: (created.path || builtPath).replace(/^\/+/, ""),
        type: created.type || "",
      };
      refreshed.push(found);
    }
    parentId = found.id;
    currentFolder = found;
  }
  return currentFolder;
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

async function buildMessageInput(header, { includeAttachmentText = false } = {}) {
  const full = await api.messages.getFull(header.id).catch(() => null);
  const attachments = await api.messages.listAttachments(header.id).catch(() => []);
  const headers = {};
  if (full?.headers) {
    for (const [k, vals] of Object.entries(full.headers)) {
      if (Array.isArray(vals) && vals.length > 0) headers[k] = String(vals[0]);
    }
  }
  let bodyText = extractPlainText(full).slice(0, 6000);
  if (includeAttachmentText) {
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
  };
}

function errorCode(err) {
  return err?.code || err?.message || String(err);
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

// Scans only accounts enabled for triage: their enabled kanban folders plus rule folders (with subfolders).
async function collectScanMessages(cfg, accountId = "") {
  const limit = cfg?.mailbox?.scanLimit || 20;
  const days = cfg?.mailbox?.scanDays || 30;
  const folders = await listAllFolders();
  let scanFolders = selectScanFolders(cfg, folders);
  if (accountId) {
    scanFolders = scanFolders.filter((f) => f.accountId === accountId);
  }
  const messages = [];
  for (const f of scanFolders) {
    const headers = await listRecentMessages(f, limit, days);
    for (let i = 0; i < headers.length; i += 6) {
      const batch = await Promise.all(headers.slice(i, i + 6).map((h) => buildMessageInput(h)));
      messages.push(...batch);
    }
  }
  return { messages, folders, scannedFolders: scanFolders.length };
}

// Executes one move the host planned. Re-checks everything against the live mailbox first.
async function executePlannedMove(cfg, plan, folders) {
  const live = await resolveMessage({
    messageId: plan.messageId,
    headerMessageId: plan.headerMessageId,
    folderId: plan.sourceFolderId,
    accountId: plan.accountId,
  });
  if (!live) throw new PolicyError("message_not_found");
  if (live.folder?.id !== plan.sourceFolderId || !isInboxFolder(live.folder)) throw new PolicyError("message_moved_meanwhile");
  if (cfg.mailbox?.autoMoveOnlyUnread && live.read) throw new PolicyError("message_read_meanwhile");
  const target = findFolderInAccount(folders, plan.accountId, plan.targetFolderId, plan.targetFolder);
  if (!target) throw new PolicyError("target_folder_missing");

  await mailbox.move(cfg, live, target);

  // IMAP moves assign a new MessageHeader.id; look it up so the board keeps working this session.
  let newId = String(live.id);
  const post = await api.messages.query({ headerMessageId: plan.headerMessageId, folderId: target.id }).catch(() => null);
  if (post?.messages?.[0]?.id) newId = String(post.messages[0].id);
  return {
    messageId: newId,
    oldMessageId: String(plan.messageId),
    headerMessageId: plan.headerMessageId,
    accountId: plan.accountId,
    ruleId: plan.ruleId,
    originalFolder: plan.sourceFolder,
    originalFolderId: plan.sourceFolderId,
    targetFolder: target.path,
    targetFolderId: target.id,
  };
}

function uiLanguage(cfg, requested = "") {
  if (requested === "de" || requested === "en") return requested;
  if (cfg?.language === "de" || cfg?.language === "en") return cfg.language;
  return (api.i18n?.getUILanguage?.() || "en").toLowerCase().startsWith("de") ? "de" : "en";
}

async function pruneStaleReadDone(cfg, scannedMessages, accountId = "") {
  const scannedIds = new Set((scannedMessages || []).map((m) => String(m.id)));
  const scannedThreadIds = new Set((scannedMessages || []).map((m) => m.threadId).filter(Boolean));
  const enabledKanban = kanbanAccountIds(cfg);
  const snap = await callNative("get_state").catch(() => null);
  const staleDoneIds = [];
  for (const [key, cls] of Object.entries(snap?.state?.classifications || {})) {
    if ((cls.kanbanColumn || "incoming") !== "done") continue;
    if (accountId && cls.accountId !== accountId) continue;
    if (!accountId && !enabledKanban.has(cls.accountId)) continue;
    if (scannedIds.has(key) || (cls.threadId && scannedThreadIds.has(cls.threadId))) continue;
    const live = await resolveMessage({
      messageId: key,
      headerMessageId: cls.threadId || "",
      accountId: cls.accountId || "",
      folderId: cls.folderId || "",
    }, { strict: false }).catch(() => null);
    if (live && live.read) staleDoneIds.push(key);
  }
  if (staleDoneIds.length > 0) {
    await callNative("remove_classifications", { ids: staleDoneIds }).catch(() => null);
  }
}

let triageQueue = Promise.resolve();

function runTriage(requestedLanguage = "", accountId = "") {
  const job = triageQueue.catch(() => {}).then(() => runTriageInternal(requestedLanguage, accountId));
  triageQueue = job.catch(() => {});
  return job;
}

async function runTriageInternal(requestedLanguage = "", accountId = "") {
  const cfg = await loadConfig();
  requireFeature(cfg, "triage");
  if (triageAccountIds(cfg).size === 0) throw new PolicyError("no_triage_accounts");

  const { messages, folders, scannedFolders } = await collectScanMessages(cfg, accountId);
  // Classify in small batches: keeps each native message small and each call within its timeout.
  let res = { results: [], plannedMoves: [], skippedAccounts: 0 };
  const batchSize = 5;
  // Unread and newest first, so they get the AI budget before older, already read mails.
  messages.sort((a, b) => Number(b.unread) - Number(a.unread) || new Date(b.date || 0) - new Date(a.date || 0));
  const scanId = `scan-${Date.now()}`; // lets the host apply one AI budget across all batches
  for (let i = 0; i < messages.length || i === 0; i += batchSize) {
    const part = await callNative("classify_batch", { scanId, language: uiLanguage(cfg, requestedLanguage), messages: messages.slice(i, i + batchSize) }, 300000);
    res = {
      ...part,
      results: [...res.results, ...(part.results || [])],
      plannedMoves: [...res.plannedMoves, ...(part.plannedMoves || [])],
      skippedAccounts: res.skippedAccounts + (part.skippedAccounts || 0),
      aiCalls: (res.aiCalls || 0) + (part.aiCalls || 0),
      aiSkipped: (res.aiSkipped || 0) + (part.aiSkipped || 0),
      classifyErrors: (res.classifyErrors || 0) + (part.classifyErrors || 0),
      classifyError: res.classifyError || part.classifyError || "",
    };
  }
  const planned = res.plannedMoves || [];
  const report = {
    readOnly: cfg.mailbox?.readOnly !== false,
    scanId,
    scannedFolders,
    scannedMessages: messages.length,
    planned,
    moved: [],
    failed: [],
    aiEnabled: res.aiEnabled !== false,
    aiCalls: res.aiCalls || 0,
    aiSkipped: res.aiSkipped || 0,
    classifyErrors: res.classifyErrors || 0,
    classifyError: res.classifyError || "",
  };
  // Classification can take minutes. Re-read the settings so a switch to read-only or a removed
  // account during the scan takes effect before anything is moved.
  const liveCfg = report.readOnly ? cfg : await loadConfig();
  if (liveCfg.mailbox?.readOnly !== false || !liveCfg.features?.triage) report.readOnly = true;
  if (!report.readOnly) {
    const recorded = [];
    for (const plan of planned) {
      try {
        recorded.push({ ...(await executePlannedMove(liveCfg, plan, folders)), scanId });
        report.moved.push(plan);
      } catch (err) {
        report.failed.push({ ...plan, error: errorCode(err) });
      }
    }
    if (recorded.length > 0) {
      await callNative("record_moves", { moves: recorded }).catch((err) => {
        report.recordError = errorCode(err);
      });
    }
  }
  await pruneStaleReadDone(liveCfg, messages, accountId).catch(() => {});
  return { ...res, moveReport: report };
}

async function findDisplayedHeader() {
  const tabs = [
    ...(await api.tabs.query({ active: true, currentWindow: true }).catch(() => [])),
    ...(await api.tabs.query({ mailTab: true }).catch(() => [])),
    ...(await api.tabs.query({ type: "messageDisplay" }).catch(() => [])),
  ];
  const seen = new Set();
  for (const tab of tabs) {
    if (seen.has(tab.id)) continue;
    seen.add(tab.id);
    const msg = await api.messageDisplay.getDisplayedMessage(tab.id).catch(() => null);
    if (msg) return msg;
  }
  return null;
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

async function getSelectedMessageHeaders(preferredTabId = null) {
  if (api.mailTabs?.getSelectedMessages) {
    const mailTabs = preferredTabId
      ? [{ id: preferredTabId }]
      : [
          ...(await api.tabs.query({ active: true, currentWindow: true, mailTab: true }).catch(() => [])),
          ...(await api.tabs.query({ mailTab: true }).catch(() => [])),
        ];
    const seenTabs = new Set();
    for (const tab of mailTabs) {
      if (!tab?.id || seenTabs.has(tab.id)) continue;
      seenTabs.add(tab.id);
      const list = await api.mailTabs.getSelectedMessages(tab.id).catch(() => null);
      const msgs = await collectMessageList(list, MAX_SELECTED_MESSAGES);
      if (msgs.length > 0) return msgs;
    }
    if (!preferredTabId) {
      const list = await api.mailTabs.getSelectedMessages().catch(() => null);
      const msgs = await collectMessageList(list, MAX_SELECTED_MESSAGES);
      if (msgs.length > 0) return msgs;
    }
  }
  const displayed = await findDisplayedHeader();
  return displayed ? [displayed] : [];
}

async function getActiveDisplayedMessage() {
  const header = (await findDisplayedHeader()) || (await getSelectedMessageHeaders())[0] || null;
  return header ? buildMessageInput(header) : null;
}

// Collects the conversation of a mail. Membership is decided by the References / In-Reply-To
// headers; the subject search only supplies candidates, so unrelated mails with the same
// subject ("Rechnung", "Angebot") never end up in the AI prompt.
async function collectThreadMessages(primary, max = 8) {
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
  for (const h of [...headers.values()].slice(0, Math.min(100, Math.max(24, want * 2)))) candidates.push(await buildMessageInput(h));
  return selectThread(primary, candidates, want);
}

// Own text of the compose window used last (quote, forward and signature are left out).
async function getActiveComposeDraft() {
  const tabs = await api.tabs.query({ type: "messageCompose" }).catch(() => []);
  if (!tabs || tabs.length === 0) return null;
  const tab = tabs.find((t) => t.active) || tabs[tabs.length - 1];
  const details = await api.compose.getComposeDetails(tab.id).catch(() => null);
  if (!details) return null;
  const body = details.isPlainText ? details.plainTextBody || "" : details.body || "";
  return { tabId: tab.id, subject: details.subject || "", body: draftOwnText(body, Boolean(details.isPlainText)) };
}

// Kanban moves always update OmaThunder's own state. Tags and column folders in Thunderbird
// are only touched when writes are allowed for the mail's account.
async function applyKanbanSideEffects(cfg, messageId, column, { state = null, folders = null } = {}) {
  if (cfg?.mailbox?.readOnly !== false) return { skipped: "read_only" };
  const snapState = state || (await callNative("get_state").catch(() => null))?.state;
  const cls = snapState?.classifications?.[String(messageId)];
  if (!cls) return { skipped: "message_not_found" };
  const msg = await resolveMessage({
    messageId,
    headerMessageId: cls.threadId,
    accountId: cls.accountId,
    folderId: cls.folderId,
  });
  if (!msg) return { skipped: "message_not_found" };

  await mailbox.addTags(cfg, msg, [{ key: `oma-${column}`, label: `Oma:${column}` }], { replacePrefix: "oma-" });

  const rule = cls.matchedRuleId ? (cfg.rules || []).find((r) => r.id === cls.matchedRuleId) : null;
  const mappedFolder = rule?.columnFolders?.[column];
  if (mappedFolder) {
    const folderList = folders || (await listAccountFolders()).folders;
    const target = findFolderInAccount(folderList, msg.folder?.accountId, "", mappedFolder);
    if (!target) return { applied: true, skipped: "target_folder_missing" };
    if (target.id !== msg.folder?.id) await mailbox.move(cfg, msg, target);
  }
  return { applied: true };
}

async function applyKanbanSideEffectsBatch(cfg, state, messageIds, column) {
  if (cfg?.mailbox?.readOnly !== false) return { skipped: "read_only", applied: 0, failed: 0 };
  const ids = (messageIds || []).filter(Boolean);
  if (ids.length === 0) return { applied: 0, failed: 0 };
  const { folders } = await listAccountFolders().catch(() => ({ folders: null }));
  let applied = 0;
  let failed = 0;
  let firstSkipped = "";
  for (const id of ids) {
    try {
      const r = await applyKanbanSideEffects(cfg, id, column, { state, folders });
      if (r?.applied) applied++;
      else if (r?.skipped && !firstSkipped) firstSkipped = r.skipped;
    } catch (err) {
      failed++;
      if (!firstSkipped) firstSkipped = errorCode(err);
    }
  }
  return { applied, failed, ...(firstSkipped ? { skipped: firstSkipped } : {}) };
}

// Writes text into an open compose window. Only the user's own part of the draft is replaced;
// signature and quoted original stay. This is the user's own unsent draft, so it works in read-only mode.
async function writeIntoCompose(tabId, rawText, subject = "") {
  const text = String(rawText || "").replace(/\r\n?/g, "\n");
  const details = await api.compose.getComposeDetails(tabId);
  const patch = details.isPlainText
    ? { plainTextBody: replaceDraftOwn(details.plainTextBody || "", true, text) }
    : { body: replaceDraftOwn(details.body || "", false, text) };
  if (subject && subject.trim()) {
    patch.subject = subject.trim();
  }
  await api.compose.setComposeDetails(tabId, patch);
}

async function applyProposalInThunderbird(proposal, { replyAll = false } = {}) {
  if (!proposal) throw new Error("Proposal missing");
  const cfg = await loadConfig();
  requireFeature(cfg, "assistant");
  const live = await resolveMessage({ messageId: proposal.messageId, headerMessageId: proposal.threadId || "" });
  if (!live) throw new PolicyError("message_not_found");

  if (proposal.kind === "draft_reply") {
    // Opens a normal reply window (with quote and signature) and puts the text above the quote.
    // Nothing is saved or sent without the user. Allowed in read-only mode.
    const tab = await api.compose.beginReply(live.id, replyAll ? "replyToAll" : "replyToSender");
    try {
      await writeIntoCompose(tab.id, proposal.body || "");
    } catch (_) {
      await new Promise((resolve) => setTimeout(resolve, 700)); // editor not ready yet
      await writeIntoCompose(tab.id, proposal.body || "").catch(() => {
        throw new PolicyError("draft_insert_failed");
      });
    }
  } else if (proposal.kind === "add_tags" && Array.isArray(proposal.tags) && proposal.tags.length > 0) {
    const defs = proposal.tags.map((tag) => ({ key: tag.toLowerCase().replace(/[^a-z0-9_-]+/g, "-"), label: tag }));
    await mailbox.addTags(cfg, live, defs);
  } else if (proposal.kind === "move_folder" && proposal.folderPath) {
    const { folders } = await listAccountFolders();
    const target = findFolderInAccount(folders, live.folder?.accountId, "", proposal.folderPath);
    if (!target) throw new PolicyError("target_folder_missing");
    await mailbox.move(cfg, live, target);
  } else {
    throw new PolicyError("proposal_unsupported");
  }
  return callNative("update_proposal", { id: proposal.id, status: "applied" });
}

// Moves mails that a deleted rule or undone scan had auto-moved back to where they came from.
// Only touches mails that are still in the folder OmaThunder moved them to.
async function revertRuleMoves(cfg, revertMoves) {
  const { folders } = await listAccountFolders();
  const report = { reverted: 0, revertedMoves: [], skipped: [], failed: [] };
  for (const m of revertMoves) {
    try {
      const live = await resolveMessage({
        messageId: m.messageId,
        headerMessageId: m.headerMessageId,
        folderId: m.targetFolderId,
        accountId: m.accountId,
      });
      if (!live) {
        report.skipped.push({ ...m, error: "message_not_found" });
        continue;
      }
      const stillThere = m.targetFolderId
        ? live.folder?.id === m.targetFolderId
        : (live.folder?.path || "").replace(/^\/+/, "").toLowerCase() === (m.targetFolder || "").toLowerCase();
      if (!stillThere) {
        report.skipped.push({ ...m, error: "message_moved_meanwhile" });
        continue;
      }
      const accountId = live.folder?.accountId;
      const dest =
        findFolderInAccount(folders, accountId, m.originalFolderId, m.originalFolder) ||
        findAllInboxes(folders).find((f) => f.accountId === accountId);
      if (!dest) throw new PolicyError("target_folder_missing");
      await mailbox.move(cfg, live, dest);

      let newId = String(live.id);
      if (m.headerMessageId) {
        const post = await api.messages.query({ headerMessageId: m.headerMessageId, folderId: dest.id }).catch(() => null);
        if (post?.messages?.[0]?.id) newId = String(post.messages[0].id);
      }
      report.revertedMoves.push({
        messageId: newId,
        oldMessageId: String(live.id),
        headerMessageId: m.headerMessageId || "",
        originalFolder: dest.path,
        originalFolderId: dest.id,
      });
      report.reverted++;
    } catch (err) {
      report.failed.push({ ...m, error: errorCode(err) });
    }
  }
  return report;
}

async function syncRevertReport(report) {
  if (report.revertedMoves?.length > 0) {
    await callNative("sync_reverts", { moves: report.revertedMoves }).catch(() => {});
  }
  const retry = (report.failed || []).map(({ error: _e, ...m }) => m);
  if (retry.length > 0) {
    await callNative("record_moves", { moves: retry }).catch(() => {});
  }
}

async function composeContext(tabId) {
  const details = await api.compose.getComposeDetails(tabId);
  const attachments = (await api.compose.listAttachments?.(tabId).catch(() => [])) || [];
  const body = details.isPlainText ? details.plainTextBody || "" : details.body || "";
  return {
    details,
    ownText: draftOwnText(body, Boolean(details.isPlainText)),
    subject: details.subject || "",
    relatedMessageId: details.relatedMessageId || null,
    attachmentNames: attachments.map((a) => a.name).filter(Boolean),
  };
}

// Runs the assistant for the draft in a compose window. The result is returned to the popup and
// only written into the draft when the user clicks "insert".
async function composeAssist(msg) {
  const cfg = await loadConfig();
  requireFeature(cfg, "assistant");
  const ctx = await composeContext(msg.tabId);
  let primary = null;
  let threadMessages = [];
  if (ctx.relatedMessageId) {
    const header = await api.messages.get(ctx.relatedMessageId).catch(() => null);
    if (header) {
      primary = await buildMessageInput(header, { includeAttachmentText: true });
      threadMessages = await collectThreadMessages(primary);
    }
  }
  if ((msg.mode === "review_draft" || msg.mode === "improve_draft" || msg.mode === "translate_draft") && !ctx.ownText) {
    throw new PolicyError("draft_empty");
  }
  if (msg.mode === "draft_reply" && !primary) throw new PolicyError("no_related_message");
  const res = await runAnalyzeThread(cfg, {
    mode: msg.mode,
    ephemeral: true,
    language: uiLanguage(cfg, msg.language),
    targetLanguage: msg.targetLanguage || "",
    tone: msg.tone || "freundlich",
    question: msg.question || "",
    userDraft: msg.mode === "draft_reply" ? "" : ctx.ownText,
    draftAttachments: ctx.attachmentNames,
    primaryMessage: primary || { id: "", subject: ctx.subject, from: "", bodyText: "" },
    threadMessages,
  });
  const draft = (res.proposals || []).find((p) => p.kind === "draft_reply");
  // Some backends answer with the mail text instead of a proposal: then that text is the draft.
  const writing = msg.mode !== "review_draft";
  const text = draft?.body || (writing ? res.answer || "" : "");
  return {
    answer: draft || !writing ? res.answer || "" : "",
    reviewIssues: res.reviewIssues || [],
    text,
    subject: draft?.subject || "",
    provider: res.provider,
  };
}

function broadcastToUI(message) {
  api.runtime.sendMessage(message).catch(() => {});
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

api.runtime.onMessage.addListener((msg) => {
  if (!msg || !msg.type) return false;
  switch (msg.type) {
    case "omathunder:getSnapshot":
      return (async () => {
        const secrets = await loadStandaloneSecrets();
        const secretsMeta = {
          hasGeminiKey: Boolean(secrets.geminiApiKey),
          hasAnthropicKey: Boolean(secrets.anthropicApiKey),
        };
        try {
          const cfg = await callNative("get_config");
          hostConnected = true;
          rememberHostConfig(cfg);
          await syncFeatures(cfg, null, { standalone: false });
          const ping = await callNative("ping");
          const [themePayload, themes, stateData, doctor, folderData] = await Promise.all([
            callNative("get_theme"),
            callNative("list_themes"),
            callNative("get_state"),
            callNative("doctor"),
            listAccountFolders().catch(() => ({ folders: [], accounts: [] })),
          ]);
          return {
            connected: true,
            standalone: false,
            nativeFiltersSupported: Boolean(api.omaFilters?.listFilters),
            host: { version: ping?.version || "?", protocol: ping?.protocol ?? 0, expectedProtocol: PROTOCOL },
            protocolOk: ping?.protocol === PROTOCOL,
            extensionVersion: api.runtime.getManifest().version,
            theme: themePayload,
            themes,
            state: stateData.state,
            barSummary: stateData.barSummary,
            config: cfg,
            secretsMeta,
            doctor,
            folders: folderData.folders,
            accounts: (folderData.accounts || []).map((a) => ({ id: a.id, name: a.name, type: a.type })),
          };
        } catch (err) {
          hostConnected = false;
          const [cfg, folderData] = await Promise.all([
            loadStandaloneConfig(),
            listAccountFolders().catch(() => ({ folders: [], accounts: [] })),
          ]);
          await syncFeatures(cfg, null, { standalone: true });
          return {
            connected: false,
            standalone: true,
            nativeFiltersSupported: Boolean(api.omaFilters?.listFilters),
            extensionVersion: api.runtime.getManifest().version,
            error: String(err?.message || err),
            theme: currentThemePayload,
            themes: OmaStandalone.listPalettes(),
            config: cfg,
            secretsMeta,
            state: { classifications: {}, proposals: [] },
            barSummary: { actionCount: 0, pendingProposals: 0 },
            folders: folderData.folders,
            accounts: (folderData.accounts || []).map((a) => ({ id: a.id, name: a.name, type: a.type })),
          };
        }
      })();

    case "omathunder:setTheme":
      return (async () => {
        const cfg = await loadConfig();
        requireFeature(cfg, "theme");
        if (hostConnected) {
          try {
            const payload = await callNative("set_theme", { mode: msg.mode || "", preset: msg.preset || "" });
            await applyThemePayload(payload);
            return payload;
          } catch (err) {
            if (!OmaStandalone.isNativeHostUnavailableError(err)) throw err;
            hostConnected = false;
          }
        }
        const nextCfg = await saveStandaloneConfig({
          theme: { mode: msg.mode || "preset", preset: msg.preset || cfg?.theme?.preset || "tokyo-night" },
        });
        const payload = buildStandaloneTheme(nextCfg);
        await applyThemePayload(payload);
        broadcastToUI({ type: "omathunder:themeChanged", payload });
        return payload;
      })();

    case "omathunder:triageInbox":
      return runTriage(msg.language || "", msg.accountId || "");

    case "omathunder:undoScan":
      return (async () => {
        const cfg = await loadConfig();
        requireFeature(cfg, "triage");
        const res = await callNative("undo_scan_moves", { scanId: msg.scanId });
        const report = await revertRuleMoves(cfg, res?.revertMoves || []);
        await syncRevertReport(report);
        return { revertReport: report };
      })();

    case "omathunder:composeContext":
      return (async () => {
        if (!currentThemePayload) await loadConfig().catch(() => {});
        const ctx = await composeContext(msg.tabId);
        return {
          ownText: ctx.ownText,
          subject: ctx.subject,
          hasRelated: Boolean(ctx.relatedMessageId),
          theme: features.theme ? currentThemePayload : null,
        };
      })();

    case "omathunder:composeAssist":
      return composeAssist(msg);

    case "omathunder:composeApply":
      return (async () => {
        const cfg = await loadConfig();
        requireFeature(cfg, "assistant");
        await writeIntoCompose(msg.tabId, msg.text || "", msg.subject || "");
        return { ok: true };
      })();

    case "omathunder:saveRules":
      return (async () => {
        const cfg = await loadConfig();
        requireFeature(cfg, "triage");
        const nextRules = [];
        const folderNotes = [];
        for (const r of msg.rules || []) {
          const copy = { ...r };
          if (msg.autoCreateFolders && copy.folderPath && copy.accountId) {
            try {
              const folder = await ensureFolderPath(cfg, copy.accountId, copy.folderPath);
              if (folder) copy.folderId = folder.id;
              for (const colPath of Object.values(copy.columnFolders || {})) {
                if (colPath) await ensureFolderPath(cfg, copy.accountId, colPath);
              }
            } catch (err) {
              folderNotes.push(errorCode(err));
            }
          }
          nextRules.push(copy);
        }
        const saved = await callNative("save_rules", { rules: nextRules });
        return { config: saved, folderNotes };
      })();

    case "omathunder:saveFilterRule":
      return (async () => {
        const cfg = await loadConfig();
        const rule = msg.rule;
        if (!rule || !rule.folderPath) throw new Error("Rule or folderPath missing");
        const existing = Array.isArray(cfg?.rules) ? cfg.rules : [];
        const nextRule = {
          id: rule.id || `rule-${Date.now()}`,
          space: rule.space || rule.name || "Filter",
          accountId: rule.accountId || "",
          folderPath: String(rule.folderPath).replace(/^\/+|\/+$/g, ""),
          domains: Array.isArray(rule.domains) ? rule.domains : [],
          fromContains: Array.isArray(rule.fromContains) ? rule.fromContains : [],
          subjectContains: Array.isArray(rule.subjectContains) ? rule.subjectContains : [],
          nativeFilterNames: Array.isArray(rule.nativeFilterNames) ? rule.nativeFilterNames : [],
          autoMove: rule.autoMove !== false,
          includeInScan: Boolean(rule.includeInScan),
        };
        const saved = await callNative("save_rules", { rules: [...existing, nextRule] });
        return { config: saved, rule: nextRule };
      })();

    case "omathunder:deleteRule":
      return (async () => {
        const cfg = await loadConfig();
        requireFeature(cfg, "triage");
        const revert = Boolean(msg.revertToIncoming);
        const res = await callNative("delete_rule", { ruleId: msg.ruleId, revertToIncoming: revert });
        if (!revert || !Array.isArray(res?.revertMoves) || res.revertMoves.length === 0) return res;
        const report = await revertRuleMoves(cfg, res.revertMoves);
        await syncRevertReport(report);
        return { ...res, revertReport: report };
      })();

    case "omathunder:moveKanban":
      return (async () => {
        const cfg = await loadConfig();
        requireFeature(cfg, "triage");
        const res = await callNative("move_kanban", { messageId: msg.messageId, column: msg.column });
        const effects = await applyKanbanSideEffects(cfg, msg.messageId, msg.column, { state: res?.state }).catch((err) => ({ skipped: errorCode(err) }));
        return { ...res, effects };
      })();

    case "omathunder:moveKanbanBatch":
      return (async () => {
        const cfg = await loadConfig();
        requireFeature(cfg, "triage");
        const ids = Array.isArray(msg.messageIds) ? msg.messageIds.map(String).filter(Boolean) : [];
        const res = await callNative("move_kanban_batch", { messageIds: ids, column: msg.column });
        const effects = await applyKanbanSideEffectsBatch(cfg, res?.state, ids, msg.column).catch((err) => ({ skipped: errorCode(err) }));
        if (res?.state) {
          broadcastToUI({ type: "omathunder:stateChanged", state: res.state, barSummary: res.barSummary || null });
        }
        return { ...res, effects };
      })();

    case "omathunder:markCardsRead":
    case "omathunder:markColumnRead":
      return (async () => {
        const cfg = await loadConfig();
        requireFeature(cfg, "triage");
        if (cfg?.mailbox?.readOnly !== false) throw new PolicyError("read_only");
        const isColumnCall = msg.type === "omathunder:markColumnRead";
        const column = isColumnCall ? String(msg.column || "done") : String(msg.column || "");
        const snap = await callNative("get_state").catch(() => null);
        const clsMap = snap?.state?.classifications || {};
        let refs = Array.isArray(msg.items) ? msg.items : null;
        if (!refs) {
          refs = Object.values(clsMap)
            .filter((c) => !column || (c.kanbanColumn || "incoming") === column)
            .map((c) => ({
              messageId: c.messageId,
              headerMessageId: c.threadId,
              accountId: c.accountId,
              folderId: c.folderId,
              kanbanColumn: c.kanbanColumn || "incoming",
            }));
        }
        let marked = 0;
        let alreadyRead = 0;
        let skipped = 0;
        let failed = 0;
        const cleanedIds = [];
        for (const ref of refs) {
          try {
            const live = await resolveMessage(ref, { strict: true });
            if (!live) {
              skipped++;
              continue;
            }
            const refId = ref?.messageId ? String(ref.messageId) : "";
            const effectiveCol = ref?.kanbanColumn || clsMap[refId]?.kanbanColumn || column || "incoming";
            const shouldClean = effectiveCol === "done" || (isColumnCall && column === "done");
            if (live.read) {
              alreadyRead++;
              if (refId && shouldClean) cleanedIds.push(refId);
              continue;
            }
            await mailbox.markRead(cfg, live, true);
            marked++;
            if (refId && shouldClean) cleanedIds.push(refId);
          } catch (err) {
            if (err instanceof PolicyError) skipped++;
            else failed++;
          }
        }
        let nextState = null;
        let removed = 0;
        if (cleanedIds.length > 0) {
          nextState = await callNative("remove_classifications", { ids: cleanedIds }).catch(() => null);
          if (nextState?.state) {
            removed = cleanedIds.length;
            broadcastToUI({ type: "omathunder:stateChanged", state: nextState.state, barSummary: nextState.barSummary || null });
          }
        }
        return {
          marked,
          alreadyRead,
          skipped,
          failed,
          removed,
          state: nextState?.state || null,
          barSummary: nextState?.barSummary || null,
        };
      })();

    case "omathunder:getSelectionInfo":
      return (async () => {
        const usePending = Boolean(msg.usePendingSelection) && pendingSelectionContext && (Date.now() - pendingSelectionContext.createdAt < 60000);
        const headers = usePending ? pendingSelectionContext.headers : await getSelectedMessageHeaders();
        return {
          count: headers.length,
          subjects: headers.slice(0, 3).map((h) => h.subject || ""),
          froms: headers.slice(0, 3).map((h) => h.author || ""),
          accountId: headers[0]?.folder?.accountId || "",
        };
      })();

    case "omathunder:listNativeFilters":
      return (async () => {
        const data = await listNativeFiltersSafe(msg.accountId || "");
        let issues = [];
        let candidates = [];
        if (data.supported && Array.isArray(data.filters)) {
          const hostRes = await callNative("suggest_space_imports", {
            accountId: msg.accountId || "",
            filters: data.filters,
          }).catch(() => null);
          issues = hostRes?.issues || [];
          candidates = hostRes?.candidates || [];
        }
        return { ...data, issues, candidates };
      })();

    case "omathunder:diagnoseFilters":
      return (async () => {
        const usePending = Boolean(msg.usePendingSelection) && pendingSelectionContext && (Date.now() - pendingSelectionContext.createdAt < 60000);
        const headers = usePending ? pendingSelectionContext.headers : await getSelectedMessageHeaders();
        if (usePending) pendingSelectionContext = null;
        if (!headers || headers.length === 0) throw new PolicyError("no_message_displayed");
        const input = await buildMessageInput(headers[0], { includeAttachmentText: false });
        const nativeData = await listNativeFiltersSafe(input.accountId || "");
        const diagnostic = await callNative("diagnose_filters", {
          message: input,
          filters: nativeData.filters || [],
        });
        return {
          supported: nativeData.supported,
          message: { id: input.id, accountId: input.accountId, subject: input.subject, from: input.from, folder: input.folder },
          diagnostic,
          filters: nativeData.filters || [],
        };
      })();

    case "omathunder:applyNativeFilter":
      return (async () => {
        if (!api.omaFilters?.upsertFilter) throw new Error("Native Filter API not available");
        const spec = msg.spec;
        if (!spec || !spec.accountId) throw new Error("Account ID missing for native filter");
        let res;
        try {
          res = await api.omaFilters.upsertFilter(spec);
        } catch (err) {
          // A rule linked to a native filter the user has deleted meanwhile: create it again.
          if (spec.mode !== "replace" || !/^not_found\b/.test(String(err?.message || ""))) throw err;
          res = await api.omaFilters.upsertFilter({ ...spec, mode: "create", name: spec.name || spec.existingName });
        }
        let runResult = null;
        if (msg.runNow && res?.filter?.name) {
          runResult = await api.omaFilters.runFilters(spec.accountId, [res.filter.name]).catch((err) => ({
            error: String(err?.message || err),
          }));
        }
        if (msg.linkRuleId && res?.filter?.name) {
          const cfg = await loadConfig().catch(() => null);
          if (cfg && Array.isArray(cfg.rules)) {
            const nextRules = cfg.rules.map((r) => {
              if (r.id !== msg.linkRuleId) return r;
              const names = Array.isArray(r.nativeFilterNames) ? [...r.nativeFilterNames] : [];
              if (!names.includes(res.filter.name)) names.push(res.filter.name);
              return { ...r, nativeFilterNames: names };
            });
            await callNative("save_rules", { rules: nextRules }).catch(() => {});
          }
        }
        return { ...res, runResult };
      })();

    case "omathunder:runNativeFilters":
      return (async () => {
        if (!api.omaFilters?.runFilters) throw new Error("Native Filter API not available");
        return api.omaFilters.runFilters(msg.accountId, Array.isArray(msg.filterNames) ? msg.filterNames : undefined);
      })();

    case "omathunder:openNativeFilters":
      return (async () => {
        if (!api.omaFilters?.openFilterDialog) return { opened: false };
        return api.omaFilters.openFilterDialog(msg.accountId || undefined);
      })();

    case "omathunder:importSpaceRules":
      return (async () => {
        const cfg = await loadConfig();
        const { folders } = await listAccountFolders().catch(() => ({ folders: [] }));
        const existing = Array.isArray(cfg?.rules) ? [...cfg.rules] : [];
        let importedCount = 0;
        for (const cand of msg.candidates || []) {
          if (!cand || !cand.folderPath || !cand.accountId) continue;
          const cleanFolder = String(cand.folderPath).replace(/^\/+|\/+$/g, "");
          const folderObj = findFolderInAccount(folders, cand.accountId, "", cleanFolder);
          const idx = existing.findIndex(
            (r) =>
              r.accountId === cand.accountId &&
              String(r.folderPath || "").replace(/^\/+|\/+$/g, "").toLowerCase() === cleanFolder.toLowerCase(),
          );
          if (idx >= 0) {
            const prev = existing[idx];
            const names = [...new Set([...(prev.nativeFilterNames || []), ...(cand.nativeFilterNames || [])])];
            existing[idx] = { ...prev, nativeFilterNames: names, folderId: prev.folderId || folderObj?.id || "" };
          } else {
            existing.push({
              id: `rule-${Date.now()}-${importedCount}`,
              space: cand.space || cleanFolder,
              accountId: cand.accountId,
              folderPath: cleanFolder,
              folderId: folderObj?.id || "",
              domains: Array.isArray(cand.domains) ? cand.domains : [],
              fromContains: Array.isArray(cand.fromContains) ? cand.fromContains : [],
              subjectContains: Array.isArray(cand.subjectContains) ? cand.subjectContains : [],
              nativeFilterNames: Array.isArray(cand.nativeFilterNames) ? cand.nativeFilterNames : [],
              autoMove: false, // Thunderbird's native filter already moves incoming mail
              includeInScan: true,
            });
            importedCount++;
          }
        }
        const saved = await callNative("save_rules", { rules: existing });
        return { config: saved, importedCount };
      })();

    case "omathunder:analyzeMessage":
      return (async () => {
        const cfg = await loadConfig();
        requireFeature(cfg, "assistant");
        const selectionModes = new Set(["summarize", "summarize_selection", "extract_events", "suggest_filter"]);
        if (!msg.messageId && selectionModes.has(msg.mode)) {
          const usePending = Boolean(msg.usePendingSelection) && pendingSelectionContext && (Date.now() - pendingSelectionContext.createdAt < 60000);
          const headers = usePending ? pendingSelectionContext.headers : await getSelectedMessageHeaders();
          if (usePending) pendingSelectionContext = null;
          if (!headers || headers.length === 0) throw new PolicyError("no_message_displayed");
          const includeAttachmentText = msg.mode !== "suggest_filter";

          if (headers.length > 1 || msg.mode === "suggest_filter") {
            const inputs = [];
            for (const h of headers.slice(0, MAX_SELECTED_MESSAGES)) {
              inputs.push(await buildMessageInput(h, { includeAttachmentText }));
            }
            inputs.sort((a, b) => new Date(a.date || 0) - new Date(b.date || 0));
            const backendMode = msg.mode === "summarize" ? "summarize_selection" : msg.mode;
            const accountId = inputs[0]?.accountId || "";
            let nativeFilters = [];
            let filterDiagnostic = null;
            if (msg.mode === "suggest_filter") {
              const nativeData = await listNativeFiltersSafe(accountId);
              if (nativeData.supported && Array.isArray(nativeData.filters) && nativeData.filters.length > 0) {
                nativeFilters = nativeData.filters;
                filterDiagnostic = await callNative("diagnose_filters", {
                  message: inputs[0],
                  filters: nativeFilters,
                }).catch(() => null);
              }
            }
            const res = await runAnalyzeThread(cfg, {
              mode: backendMode,
              ephemeral: true,
              language: uiLanguage(cfg, msg.language),
              targetLanguage: msg.targetLanguage || "",
              tone: msg.tone || "freundlich",
              question: msg.question || "",
              primaryMessage: inputs[0],
              threadMessages: inputs,
              nativeFilters,
              filterDiagnostic,
            });
            return { ...res, selectionCount: inputs.length, accountId, nativeFiltersSupported: Boolean(api.omaFilters?.upsertFilter), filterDiagnostic };
          }
          const primary = await buildMessageInput(headers[0], { includeAttachmentText });
          const threadMessages = await collectThreadMessages(primary);
          const res = await runAnalyzeThread(cfg, {
            mode: msg.mode,
            ephemeral: Boolean(msg.ephemeral) || msg.mode === "extract_events",
            language: uiLanguage(cfg, msg.language),
            targetLanguage: msg.targetLanguage || "",
            tone: msg.tone || "freundlich",
            question: msg.question || "",
            primaryMessage: primary,
            threadMessages,
          });
          return { ...res, selectionCount: 1, accountId: primary.accountId || "" };
        }

        let primary = null;
        if (msg.messageId) {
          // A board card was selected explicitly: analyze exactly that mail or fail.
          const snap = await callNative("get_state").catch(() => null);
          const cls = snap?.state?.classifications?.[String(msg.messageId)];
          const header = cls
            ? await resolveMessage(
                { messageId: msg.messageId, headerMessageId: cls.threadId, accountId: cls.accountId, folderId: cls.folderId },
                { strict: false },
              )
            : null;
          if (!header) throw new PolicyError("message_not_found");
          primary = await buildMessageInput(header, { includeAttachmentText: true });
        } else {
          primary = await getActiveDisplayedMessage();
        }
        if (!primary) throw new PolicyError("no_message_displayed");
        const threadMessages = await collectThreadMessages(primary);
        return runAnalyzeThread(cfg, {
          mode: msg.mode || "draft_reply",
          ephemeral: Boolean(msg.ephemeral),
          language: uiLanguage(cfg, msg.language),
          targetLanguage: msg.targetLanguage || "",
          tone: msg.tone || "freundlich",
          question: msg.question || "",
          userDraft: msg.userDraft || "",
          primaryMessage: primary,
          threadMessages,
        });
      })();

    case "omathunder:getDisplayedMessage":
      return (async () => {
        const header = await findDisplayedHeader();
        if (!header) return null;
        return {
          subject: header.subject || "",
          from: header.author || "",
          folder: (header.folder?.path || "").replace(/^\/+/, ""),
        };
      })();

    case "omathunder:getActiveCompose":
      return getActiveComposeDraft();

    case "omathunder:applyProposal":
      return applyProposalInThunderbird(msg.proposal, { replyAll: Boolean(msg.replyAll) });

    case "omathunder:dismissProposal":
      return callNative("update_proposal", { id: msg.id, status: "dismissed" });

    case "omathunder:dismissProposals":
      return callNative("dismiss_proposals", { ids: Array.isArray(msg.ids) ? msg.ids : [] });

    case "omathunder:saveConfig":
      return (async () => {
        if (msg.secrets) {
          await saveStandaloneSecrets(msg.secrets);
        }
        if (hostConnected) {
          try {
            const res = await callNative("save_config", msg.config || {});
            rememberHostConfig(res?.config);
            await syncFeatures(res?.config, res?.theme, { standalone: false });
            return res;
          } catch (err) {
            if (!OmaStandalone.isNativeHostUnavailableError(err)) throw err;
            hostConnected = false;
          }
        }
        const nextCfg = await saveStandaloneConfig(msg.config || {});
        await syncFeatures(nextCfg, null, { standalone: true });
        broadcastToUI({ type: "omathunder:themeChanged", payload: currentThemePayload });
        return { config: nextCfg, theme: currentThemePayload, standalone: true };
      })();

    case "omathunder:openMessage":
      return (async () => {
        const snap = await callNative("get_state").catch(() => null);
        const cls = snap?.state?.classifications?.[String(msg.messageId)];
        const live = cls
          ? await resolveMessage(
              { messageId: msg.messageId, headerMessageId: cls.threadId, accountId: cls.accountId, folderId: cls.folderId },
              { strict: false },
            )
          : null;
        if (!live) throw new PolicyError("message_not_found");
        await api.messageDisplay.open({ messageId: live.id });
        return { ok: true };
      })();

    case "omathunder:openSpace":
      return (async () => {
        const url = api.runtime.getURL("ui/space.html");
        await api.tabs.create({ url });
        return { ok: true };
      })();

    case "omathunder:archiveStats":
      return callNative("archive_stats", { archiveDir: msg.archiveDir || "" });

    case "omathunder:previewCleanup":
      return previewCleanup(msg);

    case "omathunder:runCleanup":
      return runCleanup(msg);
  }
  return false;
});

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
  requireFeature(cfg, "triage");
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

async function previewCleanup(params = {}) {
  const cfg = await loadConfig();
  const data = await collectCleanupCandidates(cfg, params);
  const localAcc = findLocalAccount(data.folderState.accounts);
  const totalBytes = data.candidates.reduce((sum, c) => sum + (c.size || 0), 0);
  return {
    candidates: data.candidates,
    totalCount: data.candidates.length,
    totalMatched: data.totalMatched,
    totalBytes,
    totalBytesAll: data.totalBytesAll,
    truncated: data.truncated,
    scannedFolders: data.scannedFolders,
    hasLocalAccount: Boolean(localAcc),
    localAccountName: localAcc?.name || "",
    readOnly: cfg.mailbox?.readOnly !== false,
    archiveDir: params.archiveDir || cfg.mailbox?.archiveDir || "",
  };
}

async function runCleanup(params = {}) {
  const mode = params.mode || "simulate"; // "simulate" | "export_only" | "export_and_local" | "export_and_delete"
  const cfg = await loadConfig();
  const data = await collectCleanupCandidates(cfg, params);
  const localAcc = findLocalAccount(data.folderState.accounts);

  if ((mode === "export_and_local" || mode === "export_and_delete") && cfg.mailbox?.readOnly !== false) {
    throw new PolicyError("read_only");
  }
  if (mode === "export_and_local" && !localAcc) {
    throw new PolicyError("local_folder_required");
  }

  const archiveDir = String(params.archiveDir || cfg.mailbox?.archiveDir || "").trim();
  const report = {
    mode,
    dryRun: mode === "simulate",
    scannedFolders: data.scannedFolders,
    totalCandidates: data.candidates.length,
    exported: 0,
    skippedDuplicates: 0,
    moved: 0,
    deleted: 0,
    bytesExported: 0,
    bytesEstimated: data.candidates.reduce((s, c) => s + (c.size || 0), 0),
    failed: [],
    items: [],
  };

  if (mode === "simulate") {
    for (const c of data.candidates) {
      try {
        const exp = await callNative("archive_export", {
          archiveDir,
          accountId: c.accountId,
          accountName: c.accountName,
          folderPath: c.folderPath,
          date: c.date,
          subject: c.subject,
          messageId: c.messageId,
          headerMessageId: c.headerMessageId,
          dryRun: true,
        });
        if (exp?.skippedDuplicate) report.skippedDuplicates++;
        else report.exported++;
        if (report.items.length < 100) {
          report.items.push({
            subject: c.subject,
            from: c.from,
            date: c.date,
            size: c.size,
            targetPath: exp?.targetPath || c.archiveRelPath,
            skippedDuplicate: Boolean(exp?.skippedDuplicate),
          });
        }
      } catch (err) {
        report.failed.push({ subject: c.subject, error: errorCode(err) });
      }
    }
    return report;
  }

  for (const c of data.candidates) {
    try {
      const live = await resolveMessage({
        messageId: c.messageId,
        headerMessageId: c.headerMessageId,
        accountId: c.accountId,
        folderId: c.folderId,
      }, { strict: true });
      if (!live) throw new PolicyError("message_not_found");
      if (live.folder?.id !== c.folderId) throw new PolicyError("message_moved_meanwhile");

      const { base64, byteLength } = await readRawEmlBase64(live.id);
      const exp = await callNative("archive_export", {
        archiveDir,
        accountId: c.accountId,
        accountName: c.accountName,
        folderPath: c.folderPath,
        date: c.date,
        subject: c.subject,
        messageId: String(live.id),
        headerMessageId: c.headerMessageId,
        rawEmlBase64: base64,
        dryRun: false,
      }, 120000);

      if (!exp?.verified) throw new Error("archive_verification_failed");
      if (exp.skippedDuplicate) report.skippedDuplicates++;
      else report.exported++;
      report.bytesExported += Number(exp.bytesWritten || byteLength || 0);

      if (mode === "export_and_local" || mode === "export_and_delete") {
        // Re-check live policy before any mailbox mutation.
        const liveCfg = await loadConfig();
        if (mode === "export_and_local") {
          const parts = ["OmaThunder-Archiv", ...buildArchiveRelativeParts(c.accountName, c.folderPath, c.date)];
          const targetLocal = await ensureLocalArchiveFolder(liveCfg, localAcc.id, parts, data.folderState);
          requireLocalArchiveMove(liveCfg, live, targetLocal, data.folderState.accounts);
          await api.messages.move([live.id], targetLocal.id);
          report.moved++;
        } else if (mode === "export_and_delete") {
          requireArchiveDelete(liveCfg, live);
          await api.messages.delete([live.id], Boolean(params.skipTrash));
          report.deleted++;
        }
      }

      if (report.items.length < 100) {
        report.items.push({
          subject: c.subject,
          from: c.from,
          date: c.date,
          size: c.size || byteLength,
          targetPath: exp.targetPath,
          skippedDuplicate: Boolean(exp.skippedDuplicate),
        });
      }
    } catch (err) {
      report.failed.push({ subject: c.subject, from: c.from, error: errorCode(err) });
    }
  }
  return report;
}

let pendingSelectionContext = null;

async function registerContextMenus() {
  if (!api.menus?.create) return;
  try {
    await api.menus.removeAll?.();
    api.menus.create({
      id: "oma-summarize-de",
      title: "OmaThunder: Auswahl zusammenfassen (DE)",
      contexts: ["message_list"],
    });
    api.menus.create({
      id: "oma-summarize-en",
      title: "OmaThunder: Summarize selection (EN)",
      contexts: ["message_list"],
    });
    api.menus.create({
      id: "oma-suggest-filter",
      title: "OmaThunder: Filterregel vorschlagen",
      contexts: ["message_list"],
    });
    api.menus.create({
      id: "oma-diagnose-filter",
      title: "OmaThunder: Filter-Diagnose (Warum?)",
      contexts: ["message_list"],
    });
  } catch (err) {
    console.warn("OmaThunder context menu setup note:", err);
  }
}

api.menus?.onClicked?.addListener(async (info, tab) => {
  const actions = {
    "oma-summarize-de": { auto: "summarize", target: "de" },
    "oma-summarize-en": { auto: "summarize", target: "en" },
    "oma-suggest-filter": { auto: "suggest_filter", target: "" },
    "oma-diagnose-filter": { auto: "diagnose_filter", target: "" },
  };
  const act = actions[info.menuItemId];
  if (!act) return;
  let headers = [];
  if (info.selectedMessages) {
    headers = await collectMessageList(info.selectedMessages, MAX_SELECTED_MESSAGES);
  }
  if (headers.length === 0) {
    headers = await getSelectedMessageHeaders(tab?.id || null);
  }
  pendingSelectionContext = { headers, lang: act.target, createdAt: Date.now() };
  const qs = new URLSearchParams({ auto: act.auto, pending: "1" });
  if (act.target) qs.set("target", act.target);
  const url = api.runtime.getURL(`ui/popup.html?${qs.toString()}`);
  if (api.windows?.create) {
    await api.windows.create({ url, type: "popup", width: 440, height: 620 }).catch(async () => {
      await api.tabs.create({ url });
    });
  } else {
    await api.tabs.create({ url });
  }
});

// Live Kanban sync: automatically scan newly arrived mail (when scanOnNewMail is active) and prune
// Done-read or deleted/trashed messages so board counters stay aligned with Thunderbird.
let newMailTimer = null;
let pendingNewMailAccounts = new Set();
let pendingNewMailAll = false;

api.messages?.onNewMailReceived?.addListener((folder, messages) => {
  if (bridgeEventsWanted()) emitMailNew(folder, messages).catch(() => {});
  if (folder?.accountId) pendingNewMailAccounts.add(folder.accountId);
  else pendingNewMailAll = true;

  clearTimeout(newMailTimer);
  newMailTimer = setTimeout(async () => {
    const scanAll = pendingNewMailAll;
    const accIds = [...pendingNewMailAccounts];
    pendingNewMailAccounts.clear();
    pendingNewMailAll = false;
    try {
      const cfg = await loadConfig();
      if (!hostConnected || !cfg.features?.triage || !cfg.mailbox?.scanOnNewMail) return;
      const enabled = kanbanAccountIds(cfg);
      if (enabled.size === 0) return;
      if (scanAll || accIds.length === 0) {
        const res = await runTriage();
        broadcastToUI({ type: "omathunder:scanFinished", report: res.moveReport });
      } else {
        for (const accId of accIds) {
          if (!enabled.has(accId)) continue;
          const res = await runTriage("", accId);
          broadcastToUI({ type: "omathunder:scanFinished", report: res.moveReport, accountId: accId });
        }
      }
    } catch (err) {
      console.info("OmaThunder auto scan skipped:", errorCode(err));
    }
  }, 3500);
});

let boardSyncTimer = null;
const pendingDoneReadRefs = [];
const pendingDeleteRefs = [];

function scheduleBoardSync() {
  clearTimeout(boardSyncTimer);
  boardSyncTimer = setTimeout(async () => {
    const doneReads = pendingDoneReadRefs.splice(0, pendingDoneReadRefs.length);
    const deletes = pendingDeleteRefs.splice(0, pendingDeleteRefs.length);
    if (doneReads.length === 0 && deletes.length === 0) return;
    try {
      const cfg = await loadConfig();
      if (!hostConnected || !cfg.features?.triage) return;
      const snap = await callNative("get_state").catch(() => null);
      const entries = Object.entries(snap?.state?.classifications || {});
      if (entries.length === 0) return;

      const doneReadIds = new Set(doneReads.map((r) => r.id).filter(Boolean));
      const doneReadThreads = new Set(doneReads.map((r) => r.headerMessageId).filter(Boolean));
      const deleteIds = new Set(deletes.map((r) => r.id).filter(Boolean));
      const deleteThreads = new Set(deletes.map((r) => r.headerMessageId).filter(Boolean));

      const toRemove = [];
      for (const [key, cls] of entries) {
        const deleted = deleteIds.has(key) || (cls.threadId && deleteThreads.has(cls.threadId));
        const readDone = (cls.kanbanColumn || "incoming") === "done"
          && (doneReadIds.has(key) || (cls.threadId && doneReadThreads.has(cls.threadId)));
        if (deleted || readDone) toRemove.push(key);
      }
      if (toRemove.length === 0) return;
      const res = await callNative("remove_classifications", { ids: toRemove }).catch(() => null);
      if (res?.state) {
        broadcastToUI({ type: "omathunder:stateChanged", state: res.state, barSummary: res.barSummary || null });
      }
    } catch (err) {
      console.info("OmaThunder live board sync note:", errorCode(err));
    }
  }, 300);
}

// Upper bound of mails read from one onDeleted/onMoved list (more is reported as truncated).
const MAX_EVENT_LIST = 2000;

api.messages?.onUpdated?.addListener((message, changedProperties) => {
  if (message) {
    // Bulk changes (e.g. mark 5000 mails read) arrive one by one: batch them into a few mail.updated events.
    queueBridgeEvent((q) => q.addUpdated(message.id, message.folder?.accountId));
  }
  if (!features.triage || !hostConnected) return;
  if (changedProperties?.read === true && message) {
    pendingDoneReadRefs.push({
      id: message.id != null ? String(message.id) : "",
      headerMessageId: message.headerMessageId || "",
    });
    scheduleBoardSync();
  }
});

api.messages?.onDeleted?.addListener(async (messages) => {
  const wantBridge = bridgeEventsWanted();
  const wantBoard = features.triage && hostConnected;
  if (!wantBridge && !wantBoard) return;
  const list = await collectMessageList(messages, MAX_EVENT_LIST + 1).catch(() => []);
  if (list.length === 0) return;
  if (wantBridge) {
    queueBridgeEvent((q) => {
      for (const m of list.slice(0, MAX_EVENT_LIST)) q.addDeleted(m.id, m.folder?.accountId);
      if (list.length > MAX_EVENT_LIST) q.markTruncated("deleted");
    });
  }
  if (!wantBoard) return;
  for (const m of list) {
    pendingDeleteRefs.push({
      id: m.id != null ? String(m.id) : "",
      headerMessageId: m.headerMessageId || "",
    });
  }
  scheduleBoardSync();
});

api.messages?.onMoved?.addListener(async (originalMessages, movedMessages) => {
  const wantBridge = bridgeEventsWanted();
  const wantBoard = features.triage && hostConnected;
  if (!wantBridge && !wantBoard) return;
  const [originals, moved] = await Promise.all([
    collectMessageList(originalMessages, MAX_EVENT_LIST + 1).catch(() => []),
    collectMessageList(movedMessages, MAX_EVENT_LIST + 1).catch(() => []),
  ]);
  if (wantBridge && moved.length > 0) {
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
  }
  if (!wantBoard) return;
  const toTrash = moved.some((m) => m?.folder?.type === "trash" || m?.folder?.type === "junk");
  if (!toTrash) return;
  for (const m of originals) {
    if (m?.folder?.type === "trash" || m?.folder?.type === "junk") continue;
    pendingDeleteRefs.push({
      id: m.id != null ? String(m.id) : "",
      headerMessageId: m.headerMessageId || "",
    });
  }
  if (pendingDeleteRefs.length > 0) scheduleBoardSync();
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

async function handleThunderbirdRPC(port, rpcId, method, params) {
  let reply;
  try {
    const result = await dispatchThunderbirdRPC(String(method || ""), params && typeof params === "object" ? params : {});
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
      const full = await api.messages.getFull(header.id).catch(() => null);
      const attachments = await api.messages.listAttachments(header.id).catch(() => []);
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
        bodyText: extractPlainText(full),
        attachments: (attachments || []).map((a) => ({
          name: a.name || "",
          contentType: a.contentType || "",
          size: Number(a.size || 0),
          partName: a.partName || "",
        })),
      };
      if (format === "html" || format === "both") out.bodyHtml = findPartText(full, "text/html");
      return out;
    }

    case "messages.thread": {
      const header = await resolveBridgeMessage(cfg, params);
      const limit = Math.max(1, Math.min(Number(params.limit) || 20, 50));
      const primary = await buildMessageInput(header, { includeAttachmentText: true });
      const msgs = await collectThreadMessages(primary, limit);
      return { messages: filterBridgeReadable(cfg, msgs).map(bridgeMessageInput) };
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

  if (isSend && !api.compose?.sendMessage) throw new PolicyError("feature_disabled:compose", "compose.sendMessage unavailable");
  const tab = replyHeader
    ? await api.compose.beginReply(replyHeader.id, replyType, details)
    : await api.compose.beginNew(details);
  const base = { ok: true, identityId: used.identityId, accountId: used.accountId };

  if (isSend) {
    const sent = await api.compose.sendMessage(tab.id, { mode: "sendNow" });
    return { ...base, sent };
  }
  if (openWindow) return { ...base, tabId: tab.id, windowOpened: true };
  let saved = null;
  if (api.compose?.saveMessage) {
    saved = await api.compose.saveMessage(tab.id, { mode: "draft" });
    await api.tabs?.remove?.(tab.id).catch(() => {});
  }
  return { ...base, tabId: tab.id, saved };
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

async function bootstrap() {
  await ensureSpacesTab();
  await registerContextMenus();
  try {
    // Opens the native port at startup (not only on the first UI action), so the host and its local
    // bridge run as soon as Thunderbird does. Lost connections are retried (scheduleNativeReconnect);
    // a host that is not installed falls back to standalone mode without retry loops.
    const cfg = await loadConfig();
    if (!cfg?.features?.theme) await removeTheme();
  } catch (err) {
    console.info("OmaThunder startup config note:", err?.message || err);
  }
}

api.runtime.onStartup?.addListener(() => {
  ensureSpacesTab();
});

bootstrap();
