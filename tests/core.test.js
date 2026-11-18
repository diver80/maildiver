/* Unit tests for extension/lib/core.js. Run from the repo root with either
 *   node tests/core.test.js
 *   jsc tests/core.test.js        (macOS: /System/Library/Frameworks/JavaScriptCore.framework/Versions/A/Helpers/jsc)
 */
if (typeof require !== "undefined") {
  require("../extension/lib/core.js");
} else {
  load("extension/lib/core.js");
}
const core = globalThis.OmaCore;
const out = typeof print === "function" ? print : console.log;

let failed = 0;
let count = 0;
const asyncQueue = [];
function test(name, fn) {
  count++;
  try {
    fn();
  } catch (err) {
    failed++;
    out(`FAIL ${name}: ${err.message}`);
  }
}
function testAsync(name, fn) {
  count++;
  asyncQueue.push(
    Promise.resolve()
      .then(() => fn())
      .catch((err) => {
        failed++;
        out(`FAIL ${name}: ${err.message}`);
      }),
  );
}
function eq(got, want, msg = "") {
  const a = JSON.stringify(got);
  const b = JSON.stringify(want);
  if (a !== b) throw new Error(`${msg} got ${a}, want ${b}`);
}
function throwsCode(fn, code) {
  try {
    fn();
  } catch (err) {
    if (err.code !== code) throw new Error(`expected ${code}, got ${err.code || err.message}`);
    return;
  }
  throw new Error(`expected ${code}, nothing thrown`);
}

const folders = [
  { id: "work:/INBOX", accountId: "work", path: "INBOX", type: "inbox" },
  { id: "work:/Kunden/Acme", accountId: "work", path: "Kunden/Acme" },
  { id: "work:/Kunden/Acme/Erledigt", accountId: "work", path: "Kunden/Acme/Erledigt" },
  { id: "work:/Kunden/Acme2", accountId: "work", path: "Kunden/Acme2" },
  { id: "priv:/INBOX", accountId: "priv", path: "INBOX", specialUse: ["inbox"] },
  { id: "priv:/Kunden/Acme", accountId: "priv", path: "Kunden/Acme" },
];
const writable = { features: { triage: true }, mailbox: { readOnly: false, triageAccountIds: ["work"] } };

test("inbox detection", () => {
  eq(core.isInboxFolder(folders[0]), true);
  eq(core.isInboxFolder(folders[4]), true);
  eq(core.isInboxFolder({ path: "/INBOX" }), true);
  eq(core.isInboxFolder({ path: "INBOX/Acme" }), false);
  eq(core.isInboxFolder(null), false);
});

test("folder lookup never leaves the account", () => {
  eq(core.findFolderInAccount(folders, "work", "", "kunden/acme").id, "work:/Kunden/Acme");
  eq(core.findFolderInAccount(folders, "priv", "work:/Kunden/Acme", "Kunden/Acme").id, "priv:/Kunden/Acme");
  eq(core.findFolderInAccount(folders, "priv", "work:/Kunden/Acme2", "Kunden/Acme2"), null);
  eq(core.findFolderInAccount(folders, "", "", "Kunden/Acme"), null);
});

test("write guard", () => {
  core.requireWritable(writable, "work");
  throwsCode(() => core.requireWritable(writable, "priv"), "account_not_enabled");
  throwsCode(() => core.requireWritable(writable, ""), "account_not_enabled");
  throwsCode(() => core.requireWritable({ mailbox: { readOnly: true, triageAccountIds: ["work"] } }, "work"), "read_only");
  throwsCode(() => core.requireWritable({}, "work"), "read_only");
  throwsCode(() => core.requireWritable(null, "work"), "read_only");
});

test("moves stay inside one writable account", () => {
  const header = { folder: { accountId: "work" } };
  core.requireMovable(writable, header, folders[1]);
  throwsCode(() => core.requireMovable(writable, header, folders[5]), "cross_account_move");
  throwsCode(() => core.requireMovable(writable, header, null), "cross_account_move");
  throwsCode(() => core.requireMovable(writable, { folder: { accountId: "priv" } }, folders[5]), "account_not_enabled");
});

test("feature gate", () => {
  core.requireFeature(writable, "triage");
  throwsCode(() => core.requireFeature(writable, "assistant"), "feature_disabled:assistant");
  throwsCode(() => core.requireFeature(undefined, "theme"), "feature_disabled:theme");
});

test("stale numeric IDs are rejected", () => {
  const ref = { messageId: "5", headerMessageId: "a@x" };
  eq(core.acceptDirectHit(ref, { headerMessageId: "a@x" }, true), true);
  eq(core.acceptDirectHit(ref, { headerMessageId: "other@x" }, true), false, "different mail under the same numeric ID");
  eq(core.acceptDirectHit(ref, { headerMessageId: "other@x" }, false), false);
  eq(core.acceptDirectHit({ messageId: "5" }, { headerMessageId: "a@x" }, true), false, "no Message-ID stored, strict");
  eq(core.acceptDirectHit({ messageId: "5" }, { headerMessageId: "a@x" }, false), true);
  eq(core.acceptDirectHit(ref, null, false), false);
});

test("ambiguous Message-ID matches are refused in strict mode", () => {
  const a = { id: 1, folder: { id: "work:/INBOX", accountId: "work" } };
  const b = { id: 2, folder: { id: "priv:/INBOX", accountId: "priv" } };
  const c = { id: 3, folder: { id: "work:/Kunden/Acme", accountId: "work" } };
  eq(core.pickMessageCandidate({}, [a], true).id, 1);
  eq(core.pickMessageCandidate({}, [a, b], true), null);
  eq(core.pickMessageCandidate({}, [a, b], false).id, 1);
  eq(core.pickMessageCandidate({ accountId: "priv" }, [a, b], true).id, 2);
  eq(core.pickMessageCandidate({ accountId: "work" }, [a, b, c], true), null);
  eq(core.pickMessageCandidate({ accountId: "work", folderId: "work:/Kunden/Acme" }, [a, b, c], true).id, 3);
  eq(core.pickMessageCandidate({ accountId: "work" }, [], true), null);
});

test("subject normalization", () => {
  eq(core.normalizeSubjectForThread("Re: AW: Fwd: Angebot"), "Angebot");
  eq(core.normalizeSubjectForThread("Rechnung"), "Rechnung");
});

test("thread is built from References, not from the subject", () => {
  const primary = { threadId: "c@x", date: "2026-11-12", headers: { references: "<a@x> <b@x>", "in-reply-to": "<b@x>" } };
  const root = { threadId: "a@x", date: "2026-11-10", headers: {} };
  const reply = { threadId: "d@x", date: "2026-11-13", headers: { "in-reply-to": "<c@x>" } };
  const replyToReply = { threadId: "e@x", date: "2026-11-14", headers: { references: "<d@x>" } };
  const sameSubjectOtherThread = { threadId: "z@y", date: "2026-11-11", headers: { references: "<q@y>" } };
  const got = core.selectThread(primary, [replyToReply, sameSubjectOtherThread, reply, root, primary]);
  eq(got.map((m) => m.threadId), ["a@x", "c@x", "d@x", "e@x"]);
  eq(core.selectThread(primary, [sameSubjectOtherThread]).map((m) => m.threadId), ["c@x"]);
  eq(core.parseMessageIds("<a@x>\n <b@x>", "<a@x>"), ["a@x", "b@x"]);
});

test("thread is capped but always keeps the primary mail", () => {
  const primary = { threadId: "p", date: "2026-01-01", headers: {} };
  const many = Array.from({ length: 12 }, (_, i) => ({ threadId: `r${i}`, date: `2026-02-${String(i + 1).padStart(2, "0")}`, headers: { references: "<p>" } }));
  const got = core.selectThread(primary, many, 8);
  eq(got.length, 8);
  eq(got[0].threadId, "p");
  eq(got[7].threadId, "r11");
});

test("archive path builder maps INBOX to Incoming/YYYY/MM and preserves sorted folders", () => {
  eq(
    core.buildArchiveRelativeParts("mail@frankhess.de", "INBOX", "2023-07-14T10:20:00Z"),
    ["mail@frankhess.de", "Incoming", "2023", "07"],
  );
  eq(
    core.buildArchiveRelativeParts("mail@frankhess.de", "Rechnungen/Strom", "2022-11-05T08:00:00Z"),
    ["mail@frankhess.de", "Rechnungen", "Strom", "2022", "11"],
  );
});

test("archive local-folder move and delete guards enforce read-only, account enablement, and local account type", () => {
  const accounts = [
    { id: "imap1", name: "mail@frankhess.de", type: "imap" },
    { id: "imap2", name: "other@example.com", type: "imap" },
    { id: "local1", name: "Lokale Ordner", type: "none" },
  ];
  const srcHeader = { id: 10, folder: { id: "imap1:/INBOX", accountId: "imap1" } };
  const localFolder = { id: "local1:/OmaThunder-Archiv", accountId: "local1" };
  const otherImapFolder = { id: "imap2:/Archiv", accountId: "imap2" };

  const roCfg = { features: { triage: true }, mailbox: { readOnly: true, triageAccountIds: ["imap1"] } };
  throwsCode(() => core.requireLocalArchiveMove(roCfg, srcHeader, localFolder, accounts), "read_only");
  throwsCode(() => core.requireArchiveDelete(roCfg, srcHeader), "read_only");

  const rwCfg = { features: { triage: true }, mailbox: { readOnly: false, triageAccountIds: ["imap1"] } };
  // Cross-account to another IMAP account is strictly blocked.
  throwsCode(() => core.requireLocalArchiveMove(rwCfg, srcHeader, otherImapFolder, accounts), "local_folder_required");
  // Moving from unenabled account is blocked.
  throwsCode(() => core.requireLocalArchiveMove(rwCfg, { id: 11, folder: { accountId: "imap2" } }, localFolder, accounts), "account_not_enabled");
  // Moving from enabled writable account to Local Folders (type "none") succeeds.
  core.requireLocalArchiveMove(rwCfg, srcHeader, localFolder, accounts);
  core.requireArchiveDelete(rwCfg, srcHeader);
});

test("matchArchiveCandidate filters by age, read flag, and starred flag", () => {
  const now = Date.UTC(2026, 10, 15, 0, 0, 0);
  const oldRead = { date: "2024-01-01T00:00:00Z", read: true, flagged: false };
  const recentRead = { date: "2026-10-01T00:00:00Z", read: true, flagged: false };
  const oldUnread = { date: "2024-01-01T00:00:00Z", read: false, flagged: false };
  const oldStarred = { date: "2024-01-01T00:00:00Z", read: true, flagged: true };

  const crit = { olderThanDays: 365, onlyRead: true, excludeStarred: true };
  eq(core.matchArchiveCandidate(oldRead, crit, now), true);
  eq(core.matchArchiveCandidate(recentRead, crit, now), false);
  eq(core.matchArchiveCandidate(oldUnread, crit, now), false);
  eq(core.matchArchiveCandidate(oldStarred, crit, now), false);
});

test("selectScanMessages keeps ALL unread mails regardless of limit/since and caps read mails", () => {
  const since = new Date("2026-10-15T00:00:00Z");
  const msgs = [
    { id: 1, date: "2024-01-10T10:00:00Z", read: false }, // old unread -> MUST be kept
    { id: 2, date: "2026-11-10T10:00:00Z", read: false }, // recent unread -> kept
    { id: 3, date: "2026-11-09T10:00:00Z", read: false }, // recent unread -> kept
    { id: 2, date: "2026-11-10T10:00:00Z", read: false }, // duplicate ID -> deduped
    { id: 4, date: "2026-11-12T10:00:00Z", read: true },  // newest read -> kept
    { id: 5, date: "2026-11-01T10:00:00Z", read: true },  // 2nd read -> dropped with limit=1
    { id: 6, date: "2025-05-01T10:00:00Z", read: true },  // old read -> dropped by since
  ];
  eq(core.selectScanMessages(msgs, since, 1).map((m) => m.id), [2, 3, 1, 4]);
});

test("requireBridgeSend enforces writable account and explicit bridge.allowSend", () => {
  const sendDisabled = { mailbox: { readOnly: false, triageAccountIds: ["work"] }, bridge: { allowSend: false } };
  throwsCode(() => core.requireBridgeSend(sendDisabled, "work"), "send_disabled");
  throwsCode(() => core.requireBridgeSend({ mailbox: { readOnly: true, triageAccountIds: ["work"] }, bridge: { allowSend: true } }, "work"), "read_only");
  throwsCode(() => core.requireBridgeSend({ mailbox: { readOnly: false, triageAccountIds: ["work"] }, bridge: { allowSend: true } }, "priv"), "account_not_enabled");
  core.requireBridgeSend({ mailbox: { readOnly: false, triageAccountIds: ["work"] }, bridge: { allowSend: true } }, "work");
});

test("summarizeMessageHeader and matchMessageQuery normalize headers and evaluate search tokens", () => {
  const header = {
    id: 42,
    headerMessageId: "msg-42@example.com",
    subject: "Re: Vertrag Update",
    author: "Alice <alice@acme.de>",
    recipients: ["frank@avono.de"],
    ccList: ["team@avono.de"],
    date: "2026-11-17T09:00:00Z",
    read: false,
    flagged: true,
    tags: ["$label1"],
    size: 2048,
    folder: { id: "work:/INBOX", accountId: "work", path: "/INBOX", type: "inbox" },
  };
  const summary = core.summarizeMessageHeader(header);
  eq(summary.id, "42");
  eq(summary.threadId, "msg-42@example.com");
  eq(summary.folder, "INBOX");
  eq(summary.inInbox, true);
  eq(summary.unread, true);
  eq(summary.flagged, true);

  eq(core.matchMessageQuery(header, ""), true);
  eq(core.matchMessageQuery(header, "from:alice@acme.de subject:vertrag"), true);
  eq(core.matchMessageQuery(header, "to:team@avono.de"), true);
  eq(core.matchMessageQuery(header, "from:bob@acme.de"), false);
  eq(core.matchMessageQuery(header, "subject:rechnung"), false);
});

// --- Local bridge policy (CONTRACT §1-§5) ---
const bridgeCfg = (over = {}) => ({
  mailbox: { readOnly: false, triageAccountIds: ["work"], ...(over.mailbox || {}) },
  bridge: { enabled: true, allowSend: false, accountIds: [], ...(over.bridge || {}) },
});

test("PolicyError carries code and optional detail", () => {
  const plain = new core.PolicyError("read_only");
  eq([plain.code, plain.message, plain.detail], ["read_only", "read_only", ""]);
  const detailed = new core.PolicyError("not_found", "message 5");
  eq([detailed.code, detailed.message], ["not_found", "not_found: message 5"]);
});

test("bridge settings: missing or malformed config means off and nothing readable", () => {
  eq(core.bridgeSettings(null), { enabled: false, allowSend: false, accountIds: [] });
  eq(core.bridgeSettings({ bridge: "yes" }).enabled, false);
  eq(core.bridgeSettings({ bridge: { enabled: "true" } }).enabled, false, "only boolean true enables");
  eq(core.bridgeSettings(bridgeCfg()).accountIds, null, "empty list = all accounts");
  eq(core.isBridgeReadable(null, "work"), false);
  eq(core.isBridgeReadable(bridgeCfg(), "anything"), true);
  eq(core.isBridgeReadable(bridgeCfg({ bridge: { accountIds: ["work"] } }), "work"), true);
  eq(core.isBridgeReadable(bridgeCfg({ bridge: { accountIds: ["work"] } }), "priv"), false);
  eq(core.isBridgeReadable(bridgeCfg({ bridge: { accountIds: "work" } }), "work"), false, "malformed restriction");
  throwsCode(() => core.requireBridgeReadable(bridgeCfg({ bridge: { accountIds: ["work"] } }), "priv"), "account_not_readable");
  core.requireBridgeWritable(bridgeCfg(), "work");
  throwsCode(() => core.requireBridgeWritable(bridgeCfg({ bridge: { accountIds: ["priv"] } }), "work"), "account_not_readable");
  throwsCode(() => core.requireBridgeWritable(bridgeCfg({ mailbox: { readOnly: true } }), "work"), "read_only");
  throwsCode(() => core.requireBridgeSend(bridgeCfg({ bridge: { allowSend: true, accountIds: ["priv"] } }), "work"), "account_not_readable");
  core.requireBridgeSend(bridgeCfg({ bridge: { allowSend: true } }), "work");
});

test("every bridge RPC method has a policy entry with a known kind", () => {
  const kinds = new Set(["meta", "read", "write", "send", "filter", "calendar", "contacts", "ui"]);
  const methods = Object.keys(core.BRIDGE_RPC_POLICY);
  for (const m of [
    "ping", "accounts.list", "folders.list", "messages.list", "messages.get", "messages.thread", "messages.actions",
    "compose.draft", "compose.send", "calendar.list", "calendar.events", "calendar.create", "calendar.update",
    "calendar.delete", "contacts.search", "contacts.upsert", "filters.list", "filters.upsert", "filters.run", "filters.openDialog",
  ]) {
    if (!methods.includes(m)) throw new Error(`missing policy for ${m}`);
  }
  for (const m of methods) {
    if (!kinds.has(core.BRIDGE_RPC_POLICY[m].kind)) throw new Error(`unknown kind for ${m}`);
  }
});

test("checkBridgeRPC applies the central policy before any handler", () => {
  throwsCode(() => core.checkBridgeRPC(bridgeCfg(), "messages.delete", {}), "invalid_argument");
  eq(core.checkBridgeRPC(null, "ping", {}).kind, "meta");
  throwsCode(() => core.checkBridgeRPC(null, "messages.list", {}), "feature_disabled:bridge");
  throwsCode(() => core.checkBridgeRPC(bridgeCfg({ bridge: { enabled: false } }), "accounts.list", {}), "feature_disabled:bridge");
  core.checkBridgeRPC(bridgeCfg({ mailbox: { readOnly: true } }), "messages.list", {});
  const restricted = bridgeCfg({ bridge: { accountIds: ["work"] } });
  throwsCode(() => core.checkBridgeRPC(restricted, "messages.list", { accountId: "priv" }), "account_not_readable");
  throwsCode(() => core.checkBridgeRPC(restricted, "folders.list", { accountId: "priv" }), "account_not_readable");
  throwsCode(() => core.checkBridgeRPC(restricted, "filters.openDialog", { accountId: "priv" }), "account_not_readable");
  core.checkBridgeRPC(restricted, "messages.list", { accountId: "work" });
  // writes
  const ro = bridgeCfg({ mailbox: { readOnly: true } });
  throwsCode(() => core.checkBridgeRPC(ro, "messages.actions", { id: "1" }), "read_only");
  throwsCode(() => core.checkBridgeRPC(ro, "compose.draft", {}), "read_only");
  core.checkBridgeRPC(ro, "compose.draft", { openWindow: true });
  throwsCode(() => core.checkBridgeRPC(ro, "compose.draft", { openWindow: "true" }), "read_only");
  throwsCode(() => core.checkBridgeRPC(ro, "calendar.create", {}), "read_only");
  throwsCode(() => core.checkBridgeRPC(ro, "contacts.upsert", {}), "read_only");
  core.checkBridgeRPC(ro, "calendar.events", {});
  core.checkBridgeRPC(ro, "contacts.search", {});
  // send
  throwsCode(() => core.checkBridgeRPC(bridgeCfg(), "compose.send", {}), "send_disabled");
  throwsCode(() => core.checkBridgeRPC(bridgeCfg({ mailbox: { readOnly: true }, bridge: { allowSend: true } }), "compose.send", {}), "read_only");
  core.checkBridgeRPC(bridgeCfg({ bridge: { allowSend: true } }), "compose.send", {});
  // filters: account scope, not only read-only
  throwsCode(() => core.checkBridgeRPC(bridgeCfg(), "filters.upsert", {}), "invalid_argument");
  throwsCode(() => core.checkBridgeRPC(bridgeCfg(), "filters.run", { accountId: "priv" }), "account_not_enabled");
  throwsCode(() => core.checkBridgeRPC(bridgeCfg({ bridge: { accountIds: ["priv"] } }), "filters.run", { accountId: "work" }), "account_not_readable");
  throwsCode(() => core.checkBridgeRPC(ro, "filters.upsert", { accountId: "work" }), "read_only");
  core.checkBridgeRPC(bridgeCfg(), "filters.run", { accountId: "work" });
});

test("bridge filter specs: no delete, valid conditions, same-account flag forced", () => {
  const base = { accountId: "work", mode: "create", name: "N", conditions: [{ field: "from", value: "@acme.de" }], actions: [{ type: "moveToFolder", targetFolderPath: "Acme" }] };
  const ok = core.sanitizeBridgeFilterSpec({ ...base, allowDelete: true, sameAccountOnly: false });
  eq([ok.allowDelete, ok.sameAccountOnly, ok.mode], [false, true, "create"]);
  throwsCode(() => core.sanitizeBridgeFilterSpec({ ...base, actions: [{ type: "delete" }] }), "invalid_argument");
  throwsCode(() => core.sanitizeBridgeFilterSpec({ ...base, actions: [{ type: "deleteFromServer" }] }), "invalid_argument");
  throwsCode(() => core.sanitizeBridgeFilterSpec({ ...base, actions: [{ type: "forward" }] }), "invalid_argument");
  throwsCode(() => core.sanitizeBridgeFilterSpec({ ...base, actions: [{ type: "moveToFolder" }] }), "invalid_argument");
  throwsCode(() => core.sanitizeBridgeFilterSpec({ ...base, conditions: [{ field: "from", value: "  " }] }), "invalid_argument");
  throwsCode(() => core.sanitizeBridgeFilterSpec({ ...base, actions: [] }), "invalid_argument");
  throwsCode(() => core.sanitizeBridgeFilterSpec({ ...base, mode: "replace" }), "invalid_argument");
  throwsCode(() => core.sanitizeBridgeFilterSpec({ ...base, mode: "nuke" }), "invalid_argument");
  core.sanitizeBridgeFilterSpec({ accountId: "work", mode: "toggle", existingName: "N", enabled: false });
  eq(core.hasNonEmptyCondition([{ value: "" }, null, { value: " x " }]), true);
  eq(core.hasNonEmptyCondition([{ value: "" }]), false);
});

test("bridgeErrorString produces CONTRACT §2 codes", () => {
  eq(core.bridgeErrorString(new core.PolicyError("read_only")), "read_only");
  eq(core.bridgeErrorString(new core.PolicyError("not_found", "message 5")), "not_found: message 5");
  eq(core.bridgeErrorString(new core.PolicyError("feature_disabled:bridge")), "feature_disabled:bridge");
  eq(core.bridgeErrorString(new core.PolicyError("message_not_found")), "not_found: message");
  eq(core.bridgeErrorString(new core.PolicyError("target_folder_missing")), "not_found: target folder");
  eq(core.bridgeErrorString(new Error("ambiguous: 2 copies")), "ambiguous: 2 copies");
  eq(core.bridgeErrorString(new Error("not_found: Calendar x")), "not_found: Calendar x");
  eq(core.bridgeErrorString(new Error("message_not_found")), "not_found: message");
  eq(core.bridgeErrorString(new Error("boom")), "boom");
  eq(core.bridgeErrorString(null), "internal_error");
});

test("bridge message resolution verifies numeric IDs and refuses ambiguous Message-IDs", () => {
  const cfg = bridgeCfg();
  const a = { id: 1, headerMessageId: "a@x", folder: { id: "work:/INBOX", accountId: "work" } };
  const d1 = { id: 2, headerMessageId: "d@x", folder: { id: "work:/INBOX", accountId: "work" } };
  const d2 = { id: 3, headerMessageId: "d@x", folder: { id: "priv:/INBOX", accountId: "priv" } };
  throwsCode(() => core.parseBridgeMessageRef({}), "invalid_argument");
  eq(core.parseBridgeMessageRef({ id: "<d@x>" }).headerMessageId, "d@x");
  eq(core.parseBridgeMessageRef({ id: "12", expectedMessageId: "<a@x>" }), {
    numericId: 12, headerMessageId: "a@x", expectedMessageId: "a@x", folderId: "", accountId: "",
  });

  eq(core.pickBridgeMessage(core.parseBridgeMessageRef({ id: "1" }), a, [], cfg).id, 1);
  eq(core.pickBridgeMessage(core.parseBridgeMessageRef({ id: "1", expectedMessageId: "a@x" }), a, [], cfg).id, 1);
  throwsCode(() => core.pickBridgeMessage(core.parseBridgeMessageRef({ id: "1", expectedMessageId: "b@x" }), a, [], cfg), "message_changed");
  throwsCode(() => core.pickBridgeMessage(core.parseBridgeMessageRef({ id: "9" }), null, [], cfg), "not_found");
  // stale numeric ID with expected Message-ID falls back to the unique copy
  eq(core.pickBridgeMessage(core.parseBridgeMessageRef({ id: "9", expectedMessageId: "a@x" }), null, [a], cfg).id, 1);

  const dup = core.parseBridgeMessageRef({ id: "d@x" });
  throwsCode(() => core.pickBridgeMessage(dup, null, [d1, d2], cfg), "ambiguous");
  eq(core.pickBridgeMessage(core.parseBridgeMessageRef({ id: "d@x", folderId: "priv:/INBOX" }), null, [d1, d2], cfg).id, 3);
  eq(core.pickBridgeMessage(core.parseBridgeMessageRef({ id: "d@x", accountId: "work" }), null, [d1, d2], cfg).id, 2);
  throwsCode(() => core.pickBridgeMessage(dup, null, [], cfg), "not_found");

  const restricted = bridgeCfg({ bridge: { accountIds: ["work"] } });
  eq(core.pickBridgeMessage(dup, null, [d1, d2], restricted).id, 2, "copies in unreadable accounts are ignored");
  throwsCode(() => core.pickBridgeMessage(core.parseBridgeMessageRef({ id: "3" }), d2, [], restricted), "account_not_readable");
  throwsCode(() => core.pickBridgeMessage(core.parseBridgeMessageRef({ id: "d@x", folderId: "priv:/INBOX" }), null, [d2], restricted), "account_not_readable");

  eq(core.summarizeMessageHeader(a).messageId, "a@x");
  eq(core.summarizeMessageHeader(a).threadId, "a@x");
});

test("compose policy uses the account owning the identity actually used", () => {
  const accounts = [
    { id: "local", identities: [] },
    { id: "work", identities: [{ id: "id-w1" }, { id: "id-w2" }] },
    { id: "priv", identities: [{ id: "id-p1" }] },
  ];
  eq(core.pickComposeIdentity(accounts, { identityId: "id-p1", accountId: "work" }), { identityId: "id-p1", accountId: "priv" });
  eq(core.pickComposeIdentity(accounts, { accountId: "work" }), { identityId: "id-w1", accountId: "work" });
  eq(core.pickComposeIdentity(accounts, { preferredAccountIds: ["local", "priv"] }), { identityId: "id-p1", accountId: "priv" });
  eq(core.pickComposeIdentity(accounts, {}), { identityId: "id-w1", accountId: "work" });
  throwsCode(() => core.pickComposeIdentity(accounts, { identityId: "nope" }), "not_found");
  throwsCode(() => core.pickComposeIdentity(accounts, { accountId: "nope" }), "not_found");
  throwsCode(() => core.pickComposeIdentity(accounts, { accountId: "local" }), "invalid_argument");
  throwsCode(() => core.pickComposeIdentity([{ id: "local", identities: [] }], {}), "not_found");
});

testAsync("idempotency cache replays outcomes and dedupes in-flight calls", async () => {
  let clock = 0;
  const cache = core.createIdempotencyCache({ ttlMs: 1000, now: () => clock });
  let runs = 0;
  let release;
  const gate = new Promise((r) => { release = r; });
  const job = () => { runs++; return gate.then(() => ({ ok: true, n: runs })); };
  const p1 = cache.run("compose.send", "k", job);
  const p2 = cache.run("compose.send", "k", job);
  release();
  const [r1, r2] = await Promise.all([p1, p2]);
  eq([runs, r1.n, r2.replayed], [1, 1, true]);
  const r3 = await cache.run("compose.send", "k", job);
  eq([runs, r3.replayed], [1, true]);
  await cache.run("compose.draft", "k", job); // other scope
  eq(runs, 2);
  clock = 5000; // expired
  await cache.run("compose.send", "k", job);
  eq(runs, 3);
  await cache.run("compose.send", "", job); // no key: no caching
  await cache.run("compose.send", "", job);
  eq(runs, 5);
  let failing = 0;
  await cache.run("compose.send", "f", () => { failing++; throw new Error("x"); }).catch(() => {});
  await cache.run("compose.send", "f", () => { failing++; return { ok: true }; });
  eq(failing, 2, "failures are not cached");
  let threw = false;
  await cache.run("compose.send", "x".repeat(300), job).catch((e) => { threw = e.code === "invalid_argument"; });
  eq(threw, true);
});

test("bridge event queue batches, caps, filters accounts and respects bridge.enabled", () => {
  const q = core.createBridgeEventQueue({ cap: 3 });
  for (let i = 1; i <= 5; i++) q.addUpdated(i, "work");
  q.addUpdated(1, "work"); // duplicate
  q.addUpdated(99, "priv");
  q.addDeleted(7, "work");
  q.addMoved({ oldId: 10, id: 11, fromFolderId: "work:/INBOX", toFolderId: "work:/Archiv", fromAccountId: "work", toAccountId: "work" });
  q.addMoved({ oldId: 20, id: 21, fromFolderId: "work:/INBOX", toFolderId: "priv:/INBOX", fromAccountId: "work", toAccountId: "priv" });
  q.addMoved({ oldId: 30, id: 31, fromFolderId: "priv:/INBOX", toFolderId: "work:/INBOX", fromAccountId: "priv", toAccountId: "work" });
  q.addCalendar("cal1", "e1");
  q.addCalendar("cal1", "e1");
  q.addCalendar("cal1", "e2");
  const events = q.drain(bridgeCfg({ bridge: { accountIds: ["work"] } }));
  const byName = Object.fromEntries(events.map((e) => [e.event, e.data]));
  eq(byName["mail.updated"], { ids: ["1", "2", "3"], accountIds: ["work"], truncated: true });
  eq(byName["mail.deleted"], { ids: ["7", "20"], accountIds: ["work"], truncated: false }, "moved out of a readable account = deleted");
  eq(byName["mail.moved"], { oldIds: ["10"], ids: ["11"], fromFolderIds: ["work:/INBOX"], toFolderIds: ["work:/Archiv"], accountIds: ["work"], truncated: false });
  eq(byName["calendar.changed"], { calendarId: "cal1", ids: ["e1", "e2"], truncated: false });
  eq(q.isEmpty(), true, "drain empties the queue");
  eq(q.drain(bridgeCfg()), []);

  q.addUpdated(1, "work");
  q.markTruncated("deleted");
  eq(q.drain(bridgeCfg({ bridge: { enabled: false } })), [], "nothing while the bridge is off");
  eq(q.isEmpty(), true);
  q.markTruncated("deleted");
  eq(q.drain(bridgeCfg()), [{ event: "mail.deleted", data: { ids: [], accountIds: [], truncated: true } }]);
});

test("mail.new payload carries up to 20 newest messages of readable accounts only", () => {
  const folder = { id: "work:/INBOX", accountId: "work", path: "/INBOX" };
  const headers = [];
  for (let i = 0; i < 25; i++) {
    headers.push({ id: i, headerMessageId: `m${i}@x`, subject: `S${i}`, author: "a@b", date: new Date(Date.UTC(2026, 10, 1, 0, i)), read: false, folder });
  }
  const ev = core.buildMailNewEvent(bridgeCfg(), folder, headers, 25);
  eq([ev.accountId, ev.folderId, ev.folder, ev.count, ev.messages.length], ["work", "work:/INBOX", "INBOX", 25, 20]);
  eq(ev.messages[0], { id: "24", messageId: "m24@x", subject: "S24", from: "a@b", date: "2026-11-01T00:24:00.000Z", accountId: "work", folderId: "work:/INBOX", unread: true });
  eq(core.buildMailNewEvent(bridgeCfg({ bridge: { accountIds: ["priv"] } }), folder, headers, 25), null);
  eq(core.buildMailNewEvent(bridgeCfg({ bridge: { enabled: false } }), folder, headers, 25), null);
  eq(core.buildMailNewEvent(null, folder, headers, 25), null);
});

test("native host reconnect backoff and missing-host detection", () => {
  eq([0, 1, 2, 3, 4, 5, 6, 20].map((n) => core.nativeReconnectDelay(n)), [1000, 2000, 4000, 8000, 16000, 30000, 30000, 30000]);
  eq(core.isNativeHostMissingError("No such native application dev.omathunder.host"), true);
  eq(core.isNativeHostMissingError("Host disconnected"), false);
  eq(core.isNativeHostMissingError(""), false);
});

test("messages.actions allows at most one folder change", () => {
  eq(core.messageMoveKind({}), "");
  eq(core.messageMoveKind({ read: true }), "");
  eq(core.messageMoveKind({ trash: true }), "trash");
  eq(core.messageMoveKind({ trash: false }), "");
  eq(core.messageMoveKind({ archive: true }), "archive");
  eq(core.messageMoveKind({ moveToFolderId: "f1" }), "move");
  eq(core.messageMoveKind({ moveToPath: "Archiv/2026" }), "move");
  throwsCode(() => core.messageMoveKind({ archive: true, trash: true }), "invalid_argument");
  throwsCode(() => core.messageMoveKind({ archive: true, moveToFolderId: "f1" }), "invalid_argument");
  throwsCode(() => core.messageMoveKind({ trash: true, moveToPath: "x" }), "invalid_argument");
  throwsCode(() => core.messageMoveKind({ archive: "yes" }), "invalid_argument");
  throwsCode(() => core.messageMoveKind({ trash: 1 }), "invalid_argument");
});

test("message security: plain, PGP/MIME, S/MIME, signed, inline", () => {
  const sec = core.describeMessageSecurity;
  const plain = { contentType: "multipart/alternative", headers: { "content-type": ["multipart/alternative; boundary=x"] }, parts: [{ contentType: "text/plain", body: "hi" }] };
  eq(sec(plain, { bodyText: "hi" }), { encrypted: false, decryption: "none", signed: false });
  eq(sec({ ...plain, decryptionStatus: "none" }, { decrypt: true, bodyText: "hi" }), { encrypted: false, decryption: "none", signed: false });

  // Thunderbird reports the decryption on the root part.
  eq(sec({ decryptionStatus: "skipped", headers: { "content-type": ["multipart/encrypted; protocol=\"application/pgp-encrypted\""] } }), { encrypted: true, decryption: "skipped", signed: false });
  eq(sec({ decryptionStatus: "fail" }, { decrypt: true }), { encrypted: true, decryption: "fail", signed: false });
  const decrypted = { decryptionStatus: "success", contentType: "multipart/signed", parts: [{ contentType: "text/plain", body: "x" }, { contentType: "application/pgp-signature" }] };
  eq(sec(decrypted, { decrypt: true, bodyText: "x" }), { encrypted: true, decryption: "success", signed: true });

  // Without decryptionStatus the content types decide.
  eq(sec({ contentType: "multipart/encrypted", parts: [{ contentType: "application/pgp-encrypted" }, { contentType: "application/octet-stream" }] }), { encrypted: true, decryption: "skipped", signed: false });
  eq(sec({ headers: { "content-type": ["application/pkcs7-mime; smime-type=enveloped-data; name=smime.p7m"] } }, { decrypt: true }), { encrypted: true, decryption: "fail", signed: false });
  eq(sec({ headers: { "content-type": ["application/pkcs7-mime; smime-type=signed-data"] } }), { encrypted: false, decryption: "none", signed: true });
  eq(sec({ contentType: "multipart/signed", parts: [{ contentType: "text/plain" }, { contentType: "application/pkcs7-signature" }] }, { bodyText: "x" }).signed, true);

  // Inline PGP shows up only in the text.
  eq(sec(plain, { bodyText: "-----BEGIN PGP SIGNED MESSAGE-----\nHash: SHA256\n\nhi" }), { encrypted: false, decryption: "none", signed: true });
  eq(sec(plain, { bodyText: "-----BEGIN PGP MESSAGE-----\nabc" }), { encrypted: true, decryption: "skipped", signed: false });
  eq(sec({ ...plain, decryptionStatus: "none" }, { decrypt: true, bodyText: "-----BEGIN PGP MESSAGE-----\nabc" }).decryption, "fail");

  eq(core.securityReadable({ encrypted: false, decryption: "none" }), true);
  eq(core.securityReadable({ encrypted: true, decryption: "success" }), true);
  eq(core.securityReadable({ encrypted: true, decryption: "skipped" }), false);
  eq(core.securityReadable({ encrypted: true, decryption: "fail" }), false);
  eq(sec(null), { encrypted: false, decryption: "none", signed: false });
});

test("compose security: sign on/off, encrypted replies, identities without key", () => {
  const plan = core.planComposeSecurity;
  const pgp = { name: "OpenPGP", signMessage: false, encryptBody: false, encryptSubject: false };
  eq(plan(pgp, {}), { patch: null, result: { technology: "OpenPGP", signed: false, encrypted: false } });
  eq(plan(pgp, { sign: "on" }).patch, { name: "OpenPGP", signMessage: true, encryptBody: false, encryptSubject: false });
  eq(plan({ ...pgp, signMessage: true }, { sign: "on" }).patch, null);
  eq(plan({ ...pgp, signMessage: true }, { sign: "off" }).result, { technology: "OpenPGP", signed: false, encrypted: false });
  // A reply to an encrypted mail is encrypted, whatever the identity's default.
  eq(plan(pgp, { requireEncryption: true }).result, { technology: "OpenPGP", signed: false, encrypted: true });
  eq(plan({ name: "S/MIME", signMessage: true, encryptBody: true }, { sign: "off", requireEncryption: true }).result, { technology: "S/MIME", signed: false, encrypted: true });
  // No key: nothing to change, but never a silent downgrade.
  eq(plan(undefined, {}), { patch: null, result: { technology: "", signed: false, encrypted: false } });
  eq(plan(undefined, { sign: "off" }).patch, null);
  throwsCode(() => plan(undefined, { sign: "on" }), "signing_unavailable");
  throwsCode(() => plan(undefined, { requireEncryption: true }), "encryption_unavailable");
  throwsCode(() => plan(pgp, { sign: "yes" }), "invalid_argument");

  core.verifyComposeSecurity({ signMessage: true, encryptBody: false }, { technology: "OpenPGP", signed: true, encrypted: false });
  core.verifyComposeSecurity(undefined, { technology: "", signed: false, encrypted: false });
  throwsCode(() => core.verifyComposeSecurity({ signMessage: false, encryptBody: false }, { technology: "OpenPGP", signed: true, encrypted: false }), "compose_security_unsupported");
  throwsCode(() => core.verifyComposeSecurity(undefined, { technology: "OpenPGP", signed: false, encrypted: true }), "compose_security_unsupported");
});

test("messages.open is a read-only UI call", () => {
  const cfg = { bridge: { enabled: true }, mailbox: { readOnly: true } };
  eq(core.checkBridgeRPC(cfg, "messages.open", { id: "1" }).kind, "ui");
});

test("host RPC: mailbox policy, never bridge methods", () => {
  const cfg = { features: { triage: true }, bridge: { enabled: false }, mailbox: { readOnly: true, triageAccountIds: ["a1"] } };
  // Works with the bridge switched off: these are the host's own jobs.
  eq(core.checkHostRPC(cfg, "host.triage.collect", {}).kind, "read");
  eq(core.checkHostRPC(cfg, "host.archive.candidates", { accountId: "a1" }).kind, "read");
  eq(core.checkHostRPC(cfg, "host.archive.raw", { accountId: "a1", messageId: "5" }).kind, "read");
  throwsCode(() => core.checkHostRPC(cfg, "host.archive.finish", { candidate: { accountId: "a1" }, action: "delete" }), "read_only");
  eq(core.checkHostRPC({ ...cfg, mailbox: { ...cfg.mailbox, readOnly: false } }, "host.archive.finish", { candidate: { accountId: "a1" } }).kind, "write");
  throwsCode(() => core.checkHostRPC(cfg, "host.archive.candidates", { accountId: "a2" }), "account_not_enabled");
  throwsCode(() => core.checkHostRPC(cfg, "host.archive.finish", { candidate: { accountId: "a2" } }), "account_not_enabled");
  throwsCode(() => core.checkHostRPC(cfg, "host.archive.candidates", {}), "invalid_argument");
  throwsCode(() => core.checkHostRPC({ ...cfg, features: { triage: false } }, "host.triage.collect", {}), "feature_disabled:triage");
  // Archive does not need the triage feature, only the account.
  eq(core.checkHostRPC({ ...cfg, features: { triage: false } }, "host.archive.candidates", { accountId: "a1" }).kind, "read");
  throwsCode(() => core.checkHostRPC({ ...cfg, mailbox: { triageAccountIds: [] } }, "host.triage.collect", {}), "no_triage_accounts");
  throwsCode(() => core.checkHostRPC(cfg, "messages.get", { id: "1" }), "invalid_argument");
  throwsCode(() => core.checkHostRPC(null, "host.triage.collect", {}), "host_config_unavailable");
  // ...and the bridge never accepts host methods.
  throwsCode(() => core.checkBridgeRPC({ bridge: { enabled: true } }, "host.archive.finish", {}), "invalid_argument");
});

Promise.all(asyncQueue).then(() => {
  out(`${count - failed}/${count} core tests passed`);
  if (failed > 0) {
    if (typeof quit === "function") quit(1);
    else process.exit(1);
  }
});
