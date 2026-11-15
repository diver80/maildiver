/* global ExtensionCommon, ChromeUtils, Components */
"use strict";

var { ExtensionCommon } = ChromeUtils.importESModule("resource://gre/modules/ExtensionCommon.sys.mjs");
var { MailServices } = ChromeUtils.importESModule("resource:///modules/MailServices.sys.mjs");

const _Ci = typeof Ci !== "undefined" ? Ci : Components.interfaces;
const _Services =
  typeof Services !== "undefined"
    ? Services
    : ChromeUtils.importESModule("resource://gre/modules/Services.sys.mjs").Services;

function fieldFromAttrib(attrib, term) {
  const A = _Ci.nsMsgSearchAttrib;
  if (term?.arbitraryHeader) return "header";
  switch (attrib) {
    case A.Sender:
      return "from";
    case A.To:
      return "to";
    case A.CC:
      return "cc";
    case A.ToOrCC:
      return "toOrCc";
    case A.AllAddresses:
      return "allAddresses";
    case A.Subject:
      return "subject";
    case A.Body:
      return "body";
    case A.Custom:
      return "custom";
    default:
      if (attrib >= A.OtherHeader && attrib < (A.kNumMsgSearchAttributes || 100)) {
        return "header";
      }
      return "other";
  }
}

function ensureCustomHeaderAttrib(headerName) {
  const A = _Ci.nsMsgSearchAttrib;
  const clean = String(headerName || "").trim();
  if (!clean) return A.OtherHeader;
  let list = [];
  try {
    const raw = _Services.prefs.getCharPref("mailnews.customHeaders", "");
    list = raw
      .split(":")
      .map((s) => s.trim())
      .filter(Boolean);
  } catch {
    list = [];
  }
  let idx = list.findIndex((h) => h.toLowerCase() === clean.toLowerCase());
  if (idx === -1) {
    list.push(clean);
    idx = list.length - 1;
    try {
      _Services.prefs.setCharPref("mailnews.customHeaders", list.join(": "));
    } catch {
      return A.OtherHeader;
    }
  }
  return A.OtherHeader + 1 + idx;
}

function attribFromCondition(cond) {
  const A = _Ci.nsMsgSearchAttrib;
  switch (String(cond?.field || "").toLowerCase()) {
    case "from":
    case "sender":
      return A.Sender;
    case "to":
      return A.To;
    case "cc":
      return A.CC;
    case "toorcc":
    case "to_or_cc":
      return A.ToOrCC;
    case "alladdresses":
    case "all_addresses":
      return A.AllAddresses;
    case "subject":
      return A.Subject;
    case "body":
      return A.Body;
    case "header":
      return ensureCustomHeaderAttrib(cond.headerName);
    default:
      return A.Subject;
  }
}

function opFromCode(op) {
  const O = _Ci.nsMsgSearchOp;
  switch (op) {
    case O.Contains:
      return "contains";
    case O.DoesntContain:
      return "doesNotContain";
    case O.Is:
      return "is";
    case O.Isnt:
      return "isNot";
    case O.BeginsWith:
      return "beginsWith";
    case O.EndsWith:
      return "endsWith";
    case O.Matches:
      return "matches";
    case O.DoesntMatch:
      return "doesNotMatch";
    default:
      return "other";
  }
}

function codeFromOp(op) {
  const O = _Ci.nsMsgSearchOp;
  switch (String(op || "contains").toLowerCase()) {
    case "doesnotcontain":
    case "doesntcontain":
      return O.DoesntContain;
    case "is":
      return O.Is;
    case "isnot":
    case "isnt":
      return O.Isnt;
    case "beginswith":
      return O.BeginsWith;
    case "endswith":
      return O.EndsWith;
    case "matches":
      return O.Matches;
    case "doesntmatch":
    case "doesnotmatch":
      return O.DoesntMatch;
    case "contains":
    default:
      return O.Contains;
  }
}

function actionTypeFromCode(type) {
  const F = _Ci.nsMsgFilterAction;
  switch (type) {
    case F.MoveToFolder:
      return "moveToFolder";
    case F.CopyToFolder:
      return "copyToFolder";
    case F.MarkRead:
      return "markRead";
    case F.Delete:
      return "delete";
    case F.DeleteFromPop3Server:
      return "deleteFromServer";
    case F.AddTag:
      return "addTag";
    case F.JunkScore:
      return "junk";
    case F.StopExecution:
      return "stopExecution";
    default:
      return "other";
  }
}

function getFolderByURI(uri) {
  if (!uri) return null;
  try {
    if (MailServices.folderLookup?.getFolderForURL) {
      return MailServices.folderLookup.getFolderForURL(uri);
    }
  } catch {
    // ignore
  }
  for (const acc of MailServices.accounts.accounts) {
    const root = acc?.incomingServer?.rootMsgFolder;
    if (!root) continue;
    const found = findFolderByURIWalk(root, uri);
    if (found) return found;
  }
  return null;
}

function findFolderByURIWalk(folder, targetUri) {
  if (!folder) return null;
  if (folder.URI === targetUri) return folder;
  for (const child of folder.subFolders || []) {
    const hit = findFolderByURIWalk(child, targetUri);
    if (hit) return hit;
  }
  return null;
}

function folderRelativePath(account, folder) {
  if (!account?.incomingServer?.rootMsgFolder || !folder) return "";
  const rootUri = account.incomingServer.rootMsgFolder.URI.replace(/\/+$/, "");
  const uri = String(folder.URI || "");
  if (uri.startsWith(rootUri + "/")) {
    return decodeURIComponent(uri.slice(rootUri.length + 1));
  }
  // Walk ancestors fallback
  const parts = [];
  let cur = folder;
  const root = account.incomingServer.rootMsgFolder;
  while (cur && cur.URI !== root.URI) {
    parts.unshift(cur.name || cur.prettyName || "");
    cur = cur.parent;
  }
  return parts.filter(Boolean).join("/");
}

function resolveChildFolder(parent, name) {
  if (!parent || !name) return null;
  try {
    const exact = parent.getChildNamed(name);
    if (exact) return exact;
  } catch {
    // fallback case-insensitive
  }
  const needle = name.toLowerCase();
  for (const child of parent.subFolders || []) {
    if (
      String(child.name || "").toLowerCase() === needle ||
      String(child.prettyName || "").toLowerCase() === needle
    ) {
      return child;
    }
  }
  return null;
}

function findSpecialTrashFolder(root) {
  const F = _Ci.nsMsgFolderFlags;
  try {
    const f = root.getFolderWithFlags(F.Trash);
    if (f) return f;
  } catch {
    // ignore
  }
  for (const child of root.subFolders || []) {
    if (child.flags & F.Trash) return child;
  }
  return null;
}

function resolveOrCreateTargetFolder(account, folderPath, createIfMissing = false) {
  const root = account?.incomingServer?.rootMsgFolder;
  if (!root) throw new Error("Account root folder unavailable");
  const clean = String(folderPath || "").replace(/^\/+|\/+$/g, "");
  if (!clean) throw new Error("Target folder path is empty");

  const low = clean.toLowerCase();
  if (low === "trash" || low === "papierkorb" || low === "deleted" || low === "bin") {
    const trash = findSpecialTrashFolder(root);
    if (trash) return trash;
  }

  const segments = clean.split("/").filter(Boolean);
  let cur = root;
  for (const seg of segments) {
    let next = resolveChildFolder(cur, seg);
    if (!next && createIfMissing) {
      try {
        cur.createSubfolder(seg, null);
        next = resolveChildFolder(cur, seg);
      } catch (err) {
        throw new Error(`Cannot create folder "${seg}": ${err?.message || err}`);
      }
    }
    if (!next) {
      throw new Error(`Target folder "${clean}" not found in account`);
    }
    cur = next;
  }
  return cur;
}

function serializeFilter(account, filter, index) {
  const terms = [];
  for (const term of filter.searchTerms || []) {
    const val = term.value;
    terms.push({
      field: fieldFromAttrib(term.attrib, term),
      headerName: term.arbitraryHeader || "",
      op: opFromCode(term.op),
      value: val?.str != null ? String(val.str) : "",
      booleanAnd: Boolean(term.booleanAnd),
      matchAll: Boolean(term.matchAll),
      beginsGrouping: Boolean(term.beginsGrouping),
      endsGrouping: Boolean(term.endsGrouping),
    });
  }

  const actions = [];
  for (const act of filter.actionList || []) {
    const type = actionTypeFromCode(act.type);
    const entry = { type };
    if (type === "moveToFolder" || type === "copyToFolder") {
      entry.targetFolderUri = act.targetFolderUri || "";
      const folder = getFolderByURI(entry.targetFolderUri);
      entry.targetFolderExists = Boolean(folder);
      entry.targetFolderPath = folder
        ? folderRelativePath(account, folder)
        : decodeURIComponent(entry.targetFolderUri.replace(/^[^:]+:\/\/[^/]+\//, ""));
    } else if (type === "addTag") {
      entry.tag = act.strValue || "";
    }
    actions.push(entry);
  }

  const boolFlags = new Set(terms.map((t) => Boolean(t.booleanAnd)));
  const hasMixedGrouping =
    terms.some((t) => t.beginsGrouping || t.endsGrouping) || boolFlags.size > 1;
  const matchAll = terms.length > 0 && terms.every((t) => t.booleanAnd);

  return {
    accountId: account.key,
    accountName: account.incomingServer?.prettyName || account.key,
    index,
    name: filter.filterName || "",
    enabled: Boolean(filter.enabled),
    filterType: Number(filter.filterType || 0),
    matchAll,
    hasMixedGrouping,
    conditions: terms,
    actions,
  };
}

function findFilterIndex(list, name) {
  const needle = String(name || "").trim();
  for (let i = 0; i < list.filterCount; i++) {
    const f = list.getFilterAt(i);
    if (f && f.filterName === needle) return i;
  }
  const low = needle.toLowerCase();
  for (let i = 0; i < list.filterCount; i++) {
    const f = list.getFilterAt(i);
    if (f && String(f.filterName || "").toLowerCase() === low) return i;
  }
  return -1;
}

function appendConditionToFilter(filter, cond, booleanAnd) {
  const term = filter.createTerm();
  const attrib = attribFromCondition(cond);
  term.attrib = attrib;
  if (String(cond?.field || "").toLowerCase() === "header" && cond?.headerName) {
    term.arbitraryHeader = String(cond.headerName).trim();
  }
  term.op = codeFromOp(cond?.op || "contains");
  term.booleanAnd = Boolean(booleanAnd);
  const val = term.value;
  val.attrib = attrib;
  val.str = String(cond?.value || "");
  term.value = val;
  filter.appendTerm(term);
}

function conditionKey(c) {
  return [
    String(c.field || "").toLowerCase(),
    String(c.headerName || "").toLowerCase(),
    String(c.op || "contains").toLowerCase(),
    String(c.value || "").toLowerCase(),
  ].join("|");
}

function rebuildFilterTerms(filter, newTerms) {
  filter.searchTerms = [];
  for (const t of newTerms) {
    appendConditionToFilter(filter, t, t.booleanAnd);
  }
}

var omaFilters = class extends ExtensionCommon.ExtensionAPI {
  getAPI() {
    return {
      omaFilters: {
        listFilters: async (accountId) => {
          const accounts = [];
          const filters = [];
          for (const acc of MailServices.accounts.accounts) {
            if (accountId && acc.key !== accountId) continue;
            if (!acc?.incomingServer?.canHaveFilters) continue;
            const list = acc.incomingServer.getFilterList(null);
            accounts.push({
              accountId: acc.key,
              accountName: acc.incomingServer.prettyName || acc.key,
              count: list.filterCount,
            });
            for (let i = 0; i < list.filterCount; i++) {
              const f = list.getFilterAt(i);
              if (f) filters.push(serializeFilter(acc, f, i));
            }
          }
          return { supported: true, accounts, filters };
        },

        upsertFilter: async (spec) => {
          const acc = MailServices.accounts.getAccount(spec?.accountId);
          if (!acc?.incomingServer?.canHaveFilters) {
            throw new Error(`Account "${spec?.accountId || ""}" cannot have native filters`);
          }
          const list = acc.incomingServer.getFilterList(null);
          const mode = String(spec?.mode || "create");
          const existingName = spec?.existingName || spec?.name || "";
          let idx = existingName ? findFilterIndex(list, existingName) : -1;
          let filter = idx >= 0 ? list.getFilterAt(idx) : null;

          if (mode === "toggle") {
            if (!filter) throw new Error(`Filter "${existingName}" not found`);
            filter.enabled = spec.enabled !== undefined ? Boolean(spec.enabled) : !filter.enabled;
            list.saveToDefaultFile();
            return { ok: true, filter: serializeFilter(acc, filter, idx) };
          }

          if (mode === "fix_match_all") {
            if (!filter) throw new Error(`Filter "${existingName}" not found`);
            const serialized = serializeFilter(acc, filter, idx);
            const wantAnd = Boolean(spec.matchAll);
            const nextTerms = serialized.conditions.map((c) => ({ ...c, booleanAnd: wantAnd }));
            rebuildFilterTerms(filter, nextTerms);
            if (spec.enable !== false) filter.enabled = true;
            list.saveToDefaultFile();
            return { ok: true, filter: serializeFilter(acc, filter, idx) };
          }

          if (mode === "append_conditions") {
            if (!filter) throw new Error(`Filter "${existingName}" not found`);
            const serialized = serializeFilter(acc, filter, idx);
            const seen = new Set(serialized.conditions.map(conditionKey));
            const nextMatchAll = spec.matchAll !== undefined ? Boolean(spec.matchAll) : serialized.matchAll;
            const merged = serialized.conditions.map((c) => ({ ...c, booleanAnd: nextMatchAll }));
            for (const c of spec.conditions || []) {
              if (!c || !String(c.value || "").trim()) continue;
              const k = conditionKey(c);
              if (!seen.has(k)) {
                seen.add(k);
                merged.push({ ...c, booleanAnd: nextMatchAll });
              }
            }
            rebuildFilterTerms(filter, merged);
            if (spec.enable !== false) filter.enabled = true;
            list.saveToDefaultFile();
            return { ok: true, filter: serializeFilter(acc, filter, idx) };
          }

          // mode === "create" or "replace"
          const filterName = String(spec?.name || existingName || "OmaThunder Filter").trim();
          const isNew = !filter || mode === "create_unique";
          if (isNew) {
            let finalName = filterName;
            if (findFilterIndex(list, finalName) >= 0 && mode === "create") {
              // Update existing same-named filter only when explicitly replacing; otherwise uniquify.
              let n = 2;
              while (findFilterIndex(list, `${filterName} (${n})`) >= 0) n++;
              finalName = `${filterName} (${n})`;
            }
            filter = list.createFilter(finalName);
            filter.filterType = _Ci.nsMsgFilterType.InboxRule | _Ci.nsMsgFilterType.Manual;
            idx = 0;
            list.insertFilterAt(0, filter);
          } else {
            filter.searchTerms = [];
            filter.clearActionList();
          }

          filter.enabled = spec.enabled !== false;
          if (!filter.filterType) {
            filter.filterType = _Ci.nsMsgFilterType.InboxRule | _Ci.nsMsgFilterType.Manual;
          }
          const matchAll = Boolean(spec?.matchAll);
          const conds = Array.isArray(spec?.conditions) ? spec.conditions : [];
          if (conds.length === 0) {
            throw new Error("At least one filter condition is required");
          }
          for (const c of conds) {
            if (!c || !String(c.value || "").trim()) continue;
            appendConditionToFilter(filter, c, matchAll);
          }

          const actions = Array.isArray(spec?.actions) ? spec.actions : [];
          for (const a of actions) {
            if (!a || !a.type) continue;
            const act = filter.createAction();
            const t = String(a.type);
            if (t === "moveToFolder" || t === "copyToFolder") {
              act.type =
                t === "copyToFolder"
                  ? _Ci.nsMsgFilterAction.CopyToFolder
                  : _Ci.nsMsgFilterAction.MoveToFolder;
              let uri = a.targetFolderUri || "";
              if (!uri && a.targetFolderPath) {
                const folder = resolveOrCreateTargetFolder(acc, a.targetFolderPath, Boolean(spec.createFolder));
                uri = folder.URI;
              }
              if (!uri) throw new Error("Missing target folder for filter action");
              act.targetFolderUri = uri;
            } else if (t === "markRead") {
              act.type = _Ci.nsMsgFilterAction.MarkRead;
            } else if (t === "delete") {
              act.type = _Ci.nsMsgFilterAction.Delete;
            } else if (t === "stopExecution") {
              act.type = _Ci.nsMsgFilterAction.StopExecution;
            } else {
              continue;
            }
            filter.appendAction(act);
          }

          list.saveToDefaultFile();
          return { ok: true, filter: serializeFilter(acc, filter, idx) };
        },

        runFilters: async (accountId, filterNames) => {
          const acc = MailServices.accounts.getAccount(accountId);
          if (!acc?.incomingServer?.canHaveFilters) {
            throw new Error(`Account "${accountId}" cannot run filters`);
          }
          const root = acc.incomingServer.rootMsgFolder;
          const inbox = root?.getFolderWithFlags(_Ci.nsMsgFolderFlags.Inbox);
          if (!inbox) throw new Error("Inbox folder not found for account");
          const list = acc.incomingServer.getFilterList(null);
          const msgWindow = _Services.wm.getMostRecentWindow("mail:3pane")?.msgWindow || null;

          let runList = list;
          let count = 0;
          if (Array.isArray(filterNames) && filterNames.length > 0) {
            const want = new Set(filterNames.map((n) => String(n || "").toLowerCase()));
            const temp = MailServices.filters.getTempFilterList(inbox);
            for (let i = 0; i < list.filterCount; i++) {
              const f = list.getFilterAt(i);
              if (f && want.has(String(f.filterName || "").toLowerCase())) {
                temp.insertFilterAt(count++, f);
              }
            }
            runList = temp;
          } else {
            for (let i = 0; i < list.filterCount; i++) {
              if (list.getFilterAt(i)?.enabled) count++;
            }
          }
          if (count === 0) return { ok: true, executed: 0 };
          MailServices.filters.applyFiltersToFolders(runList, [inbox], msgWindow);
          return { ok: true, executed: count };
        },

        openFilterDialog: async (accountId) => {
          const win = _Services.wm.getMostRecentWindow("mail:3pane");
          if (!win || typeof win.MsgFilters !== "function") {
            return { opened: false };
          }
          let folder = null;
          if (accountId) {
            const acc = MailServices.accounts.getAccount(accountId);
            folder = acc?.incomingServer?.rootMsgFolder || null;
          }
          win.MsgFilters(null, folder);
          return { opened: true };
        },
      },
    };
  }
};
