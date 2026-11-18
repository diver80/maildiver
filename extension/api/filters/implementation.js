/* global ChromeUtils, Components */
"use strict";

// All experiment scripts of an add-on are loaded into ONE shared sandbox global. Only the API class
// is exported (var omaFilters); every helper lives inside the closure so nothing collides with
// api/calendar/implementation.js.
var omaFilters;

(() => {
  const { ExtensionCommon } = ChromeUtils.importESModule("resource://gre/modules/ExtensionCommon.sys.mjs");
  const { MailServices } = ChromeUtils.importESModule("resource:///modules/MailServices.sys.mjs");

  // Only ExtensionError messages reach the extension; any other error becomes
  // "An unexpected error occurred". Messages start with a CONTRACT §2 code ("not_found: …").
  let ExtensionError = Error;
  try {
    ExtensionError =
      ChromeUtils.importESModule("resource://gre/modules/ExtensionUtils.sys.mjs").ExtensionUtils?.ExtensionError || Error;
  } catch {
    // keep Error
  }
  const fail = (code, detail = "") => new ExtensionError(detail ? `${code}: ${detail}` : code);
  const guard = (fn) => async (...args) => {
    try {
      return await fn(...args);
    } catch (err) {
      if (err instanceof ExtensionError) throw err;
      throw new ExtensionError(String(err?.message || err || "internal_error"));
    }
  };

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

  const KNOWN_FIELDS = new Set(["from", "sender", "to", "cc", "toorcc", "to_or_cc", "alladdresses", "all_addresses", "subject", "body", "header"]);
  const KNOWN_OPS = new Set([
    "contains", "doesnotcontain", "doesntcontain", "is", "isnot", "isnt", "beginswith", "endswith", "matches", "doesntmatch", "doesnotmatch",
  ]);

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

  const cleanFolderPath = (p) => String(p || "").replace(/^\/+|\/+$/g, "");

  // Finds a folder by path strictly inside one account (no creation). Returns null when missing.
  function findTargetFolder(account, folderPath) {
    const root = account?.incomingServer?.rootMsgFolder;
    if (!root) throw fail("not_found", "account root folder unavailable");
    const clean = cleanFolderPath(folderPath);
    if (!clean) throw fail("invalid_argument", "target folder path is empty");
    const low = clean.toLowerCase();
    if (low === "trash" || low === "papierkorb" || low === "deleted" || low === "bin") {
      const trash = findSpecialTrashFolder(root);
      if (trash) return trash;
    }
    let cur = root;
    for (const seg of clean.split("/").filter(Boolean)) {
      cur = resolveChildFolder(cur, seg);
      if (!cur) return null;
    }
    return cur;
  }

  // Creates the missing segments of a path inside one account.
  function createTargetFolder(account, folderPath) {
    const root = account?.incomingServer?.rootMsgFolder;
    if (!root) throw fail("not_found", "account root folder unavailable");
    const clean = cleanFolderPath(folderPath);
    let cur = root;
    for (const seg of clean.split("/").filter(Boolean)) {
      let next = resolveChildFolder(cur, seg);
      if (!next) {
        try {
          cur.createSubfolder(seg, null);
        } catch (err) {
          throw fail("conflict", `cannot create folder "${seg}": ${err?.message || err}`);
        }
        next = resolveChildFolder(cur, seg);
      }
      if (!next) throw fail("not_found", `target folder "${clean}" (creation pending, try again)`);
      cur = next;
    }
    return cur;
  }

  function folderBelongsToAccount(account, folder, uri) {
    const server = account?.incomingServer;
    if (!server) return false;
    if (folder) {
      try {
        return folder.server?.key === server.key;
      } catch {
        return false;
      }
    }
    const rootUri = String(server.rootMsgFolder?.URI || "").replace(/\/+$/, "");
    return Boolean(rootUri) && String(uri || "").startsWith(`${rootUri}/`);
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

  function findFilterIndex(list, name, skipIndex = -1) {
    const needle = String(name || "").trim();
    for (let i = 0; i < list.filterCount; i++) {
      if (i === skipIndex) continue;
      const f = list.getFilterAt(i);
      if (f && f.filterName === needle) return i;
    }
    const low = needle.toLowerCase();
    for (let i = 0; i < list.filterCount; i++) {
      if (i === skipIndex) continue;
      const f = list.getFilterAt(i);
      if (f && String(f.filterName || "").toLowerCase() === low) return i;
    }
    return -1;
  }

  // "Name", or "Name (2)", "Name (3)" … when the name is taken (case-insensitive).
  function uniqueFilterName(list, baseName, skipIndex = -1) {
    if (findFilterIndex(list, baseName, skipIndex) < 0) return baseName;
    let n = 2;
    while (findFilterIndex(list, `${baseName} (${n})`, skipIndex) >= 0) n++;
    return `${baseName} (${n})`;
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

  // Non-empty, well-formed conditions; throws invalid_argument for malformed ones.
  function validConditions(conditions) {
    const out = [];
    for (const c of Array.isArray(conditions) ? conditions : []) {
      if (!c || typeof c !== "object") throw fail("invalid_argument", "invalid filter condition");
      if (!String(c.value ?? "").trim()) continue;
      const field = String(c.field || "subject").toLowerCase();
      if (!KNOWN_FIELDS.has(field)) throw fail("invalid_argument", `unsupported condition field ${c.field}`);
      if (field === "header" && !String(c.headerName || "").trim()) throw fail("invalid_argument", "header condition needs headerName");
      if (c.op !== undefined && c.op !== null && c.op !== "" && !KNOWN_OPS.has(String(c.op).toLowerCase())) {
        throw fail("invalid_argument", `unsupported condition op ${c.op}`);
      }
      out.push(c);
    }
    return out;
  }

  // Validates all actions without changing anything. Target folders must stay in the filter's account
  // (unless spec.sameAccountOnly === false); missing folders are only allowed with spec.createFolder.
  function planActions(acc, spec) {
    const plan = [];
    for (const a of Array.isArray(spec.actions) ? spec.actions : []) {
      if (!a || typeof a !== "object") throw fail("invalid_argument", "invalid filter action");
      const type = String(a.type || "");
      switch (type) {
        case "moveToFolder":
        case "copyToFolder": {
          const uri = String(a.targetFolderUri || "").trim();
          if (uri) {
            const folder = getFolderByURI(uri);
            if (spec.sameAccountOnly !== false && !folderBelongsToAccount(acc, folder, uri)) {
              throw fail("cross_account_move", "filter target folder belongs to another account");
            }
            plan.push({ type, uri, folderPath: "" });
            break;
          }
          const folderPath = cleanFolderPath(a.targetFolderPath);
          if (!folderPath) throw fail("invalid_argument", "missing target folder for filter action");
          const existing = findTargetFolder(acc, folderPath);
          if (!existing && !spec.createFolder) throw fail("not_found", `target folder "${folderPath}"`);
          plan.push({ type, uri: existing ? existing.URI : "", folderPath });
          break;
        }
        case "markRead":
        case "stopExecution":
          plan.push({ type });
          break;
        case "delete":
          if (spec.allowDelete === false) throw fail("invalid_argument", "delete actions are not allowed here");
          plan.push({ type });
          break;
        default:
          throw fail("invalid_argument", `unsupported filter action ${type || "(empty)"}`);
      }
    }
    return plan;
  }

  function appendPlannedAction(filter, step) {
    const act = filter.createAction();
    const F = _Ci.nsMsgFilterAction;
    switch (step.type) {
      case "moveToFolder":
        act.type = F.MoveToFolder;
        act.targetFolderUri = step.uri;
        break;
      case "copyToFolder":
        act.type = F.CopyToFolder;
        act.targetFolderUri = step.uri;
        break;
      case "markRead":
        act.type = F.MarkRead;
        break;
      case "delete":
        act.type = F.Delete;
        break;
      case "stopExecution":
        act.type = F.StopExecution;
        break;
      default:
        return;
    }
    filter.appendAction(act);
  }

  function filterAccount(accountId) {
    const acc = MailServices.accounts.getAccount(String(accountId || ""));
    if (!acc) throw fail("not_found", `account ${accountId || "(empty)"}`);
    if (!acc.incomingServer?.canHaveFilters) throw fail("invalid_argument", `account ${accountId} cannot have native filters`);
    return acc;
  }

  // mode "create":  always a NEW filter, uniquified name, appended at the END (user's own rules run first).
  // mode "replace": rebuilds the filter named existingName in place (must exist).
  // Everything is validated before the live filter list is touched, so a failure never leaves a
  // half-built filter behind.
  async function upsertFilter(spec) {
    const s = spec && typeof spec === "object" ? spec : {};
    const acc = filterAccount(s.accountId);
    const list = acc.incomingServer.getFilterList(null);
    const mode = String(s.mode || "create");
    const existingName = String(s.existingName || "").trim();

    if (mode === "toggle" || mode === "fix_match_all" || mode === "append_conditions") {
      const name = existingName || String(s.name || "").trim();
      const idx = name ? findFilterIndex(list, name) : -1;
      if (idx < 0) throw fail("not_found", `filter "${name}"`);
      const filter = list.getFilterAt(idx);

      if (mode === "toggle") {
        filter.enabled = s.enabled !== undefined ? Boolean(s.enabled) : !filter.enabled;
      } else if (mode === "fix_match_all") {
        const serialized = serializeFilter(acc, filter, idx);
        const wantAnd = Boolean(s.matchAll);
        rebuildFilterTerms(filter, serialized.conditions.map((c) => ({ ...c, booleanAnd: wantAnd })));
        if (s.enable !== false) filter.enabled = true;
      } else {
        const additions = validConditions(s.conditions);
        const serialized = serializeFilter(acc, filter, idx);
        const seen = new Set(serialized.conditions.map(conditionKey));
        const nextMatchAll = s.matchAll !== undefined ? Boolean(s.matchAll) : serialized.matchAll;
        const merged = serialized.conditions.map((c) => ({ ...c, booleanAnd: nextMatchAll }));
        for (const c of additions) {
          const k = conditionKey(c);
          if (!seen.has(k)) {
            seen.add(k);
            merged.push({ ...c, booleanAnd: nextMatchAll });
          }
        }
        if (merged.length === 0) throw fail("invalid_argument", "at least one non-empty condition is required");
        rebuildFilterTerms(filter, merged);
        if (s.enable !== false) filter.enabled = true;
      }
      list.saveToDefaultFile();
      return { ok: true, filter: serializeFilter(acc, filter, idx) };
    }

    if (mode !== "create" && mode !== "replace") throw fail("invalid_argument", `unknown filter mode ${mode}`);

    // 1. Validate everything first.
    const conds = validConditions(s.conditions);
    if (conds.length === 0) throw fail("invalid_argument", "at least one non-empty filter condition is required");
    const plan = planActions(acc, s);
    if (plan.length === 0) throw fail("invalid_argument", "at least one filter action is required");

    let replaceIdx = -1;
    let oldFilter = null;
    if (mode === "replace") {
      const name = existingName || String(s.name || "").trim();
      replaceIdx = name ? findFilterIndex(list, name) : -1;
      if (replaceIdx < 0) throw fail("not_found", `filter "${name}"`);
      oldFilter = list.getFilterAt(replaceIdx);
    }
    const wantedName = String(s.name || "").trim();
    const finalName =
      mode === "create"
        ? uniqueFilterName(list, wantedName || existingName || "OmaThunder Filter")
        : wantedName
          ? uniqueFilterName(list, wantedName, replaceIdx)
          : String(oldFilter.filterName || existingName);

    // 2. Build the new filter detached from the list.
    const filter = list.createFilter(finalName);
    filter.filterType =
      (oldFilter && oldFilter.filterType) || _Ci.nsMsgFilterType.InboxRule | _Ci.nsMsgFilterType.Manual;
    filter.enabled = s.enabled !== false;
    const matchAll = Boolean(s.matchAll);
    for (const c of conds) appendConditionToFilter(filter, c, matchAll);

    // 3. Create missing target folders (only now that the spec is known to be valid).
    for (const step of plan) {
      if ((step.type === "moveToFolder" || step.type === "copyToFolder") && !step.uri) {
        step.uri = createTargetFolder(acc, step.folderPath).URI;
      }
      appendPlannedAction(filter, step);
    }

    // 4. Swap into the live list and persist.
    let idx;
    if (mode === "replace") {
      list.removeFilterAt(replaceIdx);
      list.insertFilterAt(replaceIdx, filter);
      idx = replaceIdx;
    } else {
      idx = list.filterCount;
      list.insertFilterAt(idx, filter);
    }
    list.saveToDefaultFile();
    return { ok: true, created: mode === "create", filter: serializeFilter(acc, filter, idx) };
  }

  async function runFilters(accountId, filterNames) {
    const acc = filterAccount(accountId);
    const root = acc.incomingServer.rootMsgFolder;
    const inbox = root?.getFolderWithFlags(_Ci.nsMsgFolderFlags.Inbox);
    if (!inbox) throw fail("not_found", "inbox folder of account");
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
  }

  omaFilters = class extends ExtensionCommon.ExtensionAPI {
    getAPI() {
      return {
        omaFilters: {
          listFilters: guard(async (accountId) => {
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
          }),

          upsertFilter: guard(upsertFilter),

          runFilters: guard(runFilters),

          openFilterDialog: guard(async (accountId) => {
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
          }),
        },
      };
    }
  };
})();
