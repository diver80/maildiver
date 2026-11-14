/* global browser, messenger, OmaCore */
const api = typeof messenger !== "undefined" ? messenger : browser;
// Pure decision logic (unit-tested in tests/core.test.js); this file only talks to Thunderbird.
const {
  PolicyError, isInboxFolder, findFolderInAccount, requireFeature, triageAccountIds, requireWritable,
  requireMovable, acceptDirectHit, pickMessageCandidate, selectScanFolders, newestSince, mergeTags,
  normalizeSubjectForThread, parseMessageIds, selectThread, draftOwnText, replaceDraftOwn, htmlToText,
} = OmaCore;
// Must match ProtocolVersion in internal/native/handler.go.
const PROTOCOL = 3;
const HOST_NAME = "dev.omathunder.host";

let nativePort = null;
let reqCounter = 0;
const pendingRequests = new Map();

let currentThemePayload = null;
let bodyScriptHandle = null;
let composeScriptHandle = null;
// Last known feature flags; refreshed from the host config. Theme stays off until the host confirms it.
let features = { theme: false, assistant: false, triage: false };
// Whether the theme also recolors message bodies and the compose editor (theme.styleBody).
let styleBody = false;

async function connectNative() {
  if (nativePort) return nativePort;
  const port = api.runtime.connectNative(HOST_NAME);
  nativePort = port;

  port.onMessage.addListener((msg) => {
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
    nativePort = null;
    for (const [, pending] of pendingRequests) {
      clearTimeout(pending.timer);
      pending.reject(new Error(errMsg));
    }
    pendingRequests.clear();
    broadcastToUI({ type: "omathunder:hostStatus", connected: false, error: errMsg });
  });

  return port;
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

// Applies the feature flags of a host config: switches the theme on/off and caches the flags.
async function syncFeatures(cfg, themePayload = null) {
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
  const payload = themePayload || (!themeWasOn || styleBodyChanged ? await callNative("get_theme").catch(() => null) : null);
  if (payload) await applyThemePayload(payload);
}

// ---------------------------------------------------------------------------
// Safety layer: every change to mails, folders or tags goes through these helpers.
// ---------------------------------------------------------------------------

async function loadConfig() {
  const cfg = await callNative("get_config");
  await syncFeatures(cfg);
  return cfg;
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

// Returns the newest `limit` mails of a folder received within the last `days` days.
async function listRecentMessages(folder, limit, days) {
  const since = new Date(Date.now() - days * 86400000);
  let page = await api.messages.query({ folderId: folder.id, fromDate: since }).catch(() => null);
  if (!page) page = await api.messages.list(folder.id).catch(() => null);
  const all = [];
  let pages = 0;
  while (page) {
    all.push(...(page.messages || []));
    if (!page.id || ++pages >= 20) break;
    page = await api.messages.continueList(page.id).catch(() => null);
  }
  return newestSince(all, since, limit);
}

// Scans only accounts enabled for triage: their INBOX plus rule folders (with subfolders) marked for the board.
async function collectScanMessages(cfg) {
  const limit = cfg?.mailbox?.scanLimit || 20;
  const days = cfg?.mailbox?.scanDays || 30;
  const folders = await listAllFolders();
  const scanFolders = selectScanFolders(cfg, folders);
  const messages = [];
  for (const f of scanFolders) {
    for (const header of await listRecentMessages(f, limit, days)) {
      messages.push(await buildMessageInput(header));
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

async function runTriage(requestedLanguage = "") {
  const cfg = await loadConfig();
  requireFeature(cfg, "triage");
  if (triageAccountIds(cfg).size === 0) throw new PolicyError("no_triage_accounts");

  const { messages, folders, scannedFolders } = await collectScanMessages(cfg);
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
async function collectThreadMessages(primary) {
  if (!primary || !api.messages?.query) return primary ? [primary] : [];
  const headers = new Map();
  const add = (list) => {
    for (const h of list || []) {
      const key = h.headerMessageId || "";
      if (key && key !== primary.threadId && !headers.has(key)) headers.set(key, h);
    }
  };

  // Earlier mails named in the primary mail's own headers.
  const ancestors = parseMessageIds(primary.headers?.references, primary.headers?.["in-reply-to"]).slice(-8);
  for (const id of ancestors) {
    const res = await api.messages.query({ headerMessageId: id }).catch(() => null);
    add((res?.messages || []).slice(0, 1));
  }
  // Later replies cannot be looked up by header, so search by subject and verify the link below.
  const baseSubj = normalizeSubjectForThread(primary.subject);
  if (baseSubj) {
    const res = await api.messages.query({ accountId: primary.accountId || undefined, subject: baseSubj }).catch(() => null);
    add((res?.messages || []).filter((h) => normalizeSubjectForThread(h.subject).toLowerCase() === baseSubj.toLowerCase()).slice(0, 16));
  }

  const candidates = [];
  for (const h of [...headers.values()].slice(0, 24)) candidates.push(await buildMessageInput(h));
  return selectThread(primary, candidates, 8);
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
async function applyKanbanSideEffects(cfg, messageId, column) {
  if (cfg?.mailbox?.readOnly !== false) return { skipped: "read_only" };
  const snap = await callNative("get_state").catch(() => null);
  const cls = snap?.state?.classifications?.[String(messageId)];
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
    const { folders } = await listAccountFolders();
    const target = findFolderInAccount(folders, msg.folder?.accountId, "", mappedFolder);
    if (!target) return { applied: true, skipped: "target_folder_missing" };
    if (target.id !== msg.folder?.id) await mailbox.move(cfg, msg, target);
  }
  return { applied: true };
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
  const res = await callNative("analyze_thread", {
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
  }, 120000);
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

api.runtime.onMessage.addListener((msg) => {
  if (!msg || !msg.type) return false;
  switch (msg.type) {
    case "omathunder:getSnapshot":
      return (async () => {
        try {
          const cfg = await loadConfig();
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
            host: { version: ping?.version || "?", protocol: ping?.protocol ?? 0, expectedProtocol: PROTOCOL },
            protocolOk: ping?.protocol === PROTOCOL,
            extensionVersion: api.runtime.getManifest().version,
            theme: themePayload,
            themes,
            state: stateData.state,
            barSummary: stateData.barSummary,
            config: cfg,
            doctor,
            folders: folderData.folders,
            accounts: (folderData.accounts || []).map((a) => ({ id: a.id, name: a.name, type: a.type })),
          };
        } catch (err) {
          const folderData = await listAccountFolders().catch(() => ({ folders: [], accounts: [] }));
          return {
            connected: false,
            extensionVersion: api.runtime.getManifest().version,
            error: String(err?.message || err),
            theme: currentThemePayload,
            folders: folderData.folders,
            accounts: (folderData.accounts || []).map((a) => ({ id: a.id, name: a.name, type: a.type })),
          };
        }
      })();

    case "omathunder:setTheme":
      return (async () => {
        const cfg = await loadConfig();
        requireFeature(cfg, "theme");
        const payload = await callNative("set_theme", { mode: msg.mode || "", preset: msg.preset || "" });
        await applyThemePayload(payload);
        return payload;
      })();

    case "omathunder:triageInbox":
      return runTriage(msg.language || "");

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
          customer: rule.customer || rule.name || "Filter",
          accountId: rule.accountId || "",
          folderPath: String(rule.folderPath).replace(/^\/+|\/+$/g, ""),
          domains: Array.isArray(rule.domains) ? rule.domains : [],
          fromContains: Array.isArray(rule.fromContains) ? rule.fromContains : [],
          subjectContains: Array.isArray(rule.subjectContains) ? rule.subjectContains : [],
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
        const effects = await applyKanbanSideEffects(cfg, msg.messageId, msg.column).catch((err) => ({ skipped: errorCode(err) }));
        return { ...res, effects };
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
            const res = await callNative("analyze_thread", {
              mode: backendMode,
              ephemeral: true,
              language: uiLanguage(cfg, msg.language),
              targetLanguage: msg.targetLanguage || "",
              tone: msg.tone || "freundlich",
              question: msg.question || "",
              primaryMessage: inputs[0],
              threadMessages: inputs,
            }, 120000);
            return { ...res, selectionCount: inputs.length, accountId: inputs[0]?.accountId || "" };
          }
          const primary = await buildMessageInput(headers[0], { includeAttachmentText });
          const threadMessages = await collectThreadMessages(primary);
          const res = await callNative("analyze_thread", {
            mode: msg.mode,
            ephemeral: Boolean(msg.ephemeral) || msg.mode === "extract_events",
            language: uiLanguage(cfg, msg.language),
            targetLanguage: msg.targetLanguage || "",
            tone: msg.tone || "freundlich",
            question: msg.question || "",
            primaryMessage: primary,
            threadMessages,
          }, 120000);
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
        return callNative("analyze_thread", {
          mode: msg.mode || "draft_reply",
          language: uiLanguage(cfg, msg.language),
          targetLanguage: msg.targetLanguage || "",
          tone: msg.tone || "freundlich",
          question: msg.question || "",
          userDraft: msg.userDraft || "",
          primaryMessage: primary,
          threadMessages,
        }, 120000);
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

    case "omathunder:saveConfig":
      return (async () => {
        const res = await callNative("save_config", msg.config);
        await syncFeatures(res?.config, res?.theme);
        return res;
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
  }
  return false;
});

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
  } catch (err) {
    console.warn("OmaThunder context menu setup note:", err);
  }
}

api.menus?.onClicked?.addListener(async (info, tab) => {
  const actions = {
    "oma-summarize-de": { auto: "summarize", target: "de" },
    "oma-summarize-en": { auto: "summarize", target: "en" },
    "oma-suggest-filter": { auto: "suggest_filter", target: "" },
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

// Optional: scan automatically when new mail arrives. Same limits and safety rules as a manual scan.
let newMailTimer = null;
api.messages?.onNewMailReceived?.addListener(() => {
  if (!features.triage) return;
  clearTimeout(newMailTimer);
  newMailTimer = setTimeout(async () => {
    try {
      const cfg = await loadConfig();
      if (!cfg.features?.triage || !cfg.mailbox?.scanOnNewMail) return;
      const res = await runTriage();
      broadcastToUI({ type: "omathunder:scanFinished", report: res.moveReport });
    } catch (err) {
      console.info("OmaThunder auto scan skipped:", errorCode(err));
    }
  }, 20000);
});

async function bootstrap() {
  await ensureSpacesTab();
  await registerContextMenus();
  try {
    const cfg = await callNative("get_config");
    // Drop a theme a previous session may have left behind if the theme block is now off.
    if (!cfg?.features?.theme) await removeTheme();
    await syncFeatures(cfg);
  } catch (err) {
    console.info("OmaThunder host not connected yet:", err?.message || err);
  }
}

api.runtime.onStartup?.addListener(() => {
  ensureSpacesTab();
});

bootstrap();
