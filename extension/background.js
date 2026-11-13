/* global browser, messenger */
const api = typeof messenger !== "undefined" ? messenger : browser;
const HOST_NAME = "dev.omathunder.host";

let nativePort = null;
let reqCounter = 0;
const pendingRequests = new Map();

let currentThemePayload = null;
let bodyScriptHandle = null;
let composeScriptHandle = null;
// Last known feature flags; refreshed from the host config. Theme stays off until the host confirms it.
let features = { theme: false, assistant: false, triage: false };

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
  if (api.messageDisplayScripts?.register && payload.bodyCss) {
    if (bodyScriptHandle) {
      await bodyScriptHandle.unregister().catch(() => {});
      bodyScriptHandle = null;
    }
    bodyScriptHandle = await api.messageDisplayScripts.register({
      css: [{ code: payload.bodyCss }],
      js: [{ file: "/content/message-theme.js" }],
    }).catch(() => null);
  }
  if (api.composeScripts?.register && payload.composeCss) {
    if (composeScriptHandle) {
      await composeScriptHandle.unregister().catch(() => {});
      composeScriptHandle = null;
    }
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
  features = next;
  if (!next.theme) {
    if (themeWasOn || currentThemePayload) await removeTheme();
    return;
  }
  const payload = themePayload || (!themeWasOn ? await callNative("get_theme").catch(() => null) : null);
  if (payload) await applyThemePayload(payload);
}

// ---------------------------------------------------------------------------
// Safety layer: every change to mails, folders or tags goes through these helpers.
// ---------------------------------------------------------------------------

class PolicyError extends Error {
  constructor(code) {
    super(code);
    this.code = code;
  }
}

async function loadConfig() {
  const cfg = await callNative("get_config");
  await syncFeatures(cfg);
  return cfg;
}

function requireFeature(cfg, name) {
  if (!cfg?.features?.[name]) throw new PolicyError(`feature_disabled:${name}`);
}

function triageAccountIds(cfg) {
  return new Set(cfg?.mailbox?.triageAccountIds || []);
}

// Throws unless OmaThunder may change mails/folders/tags in this account.
function requireWritable(cfg, accountId) {
  if (cfg?.mailbox?.readOnly !== false) throw new PolicyError("read_only");
  if (!accountId || !triageAccountIds(cfg).has(accountId)) throw new PolicyError("account_not_enabled");
}

const mailbox = {
  async move(cfg, header, targetFolder) {
    const accountId = header?.folder?.accountId;
    requireWritable(cfg, accountId);
    if (!targetFolder || targetFolder.accountId !== accountId) throw new PolicyError("cross_account_move");
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
    const kept = (header.tags || []).filter((t) => !(replacePrefix && t.startsWith(replacePrefix)));
    const merged = [...new Set([...kept, ...tagDefs.map((d) => d.key)])];
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

function isInboxFolder(f) {
  if (!f) return false;
  if (f.type === "inbox" || (f.specialUse || []).includes("inbox")) return true;
  return (f.path || "").replace(/^\/+/, "").toLowerCase() === "inbox";
}

function findAllInboxes(folders) {
  return folders.filter(isInboxFolder);
}

// Finds a folder strictly inside one account (no fallback to other accounts).
function findFolderInAccount(folders, accountId, folderId, folderPath) {
  if (!accountId) return null;
  if (folderId) {
    const byId = folders.find((f) => f.id === folderId && f.accountId === accountId);
    if (byId) return byId;
  }
  const needle = (folderPath || "").replace(/^\/+|\/+$/g, "").toLowerCase();
  if (!needle) return null;
  return folders.find((f) => f.accountId === accountId && f.path.toLowerCase() === needle) || null;
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

function extractPlainText(part) {
  if (!part) return "";
  if (part.contentType && part.contentType.toLowerCase().startsWith("text/plain") && part.body) {
    return part.body;
  }
  if (Array.isArray(part.parts)) {
    for (const child of part.parts) {
      const text = extractPlainText(child);
      if (text) return text;
    }
  }
  if (part.contentType && part.contentType.toLowerCase().startsWith("text/html") && part.body) {
    return part.body.replace(/<[^>]+>/g, " ").replace(/\s+/g, " ").trim();
  }
  return "";
}

async function buildMessageInput(header) {
  const full = await api.messages.getFull(header.id).catch(() => null);
  const attachments = await api.messages.listAttachments(header.id).catch(() => []);
  const headers = {};
  if (full?.headers) {
    for (const [k, vals] of Object.entries(full.headers)) {
      if (Array.isArray(vals) && vals.length > 0) headers[k] = String(vals[0]);
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
    bodyText: extractPlainText(full).slice(0, 6000),
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
    if (direct && hdrId && direct.headerMessageId === hdrId) return direct;
    if (direct && !hdrId && !strict) return direct;
  }
  if (!hdrId || !api.messages?.query) return null;
  const res = await api.messages.query({ headerMessageId: hdrId }).catch(() => null);
  let candidates = res?.messages || [];
  if (ref.folderId) {
    const inFolder = candidates.filter((m) => m.folder?.id === ref.folderId);
    if (inFolder.length === 1) return inFolder[0];
  }
  if (ref.accountId) candidates = candidates.filter((m) => m.folder?.accountId === ref.accountId);
  if (candidates.length === 1) return candidates[0];
  return strict ? null : candidates[0] || null;
}

// Returns the newest `limit` mails of a folder received within the last `days` days.
async function listRecentMessages(folder, limit, days) {
  const since = new Date(Date.now() - days * 86400000);
  let page = await api.messages.query({ folderId: folder.id, fromDate: since }).catch(() => null);
  if (!page) page = await api.messages.list(folder.id).catch(() => null);
  const recent = [];
  let pages = 0;
  while (page) {
    for (const m of page.messages || []) {
      if (!m.date || new Date(m.date) >= since) recent.push(m);
    }
    if (!page.id || ++pages >= 20) break;
    page = await api.messages.continueList(page.id).catch(() => null);
  }
  recent.sort((a, b) => new Date(b.date || 0) - new Date(a.date || 0));
  return recent.slice(0, limit);
}

// Scans only accounts enabled for triage: their INBOX plus rule folders (with subfolders) marked for the board.
async function collectScanMessages(cfg) {
  const enabled = triageAccountIds(cfg);
  const limit = cfg?.mailbox?.scanLimit || 20;
  const days = cfg?.mailbox?.scanDays || 30;
  const folders = await listAllFolders();
  const scan = new Map();
  for (const inbox of findAllInboxes(folders)) {
    if (enabled.has(inbox.accountId)) scan.set(inbox.id, inbox);
  }
  for (const rule of cfg?.rules || []) {
    if (!rule.includeInScan) continue;
    const root = (rule.folderPath || "").replace(/^\/+|\/+$/g, "").toLowerCase();
    if (!root) continue;
    for (const f of folders) {
      if (!enabled.has(f.accountId)) continue;
      if (rule.accountId && f.accountId !== rule.accountId) continue;
      const path = f.path.toLowerCase();
      if (path === root || path.startsWith(`${root}/`)) scan.set(f.id, f);
    }
  }
  const messages = [];
  for (const f of scan.values()) {
    for (const header of await listRecentMessages(f, limit, days)) {
      messages.push(await buildMessageInput(header));
    }
  }
  return { messages, folders, scannedFolders: scan.size };
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

async function runTriage() {
  const cfg = await loadConfig();
  requireFeature(cfg, "triage");
  if (triageAccountIds(cfg).size === 0) throw new PolicyError("no_triage_accounts");

  const { messages, folders, scannedFolders } = await collectScanMessages(cfg);
  // Classify in small batches: keeps each native message small and each call within its timeout.
  let res = { results: [], plannedMoves: [], skippedAccounts: 0 };
  const batchSize = 10;
  for (let i = 0; i < messages.length || i === 0; i += batchSize) {
    const part = await callNative("classify_batch", { messages: messages.slice(i, i + batchSize) }, 300000);
    res = {
      ...part,
      results: [...res.results, ...(part.results || [])],
      plannedMoves: [...res.plannedMoves, ...(part.plannedMoves || [])],
      skippedAccounts: res.skippedAccounts + (part.skippedAccounts || 0),
    };
  }
  const planned = res.plannedMoves || [];
  const report = {
    readOnly: cfg.mailbox?.readOnly !== false,
    scannedFolders,
    scannedMessages: messages.length,
    planned,
    moved: [],
    failed: [],
  };
  if (!report.readOnly) {
    const recorded = [];
    for (const plan of planned) {
      try {
        recorded.push(await executePlannedMove(cfg, plan, folders));
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

async function getActiveDisplayedMessage() {
  const header = await findDisplayedHeader();
  return header ? buildMessageInput(header) : null;
}

function normalizeSubjectForThread(subj) {
  return (subj || "").replace(/^(?:re|aw|fwd|wg):\s*/gi, "").trim();
}

async function collectThreadMessages(primary) {
  if (!primary || !api.messages?.query) return primary ? [primary] : [];
  const baseSubj = normalizeSubjectForThread(primary.subject);
  if (!baseSubj) return [primary];
  const queryRes = await api.messages.query({
    accountId: primary.accountId || undefined,
    subject: baseSubj,
  }).catch(() => ({ messages: [] }));

  const seen = new Set([primary.threadId || primary.id]);
  const list = [primary];
  for (const h of (queryRes.messages || []).slice(0, 16)) {
    if (normalizeSubjectForThread(h.subject).toLowerCase() !== baseSubj.toLowerCase()) continue;
    const key = h.headerMessageId || String(h.id);
    if (seen.has(key)) continue;
    seen.add(key);
    list.push(await buildMessageInput(h));
    if (list.length >= 8) break;
  }
  list.sort((a, b) => new Date(a.date || 0) - new Date(b.date || 0));
  return list;
}

async function getActiveComposeDraft() {
  const tabs = await api.tabs.query({ type: "messageCompose" }).catch(() => []);
  if (!tabs || tabs.length === 0) return null;
  const details = await api.compose.getComposeDetails(tabs[0].id).catch(() => null);
  if (!details) return null;
  return {
    tabId: tabs[0].id,
    subject: details.subject || "",
    body: details.plainTextBody || (details.body ? details.body.replace(/<[^>]+>/g, " ").trim() : ""),
  };
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

async function applyProposalInThunderbird(proposal) {
  if (!proposal) throw new Error("Proposal missing");
  const cfg = await loadConfig();
  requireFeature(cfg, "assistant");
  const live = await resolveMessage({ messageId: proposal.messageId, headerMessageId: proposal.threadId || "" });
  if (!live) throw new PolicyError("message_not_found");

  if (proposal.kind === "draft_reply") {
    // Opens a compose window only. Nothing is saved or sent without the user. Allowed in read-only mode.
    await api.compose.beginReply(live.id, {
      plainTextBody: proposal.body || "",
      subject: proposal.subject || undefined,
    });
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

// Moves mails that a deleted rule had auto-moved back to where they came from.
// Only touches mails that are still in the folder OmaThunder moved them to.
async function revertRuleMoves(cfg, revertMoves) {
  const { folders } = await listAccountFolders();
  const report = { reverted: 0, skipped: [], failed: [] };
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
      report.reverted++;
    } catch (err) {
      report.failed.push({ ...m, error: errorCode(err) });
    }
  }
  return report;
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
          const [themePayload, themes, stateData, doctor, folderData] = await Promise.all([
            callNative("get_theme"),
            callNative("list_themes"),
            callNative("get_state"),
            callNative("doctor"),
            listAccountFolders().catch(() => ({ folders: [], accounts: [] })),
          ]);
          return {
            connected: true,
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
      return runTriage();

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
        const saved = await callNative("save_rules", { incomingFolder: msg.incomingFolder || "INBOX", rules: nextRules });
        return { config: saved, folderNotes };
      })();

    case "omathunder:deleteRule":
      return (async () => {
        const cfg = await loadConfig();
        requireFeature(cfg, "triage");
        const revert = Boolean(msg.revertToIncoming);
        const res = await callNative("delete_rule", { ruleId: msg.ruleId, revertToIncoming: revert });
        if (!revert || !Array.isArray(res?.revertMoves) || res.revertMoves.length === 0) return res;
        return { ...res, revertReport: await revertRuleMoves(cfg, res.revertMoves) };
      })();

    case "omathunder:moveKanban":
      return (async () => {
        const cfg = await loadConfig();
        requireFeature(cfg, "triage");
        const res = await callNative("move_kanban", { messageId: msg.messageId, column: msg.column });
        const effects = await applyKanbanSideEffects(cfg, msg.messageId, msg.column).catch((err) => ({ skipped: errorCode(err) }));
        return { ...res, effects };
      })();

    case "omathunder:analyzeMessage":
      return (async () => {
        const cfg = await loadConfig();
        requireFeature(cfg, "assistant");
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
          primary = await buildMessageInput(header);
        } else {
          primary = await getActiveDisplayedMessage();
        }
        if (!primary) throw new PolicyError("no_message_displayed");
        const threadMessages = await collectThreadMessages(primary);
        return callNative("analyze_thread", {
          mode: msg.mode || "draft_reply",
          language: msg.language || "",
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
      return applyProposalInThunderbird(msg.proposal);

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
        const id = Number(msg.messageId);
        if (Number.isFinite(id) && api.messageDisplay?.open) {
          await api.messageDisplay.open({ messageId: id });
        }
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

async function bootstrap() {
  await ensureSpacesTab();
  try {
    const cfg = await callNative("get_config");
    // Drop a theme a previous session may have left behind if the theme block is now off.
    if (!cfg?.features?.theme) await removeTheme();
    await syncFeatures(cfg);
  } catch (err) {
    console.info("OmaThunder host not connected yet:", err?.message || err);
  }
}

bootstrap();
