/* global ChromeUtils, Components */
"use strict";

// All experiment scripts of an add-on are loaded into ONE shared sandbox global. Only the API class
// is exported (var omaCalendar); every helper lives inside the closure so nothing collides with
// api/filters/implementation.js.
var omaCalendar;

(() => {
  const { ExtensionCommon } = ChromeUtils.importESModule("resource://gre/modules/ExtensionCommon.sys.mjs");
  const { cal } = ChromeUtils.importESModule("resource:///modules/calendar/calUtils.sys.mjs");
  const { CalEvent } = ChromeUtils.importESModule("resource:///modules/CalEvent.sys.mjs");

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
  const DATE_ONLY = /^\d{4}-\d{2}-\d{2}$/;

  function pad2(n) {
    return String(n).padStart(2, "0");
  }

  const defaultTz = () => cal.dtz?.defaultTimezone || cal.timezoneService?.floating;
  const floatingTz = () => cal.dtz?.floating || cal.timezoneService?.floating || defaultTz();

  function serializeDateTime(dt) {
    if (!dt) return "";
    try {
      if (dt.isDate) {
        return `${dt.year}-${pad2(dt.month + 1)}-${pad2(dt.day)}`;
      }
      if (cal.dtz?.toRFC3339) {
        return cal.dtz.toRFC3339(dt) || dt.icalString || "";
      }
      return dt.icalString || "";
    } catch {
      return dt?.icalString || "";
    }
  }

  // Milliseconds since the epoch; floating times and all-day dates count as local time.
  function toMillis(dt) {
    if (!dt) return NaN;
    try {
      if (cal.dtz?.dateTimeToJsDate) return cal.dtz.dateTimeToJsDate(dt).getTime();
    } catch {
      // fall through
    }
    try {
      return Number(dt.nativeTime) / 1000;
    } catch {
      return NaN;
    }
  }

  function makeDate(year, month0, day) {
    const dt = cal.createDateTime();
    dt.resetTo(year, month0, day, 0, 0, 0, floatingTz());
    dt.isDate = true;
    return dt;
  }

  // All-day value: the YYYY-MM-DD prefix of the string.
  function parseDateOnly(value) {
    const str = String(value ?? "").trim();
    const m = str.match(/^(\d{4})-(\d{2})-(\d{2})/);
    if (!m) throw fail("invalid_argument", `invalid date: ${str}`);
    const month = Number(m[2]);
    const day = Number(m[3]);
    if (month < 1 || month > 12 || day < 1 || day > 31) throw fail("invalid_argument", `invalid date: ${str}`);
    return makeDate(Number(m[1]), month - 1, day);
  }

  // Timed value: RFC 3339 / ISO 8601; a bare date means local midnight.
  function parseTimed(value) {
    const str = String(value ?? "").trim();
    if (!str) throw fail("invalid_argument", "empty date-time");
    const tz = defaultTz();
    if (DATE_ONLY.test(str)) {
      const d = parseDateOnly(str);
      const dt = cal.createDateTime();
      dt.resetTo(d.year, d.month, d.day, 0, 0, 0, tz);
      return dt;
    }
    if (cal.dtz?.fromRFC3339) {
      try {
        const parsed = cal.dtz.fromRFC3339(str, tz);
        if (parsed) return parsed;
      } catch {
        // fall through to JS Date
      }
    }
    const jsDate = new Date(str);
    if (Number.isNaN(jsDate.getTime())) throw fail("invalid_argument", `invalid date-time: ${str}`);
    if (cal.dtz?.jsDateToDateTime) return cal.dtz.jsDateToDateTime(jsDate, tz);
    const dt = cal.createDateTime();
    dt.nativeTime = jsDate.getTime() * 1000;
    return dt;
  }

  const parseIn = (value, allDay) => (allDay ? parseDateOnly(value) : parseTimed(value));

  // dt + days/hours (both with the same sign).
  function plus(dt, { days = 0, hours = 0 } = {}) {
    const out = dt.clone();
    const dur = cal.createDuration();
    if (days) dur.days = Math.abs(days);
    if (hours) dur.hours = Math.abs(hours);
    if (days < 0 || hours < 0) dur.isNegative = true;
    out.addDuration(dur);
    return out;
  }

  // Default end: all-day events end (exclusively) the next day, timed events after one hour.
  const defaultEnd = (start, allDay) => (allDay ? plus(start, { days: 1 }) : plus(start, { hours: 1 }));

  // Converts a start/end value between all-day and timed without changing the covered days.
  function convertMode(dt, allDay, which) {
    if (!dt) return null;
    if (Boolean(dt.isDate) === allDay) return dt.clone();
    if (allDay) {
      const local = dt.timezone?.isFloating ? dt : dt.getInTimezone(defaultTz());
      const d = makeDate(local.year, local.month, local.day);
      // An end inside a day still covers that day; all-day ends are exclusive.
      if (which === "end" && (local.hour || local.minute || local.second)) return plus(d, { days: 1 });
      return d;
    }
    const t = cal.createDateTime();
    t.resetTo(dt.year, dt.month, dt.day, 0, 0, 0, defaultTz());
    return t;
  }

  const isSet = (v) => v !== undefined && v !== null && String(v).trim() !== "";

  // New start/end for an item according to spec.{start,end,allDay}; null when the times stay.
  // - only start given: the duration is kept
  // - allDay switched without new times: both ends are converted consistently
  // - end must be after start (all-day end is exclusive)
  function computeTimes(item, s) {
    const oldStart = item.startDate;
    const oldEnd = item.endDate || oldStart;
    const wasAllDay = Boolean(oldStart?.isDate);
    const allDay = s.allDay !== undefined && s.allDay !== null ? Boolean(s.allDay) : wasAllDay;
    const hasStart = isSet(s.start);
    const hasEnd = isSet(s.end);
    if (!hasStart && !hasEnd && allDay === wasAllDay) return null;

    const start = hasStart ? parseIn(s.start, allDay) : convertMode(oldStart, allDay, "start");
    let end;
    if (hasEnd) {
      end = parseIn(s.end, allDay);
    } else if (hasStart && allDay === wasAllDay && oldStart && oldEnd) {
      end = start.clone();
      end.addDuration(oldEnd.subtractDate(oldStart));
    } else {
      end = convertMode(oldEnd, allDay, "end");
    }
    if (!end || end.compare(start) <= 0) {
      if (hasEnd) throw fail("invalid_argument", "end must be after start (all-day end is exclusive)");
      end = defaultEnd(start, allDay);
    }
    return { start, end };
  }

  function serializeCalendar(c) {
    let color = "";
    try {
      color = String(c.getProperty("color") || "");
    } catch {
      // ignore
    }
    return {
      id: String(c.id || ""),
      name: String(c.name || ""),
      type: String(c.type || ""),
      readOnly: Boolean(c.readOnly),
      disabled: Boolean(c.getProperty?.("disabled")),
      color,
      uri: String(c.uri?.spec || ""),
    };
  }

  function serializeEvent(item) {
    if (!item) return null;
    let location = "";
    let url = "";
    try {
      location = String(item.getProperty("LOCATION") || "");
    } catch {
      // ignore
    }
    try {
      url = String(item.getProperty("URL") || "");
    } catch {
      // ignore
    }
    let categories = [];
    try {
      categories = item.getCategories?.() || [];
    } catch {
      // ignore
    }
    let attendees = [];
    try {
      attendees = (item.getAttendees?.() || []).map((a) => ({
        id: String(a.id || "").replace(/^mailto:/i, ""),
        name: String(a.commonName || ""),
        role: String(a.role || ""),
        status: String(a.participationStatus || ""),
      }));
    } catch {
      // ignore
    }
    const startDt = item.startDate;
    const endDt = item.endDate;
    let isRecurring = false;
    try {
      isRecurring = Boolean(item.recurrenceId || item.recurrenceInfo || (item.parentItem && item.parentItem !== item));
    } catch {
      // ignore
    }
    return {
      id: String(item.id || ""),
      recurrenceId: item.recurrenceId ? serializeDateTime(item.recurrenceId) : "",
      isRecurring,
      calendarId: String(item.calendar?.id || ""),
      calendarName: String(item.calendar?.name || ""),
      title: String(item.title || ""),
      start: serializeDateTime(startDt),
      end: serializeDateTime(endDt),
      allDay: Boolean(startDt?.isDate),
      location,
      url,
      description: String(item.descriptionText || ""),
      status: String(item.status || ""),
      categories: Array.isArray(categories) ? categories.map(String) : [],
      attendees,
      readOnly: Boolean(item.calendar?.readOnly),
    };
  }

  function findCalendarById(calendarId) {
    const cals = cal.manager?.getCalendars?.() || [];
    const target = String(calendarId || "").trim();
    if (!target) return null;
    return cals.find((c) => String(c.id) === target) || null;
  }

  function writableCalendar(calendarId) {
    const c = findCalendarById(calendarId);
    if (!c) throw fail("not_found", `calendar ${calendarId || "(empty)"}`);
    if (c.readOnly) throw fail("read_only", `calendar "${c.name}" is read-only`);
    return c;
  }

  async function fetchCalendarItems(calendar, filter, rangeStart, rangeEnd) {
    if (typeof calendar.getItemsAsArray === "function") {
      return calendar.getItemsAsArray(filter, 0, rangeStart, rangeEnd);
    }
    const stream = calendar.getItems(filter, 0, rangeStart, rangeEnd);
    const out = [];
    if (cal.iterate?.streamValues) {
      for await (const items of cal.iterate.streamValues(stream)) {
        for (const item of items) out.push(item);
      }
    }
    return out;
  }

  // Returns the stored item (for a recurring event: the series master).
  async function findEventById(calendar, id) {
    const items = await calendar.getItem(String(id));
    if (Array.isArray(items)) return items[0] || null;
    return items || null;
  }

  // scope: "occurrence" (default when recurrenceId is given) or "series".
  function resolveScope(s, parent) {
    const ridStr = String(s.recurrenceId ?? "").trim();
    const scope = String(s.scope || "").trim() || (ridStr ? "occurrence" : "series");
    if (scope !== "occurrence" && scope !== "series") throw fail("invalid_argument", `unknown scope ${scope}`);
    if (scope === "occurrence") {
      if (!ridStr) throw fail("invalid_argument", "recurrenceId is required for scope occurrence");
      if (!parent.recurrenceInfo) throw fail("invalid_argument", "event is not recurring");
    }
    return { scope, ridStr };
  }

  // Finds the recurrence ID of an existing occurrence (or exception) of `parent`.
  function matchRecurrenceId(parent, ridStr) {
    const info = parent.recurrenceInfo;
    if (!info) throw fail("invalid_argument", "event is not recurring");
    const asDate = DATE_ONLY.test(ridStr) || Boolean(parent.startDate?.isDate);
    const rid = asDate ? parseDateOnly(ridStr) : parseTimed(ridStr);
    const exception = info.getExceptionFor?.(rid);
    if (exception) return exception.recurrenceId || rid;
    const occurrences = info.getOccurrences(plus(rid, { days: -2 }), plus(rid, { days: 2 }), 0) || [];
    const hit = occurrences.find((o) => o?.recurrenceId && o.recurrenceId.compare(rid) === 0);
    if (!hit) throw fail("not_found", `occurrence ${ridStr}`);
    return hit.recurrenceId;
  }

  function applyEventPatch(item, s) {
    if (s.title !== undefined) {
      const title = String(s.title ?? "").trim();
      if (!title) throw fail("invalid_argument", "event title must not be empty");
      item.title = title;
    }
    const times = computeTimes(item, s);
    if (times) {
      item.startDate = times.start;
      item.endDate = times.end;
    }
    if (s.location !== undefined) {
      if (s.location) item.setProperty("LOCATION", String(s.location));
      else item.deleteProperty("LOCATION");
    }
    if (s.description !== undefined) item.descriptionText = String(s.description ?? "");
    if (Array.isArray(s.categories) && typeof item.setCategories === "function") {
      item.setCategories(s.categories.map(String));
    }
  }

  const specOf = (spec) => (spec && typeof spec === "object" ? spec : {});

  async function getEvents(query = {}) {
    const q = specOf(query);
    const cals = cal.manager?.getCalendars?.() || [];
    const wantIds = Array.isArray(q.calendarIds) && q.calendarIds.length > 0 ? new Set(q.calendarIds.map(String)) : null;
    const now = Date.now();
    const rangeStart = parseTimed(isSet(q.start) ? q.start : new Date(now - 24 * 60 * 60 * 1000).toISOString());
    const rangeEnd = parseTimed(isSet(q.end) ? q.end : new Date(now + 14 * 24 * 60 * 60 * 1000).toISOString());
    if (rangeEnd.compare(rangeStart) <= 0) throw fail("invalid_argument", "end must be after start");
    const limit = Math.max(1, Math.min(Number(q.limit) || 250, 1000));
    const filter = _Ci.calICalendar.ITEM_FILTER_TYPE_EVENT | _Ci.calICalendar.ITEM_FILTER_CLASS_OCCURRENCES;

    const rows = [];
    for (const c of cals) {
      if (c.getProperty?.("disabled")) continue;
      if (wantIds && !wantIds.has(String(c.id))) continue;
      try {
        const items = await fetchCalendarItems(c, filter, rangeStart, rangeEnd);
        for (const item of items || []) {
          const ev = serializeEvent(item);
          if (!ev) continue;
          const key = toMillis(item.startDate);
          rows.push({ key: Number.isFinite(key) ? key : Number.MAX_SAFE_INTEGER, ev });
        }
      } catch (err) {
        console.warn("OmaThunder calendar getEvents warning:", c.id, err);
      }
    }
    // Sort by the real instant: RFC 3339 strings with different offsets do not sort correctly as text.
    rows.sort((a, b) => a.key - b.key || a.ev.title.localeCompare(b.ev.title));
    return { supported: true, events: rows.slice(0, limit).map((r) => r.ev), total: rows.length };
  }

  async function createEvent(spec) {
    const s = specOf(spec);
    let targetCal;
    if (isSet(s.calendarId)) {
      targetCal = writableCalendar(s.calendarId);
    } else {
      const cals = cal.manager?.getCalendars?.() || [];
      targetCal = cals.find((c) => !c.readOnly && !c.getProperty?.("disabled"));
      if (!targetCal) throw fail("not_found", "no writable calendar");
    }
    const title = String(s.title || "").trim();
    if (!title) throw fail("invalid_argument", "event title is required");
    if (!isSet(s.start)) throw fail("invalid_argument", "event start is required");

    const allDay = s.allDay === true || (s.allDay === undefined && DATE_ONLY.test(String(s.start).trim()));
    const startDt = parseIn(s.start, allDay);
    const endDt = isSet(s.end) ? parseIn(s.end, allDay) : defaultEnd(startDt, allDay);
    if (endDt.compare(startDt) <= 0) throw fail("invalid_argument", "end must be after start (all-day end is exclusive)");

    const ev = new CalEvent();
    ev.id = cal.getUUID();
    ev.title = title;
    ev.startDate = startDt;
    ev.endDate = endDt;
    if (s.location) ev.setProperty("LOCATION", String(s.location));
    if (s.description) ev.descriptionText = String(s.description);
    if (Array.isArray(s.categories) && s.categories.length > 0 && typeof ev.setCategories === "function") {
      ev.setCategories(s.categories.map(String));
    }
    const added = await targetCal.addItem(ev);
    return { ok: true, event: serializeEvent(added || ev) };
  }

  async function updateEvent(spec) {
    const s = specOf(spec);
    const targetCal = writableCalendar(s.calendarId);
    if (!isSet(s.id)) throw fail("invalid_argument", "event id is required");
    const parent = await findEventById(targetCal, s.id);
    if (!parent) throw fail("not_found", `event ${s.id}`);
    const { scope, ridStr } = resolveScope(s, parent);

    if (scope === "series") {
      const next = parent.clone();
      applyEventPatch(next, s);
      const modified = await targetCal.modifyItem(next, parent);
      return { ok: true, scope, event: serializeEvent(modified || next) };
    }

    // One occurrence: store it as an exception of a cloned series master, leave the rest untouched.
    const rid = matchRecurrenceId(parent, ridStr);
    const newParent = parent.clone();
    const proxy = newParent.recurrenceInfo.getOccurrenceFor(rid);
    if (!proxy) throw fail("not_found", `occurrence ${ridStr}`);
    const occurrence = proxy.clone();
    applyEventPatch(occurrence, s);
    newParent.recurrenceInfo.modifyException(occurrence, true);
    const modified = await targetCal.modifyItem(newParent, parent);
    const stored = (modified || newParent).recurrenceInfo?.getOccurrenceFor?.(rid) || occurrence;
    return { ok: true, scope, event: serializeEvent(stored) };
  }

  async function deleteEvent(spec) {
    const s = specOf(spec);
    const targetCal = writableCalendar(s.calendarId);
    if (!isSet(s.id)) throw fail("invalid_argument", "event id is required");
    const parent = await findEventById(targetCal, s.id);
    if (!parent) throw fail("not_found", `event ${s.id}`);
    const { scope, ridStr } = resolveScope(s, parent);

    if (scope === "series") {
      await targetCal.deleteItem(parent);
      return { ok: true, scope, id: String(s.id), calendarId: String(targetCal.id) };
    }
    // One occurrence: add an EXDATE (and drop a possible exception) on a cloned series master.
    const rid = matchRecurrenceId(parent, ridStr);
    const newParent = parent.clone();
    newParent.recurrenceInfo.removeOccurrenceAt(rid);
    await targetCal.modifyItem(newParent, parent);
    return {
      ok: true,
      scope,
      id: String(s.id),
      recurrenceId: serializeDateTime(rid),
      calendarId: String(targetCal.id),
    };
  }

  omaCalendar = class extends ExtensionCommon.ExtensionAPI {
    getAPI(context) {
      return {
        omaCalendar: {
          listCalendars: guard(async () => {
            const cals = cal.manager?.getCalendars?.() || [];
            return { supported: true, calendars: cals.map(serializeCalendar) };
          }),
          getEvents: guard(getEvents),
          createEvent: guard(createEvent),
          updateEvent: guard(updateEvent),
          deleteEvent: guard(deleteEvent),

          // Lightweight change notifications: only kind, calendar and event id. No titles, descriptions
          // or attendees, and no event for plain calendar refreshes (onLoad).
          onChanged: new ExtensionCommon.EventManager({
            context,
            name: "omaCalendar.onChanged",
            register: (fire) => {
              const notify = (kind, item) => {
                const calendarId = String(item?.calendar?.id || "");
                if (!calendarId) return;
                fire.async({ kind, calendarId, id: String(item?.id || "") });
              };
              const observer = {
                QueryInterface: ChromeUtils.generateQI(["calIObserver"]),
                onStartBatch() {},
                onEndBatch() {},
                onLoad() {},
                onAddItem(item) {
                  notify("add", item);
                },
                onModifyItem(newItem) {
                  notify("modify", newItem);
                },
                onDeleteItem(deletedItem) {
                  notify("delete", deletedItem);
                },
                onError() {},
                onPropertyChanged() {},
                onPropertyDeleted() {},
              };
              cal.manager?.addCalendarObserver?.(observer);
              return () => {
                cal.manager?.removeCalendarObserver?.(observer);
              };
            },
          }).api(),
        },
      };
    }
  };
})();
