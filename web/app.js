/* ==========================================================================
   MiniMax Design 2API — admin console · shell
   --------------------------------------------------------------------------
   Zero-build SPA. Plain ES2020, no framework, no bundler: what the Go binary
   embeds is what the browser runs.

   Visual vocabulary mirrors chenyme/grok2api's React console — sidebar plus a
   centred 1280px column, card surfaces, pill buttons, h-8 controls, quiet
   12px type. Page content is this project's own (see pages.js).

   Load order: this file defines the shell, pages.js defines the pages, and
   boot happens on DOMContentLoaded so both are guaranteed present.
   ========================================================================== */

"use strict";

/* --- icons -------------------------------------------------------------- */

const ICONS = {
  dashboard: '<rect x="3" y="3" width="7" height="9" rx="1"/><rect x="14" y="3" width="7" height="5" rx="1"/><rect x="14" y="12" width="7" height="9" rx="1"/><rect x="3" y="16" width="7" height="5" rx="1"/>',
  box: '<path d="M21 8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16Z"/><path d="m3.3 7 8.7 5 8.7-5"/><path d="M12 22V12"/>',
  users: '<path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M22 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/>',
  sparkles: '<path d="M9.937 15.5A2 2 0 0 0 8.5 14.063l-6.135-1.582a.5.5 0 0 1 0-.962L8.5 9.936A2 2 0 0 0 9.937 8.5l1.582-6.135a.5.5 0 0 1 .963 0L14.063 8.5A2 2 0 0 0 15.5 9.937l6.135 1.581a.5.5 0 0 1 0 .964L15.5 14.063a2 2 0 0 0-1.437 1.437l-1.582 6.135a.5.5 0 0 1-.963 0z"/><path d="M20 3v4"/><path d="M22 5h-4"/><path d="M4 17v2"/><path d="M5 18H3"/>',
  message: '<path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/><path d="M13 8H7"/><path d="M17 12H7"/>',
  image: '<rect x="3" y="3" width="18" height="18" rx="2" ry="2"/><circle cx="9" cy="9" r="2"/><path d="m21 15-3.086-3.086a2 2 0 0 0-2.828 0L6 21"/>',
  video: '<path d="m16 13 5.223 3.482a.5.5 0 0 0 .777-.416V7.87a.5.5 0 0 0-.752-.432L16 10.5"/><rect x="2" y="6" width="14" height="12" rx="2"/>',
  audio: '<path d="M2 10v3"/><path d="M6 6v11"/><path d="M10 3v18"/><path d="M14 8v7"/><path d="M18 5v13"/><path d="M22 10v3"/>',
  settings: '<path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z"/><circle cx="12" cy="12" r="3"/>',
  info: '<circle cx="12" cy="12" r="10"/><path d="M12 16v-4"/><path d="M12 8h.01"/>',
  refresh: '<path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/><path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16"/><path d="M8 16H3v5"/>',
  plus: '<path d="M5 12h14"/><path d="M12 5v14"/>',
  trash: '<path d="M3 6h18"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6"/><path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/><path d="M10 11v6"/><path d="M14 11v6"/>',
  play: '<polygon points="6 3 20 12 6 21 6 3" fill="currentColor" stroke="none"/>',
  copy: '<rect x="8" y="8" width="14" height="14" rx="2" ry="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/>',
  check: '<path d="M20 6 9 17l-5-5"/>',
  search: '<circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>',
  sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2"/><path d="M12 20v2"/><path d="m4.93 4.93 1.41 1.41"/><path d="m17.66 17.66 1.41 1.41"/><path d="M2 12h2"/><path d="M20 12h2"/><path d="m6.34 17.66-1.41 1.41"/><path d="m19.07 4.93-1.41 1.41"/>',
  moon: '<path d="M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9Z"/>',
  monitor: '<rect x="2" y="3" width="20" height="14" rx="2"/><path d="M8 21h8"/><path d="M12 17v4"/>',
  chevron: '<path d="m6 9 6 6 6-6"/>',
  loader: '<path d="M21 12a9 9 0 1 1-6.219-8.56"/>',
  inbox: '<path d="M22 12h-6l-2 3h-4l-2-3H2"/><path d="M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z"/>',
  alert: '<circle cx="12" cy="12" r="10"/><path d="M12 8v4"/><path d="M12 16h.01"/>',
  ok: '<circle cx="12" cy="12" r="10"/><path d="m9 12 2 2 4-4"/>',
  fail: '<circle cx="12" cy="12" r="10"/><path d="m15 9-6 6"/><path d="m9 9 6 6"/>',
  download: '<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><path d="m7 10 5 5 5-5"/><path d="M12 15V3"/>',
  activity: '<path d="M22 12h-2.48a2 2 0 0 0-1.93 1.46l-2.35 8.36a.25.25 0 0 1-.48 0L9.24 2.18a.25.25 0 0 0-.48 0l-2.35 8.36A2 2 0 0 1 4.49 12H2"/>',
  gauge: '<path d="m12 14 4-4"/><path d="M3.34 19a10 10 0 1 1 17.32 0"/>',
  x: '<path d="M18 6 6 18"/><path d="m6 6 12 12"/>',
  layers: '<path d="m12.83 2.18a2 2 0 0 0-1.66 0L2.6 6.08a1 1 0 0 0 0 1.83l8.58 3.91a2 2 0 0 0 1.66 0l8.58-3.9a1 1 0 0 0 0-1.83Z"/><path d="m6.08 9.5-3.5 1.6a1 1 0 0 0 0 1.81l8.6 3.91a2 2 0 0 0 1.65 0l8.58-3.9a1 1 0 0 0 0-1.83l-3.5-1.59"/><path d="m6.08 14.5-3.5 1.6a1 1 0 0 0 0 1.81l8.6 3.91a2 2 0 0 0 1.65 0l8.58-3.9a1 1 0 0 0 0-1.83l-3.5-1.59"/>',
  clockOff: '<circle cx="12" cy="12" r="10"/><path d="M12 6v6l2 2"/><path d="m4.9 4.9 14.2 14.2"/>',
  more: '<circle cx="12" cy="12" r="1" fill="currentColor"/><circle cx="19" cy="12" r="1" fill="currentColor"/><circle cx="5" cy="12" r="1" fill="currentColor"/>',
  ban: '<circle cx="12" cy="12" r="10"/><path d="m4.9 4.9 14.2 14.2"/>',
  edit: '<path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.12 2.12 0 0 1 3 3L12 15l-4 1 1-4Z"/>',
  shield: '<path d="M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z"/><path d="m9 12 2 2 4-4"/>',
  zap: '<path d="M4 14a1 1 0 0 1-.78-1.63l9.9-10.2a.5.5 0 0 1 .86.46l-1.92 6.02A1 1 0 0 0 13 10h7a1 1 0 0 1 .78 1.63l-9.9 10.2a.5.5 0 0 1-.86-.46l1.92-6.02A1 1 0 0 0 11 14z"/>',
};

function svg(inner, cls) {
  return '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" ' +
    'stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"' +
    (cls ? ' class="' + cls + '"' : "") + ">" + inner + "</svg>";
}
function icon(name, cls) { return svg(ICONS[name] || "", cls); }
function spinner(cls) { return svg(ICONS.loader, "spinner" + (cls ? " " + cls : "")); }

/* --- dom / format helpers ---------------------------------------------- */

const $ = (id) => document.getElementById(id);

function esc(v) {
  return String(v == null ? "" : v).replace(/[&<>"']/g, (c) => (
    { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]
  ));
}

function num(v, digits) {
  const n = Number(v || 0);
  if (!isFinite(n)) return "0";
  return n.toLocaleString("zh-CN", {
    minimumFractionDigits: digits || 0,
    maximumFractionDigits: digits || 0,
  });
}

function pct(v, digits) { return num(v, digits == null ? 1 : digits) + "%"; }

function fmtDuration(seconds) {
  const s = Math.max(0, Math.floor(seconds || 0));
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  if (d) return d + " 天 " + h + " 小时";
  if (h) return h + " 小时 " + m + " 分";
  if (m) return m + " 分 " + sec + " 秒";
  return sec + " 秒";
}

function fmtDate(ts) {
  if (!ts) return "—";
  const d = new Date(ts * 1000);
  const days = Math.round((d.getTime() - Date.now()) / 86400000);
  const label = d.toISOString().slice(0, 10);
  if (days < 0) return label + "（已过期）";
  if (days === 0) return label + "（今天）";
  return label + "（" + days + " 天后）";
}

function fmtDateTime(ms) {
  if (!ms) return "—";
  const d = new Date(ms);
  const p = (n) => String(n).padStart(2, "0");
  return p(d.getMonth() + 1) + "-" + p(d.getDate()) + " " +
    p(d.getHours()) + ":" + p(d.getMinutes()) + ":" + p(d.getSeconds());
}

function fmtAgo(ms) {
  const diff = Math.max(0, Date.now() - (ms || 0)) / 1000;
  if (diff < 60) return Math.floor(diff) + " 秒前";
  if (diff < 3600) return Math.floor(diff / 60) + " 分钟前";
  if (diff < 86400) return Math.floor(diff / 3600) + " 小时前";
  return Math.floor(diff / 86400) + " 天前";
}

function fmtMs(ms) {
  const v = Number(ms || 0);
  if (v < 1000) return v + " ms";
  return (v / 1000).toFixed(v < 10000 ? 2 : 1) + " s";
}

// Token budgets read badly as "1,000k", so anything at or above a million
// switches unit. The value is a ceiling, not a precision figure.
function fmtTokens(n) {
  const v = Number(n || 0);
  if (!v) return "—";
  if (v >= 1e6) return (v / 1e6).toFixed(v % 1e6 === 0 ? 0 : 1) + "M";
  if (v >= 1e3) return Math.round(v / 1e3) + "k";
  return String(v);
}

/* --- api ---------------------------------------------------------------- */

const CLIENT_KEY_STORE = "md2a:clientKey";

function clientKey() {
  try { return localStorage.getItem(CLIENT_KEY_STORE) || ""; } catch (e) { return ""; }
}
function setClientKey(v) {
  try {
    if (v) localStorage.setItem(CLIENT_KEY_STORE, v);
    else localStorage.removeItem(CLIENT_KEY_STORE);
  } catch (e) {}
}

// v1Headers attaches the optional client key. The admin console's own
// /admin/api/* calls need no credentials; only the /v1/* traffic does.
function v1Headers(extra) {
  const h = Object.assign({}, extra || {});
  const k = clientKey();
  if (k) h["Authorization"] = "Bearer " + k;
  return h;
}

async function api(path, opts) {
  const res = await fetch(path, opts);
  const text = await res.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch (e) { data = { raw: text }; }
  if (!res.ok) {
    const msg = (data && data.error && data.error.message) ||
      (data && data.message) || ("HTTP " + res.status);
    throw new Error(msg);
  }
  return data;
}

function apiJSON(path, method, body) {
  return api(path, {
    method,
    headers: { "content-type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
}

/* --- toast -------------------------------------------------------------- */

function toast(message, kind) {
  const host = $("toastHost");
  const el = document.createElement("div");
  el.className = "toast " + (kind === "ok" ? "ok" : kind === "err" ? "err" : "");
  el.innerHTML = icon(kind === "ok" ? "ok" : kind === "err" ? "fail" : "info") +
    '<span class="body"></span>';
  el.querySelector(".body").textContent = String(message);
  host.appendChild(el);
  setTimeout(() => {
    el.style.transition = "opacity .2s, transform .2s";
    el.style.opacity = "0";
    el.style.transform = "translateY(6px)";
    setTimeout(() => el.remove(), 240);
  }, kind === "err" ? 6500 : 3600);
}

/* --- dialog ------------------------------------------------------------- */

const dialogStack = [];

function openDialog(opts) {
  const host = $("dialogHost");
  const overlay = document.createElement("div");
  overlay.className = "overlay";
  overlay.innerHTML =
    '<div class="dialog' + (opts.wide ? " wide" : "") + '" role="dialog" aria-modal="true">' +
      '<div class="dialog-head"><h2 class="dialog-title"></h2>' +
        (opts.desc ? '<p class="dialog-desc"></p>' : "") +
      "</div>" +
      '<div class="dialog-body"></div>' +
      '<div class="dialog-foot"></div>' +
    "</div>";
  overlay.querySelector(".dialog-title").textContent = opts.title || "";
  if (opts.desc) overlay.querySelector(".dialog-desc").textContent = opts.desc;

  const body = overlay.querySelector(".dialog-body");
  if (typeof opts.body === "string") body.innerHTML = opts.body;
  else if (opts.body) body.appendChild(opts.body);

  const foot = overlay.querySelector(".dialog-foot");
  if (opts.actions) {
    opts.actions.forEach((a) => {
      const b = document.createElement("button");
      b.className = "btn " + (a.variant === undefined ? "" : a.variant);
      b.textContent = a.label;
      b.onclick = () => {
        if (!a.onClick) { closeDialog(); return; }
        // An action may veto closing by returning false (e.g. validation
        // failed, or the request is still in flight).
        const r = a.onClick();
        if (r === false) return;
        if (r && typeof r.then === "function") {
          r.then((ok) => { if (ok !== false) closeDialog(); }, () => {});
        } else {
          closeDialog();
        }
      };
      foot.appendChild(b);
    });
  } else {
    foot.style.display = "none";
  }

  overlay.addEventListener("mousedown", (e) => { if (e.target === overlay) closeDialog(); });
  host.appendChild(overlay);
  dialogStack.push(overlay);
  const focusable = overlay.querySelector("input, textarea, select, button");
  if (focusable) focusable.focus();
  return overlay;
}

function closeDialog() {
  const top = dialogStack.pop();
  if (top) top.remove();
}

function confirmDialog(title, desc, confirmLabel, onConfirm) {
  openDialog({
    title,
    desc,
    actions: [
      { label: "取消", variant: "secondary" },
      { label: confirmLabel || "确认", variant: "destructive", onClick: onConfirm },
    ],
  });
}

/* --- menu --------------------------------------------------------------- */

let openMenuEl = null;

function closeMenu() {
  if (openMenuEl) { openMenuEl.remove(); openMenuEl = null; }
  document.removeEventListener("mousedown", menuOutside, true);
  window.removeEventListener("resize", closeMenu);
  window.removeEventListener("scroll", closeMenu, true);
}

function menuOutside(e) {
  if (openMenuEl && !openMenuEl.contains(e.target)) closeMenu();
}

// openMenu renders a popover anchored to a trigger. Items are
// { label, icon, variant, checked, onClick }, { separator: true } or
// { heading: "..." }.
function openMenu(anchor, items, opts) {
  closeMenu();
  const menu = document.createElement("div");
  menu.className = "menu" + (opts && opts.wide ? " wide" : "");
  items.forEach((it) => {
    if (it.separator) {
      const sep = document.createElement("div");
      sep.className = "menu-sep";
      menu.appendChild(sep);
      return;
    }
    if (it.heading) {
      const h = document.createElement("div");
      h.className = "menu-label";
      h.textContent = it.heading;
      menu.appendChild(h);
      return;
    }
    const b = document.createElement("button");
    b.className = "menu-item" + (it.variant === "destructive" ? " destructive" : "");
    if (it.checked !== undefined) b.setAttribute("aria-checked", it.checked ? "true" : "false");
    b.innerHTML = (it.icon ? icon(it.icon) : "") + "<span></span>" +
      (it.checked !== undefined ? icon("check", "tick") : "");
    b.querySelector("span").textContent = it.label;
    b.onclick = () => { closeMenu(); if (it.onClick) it.onClick(); };
    menu.appendChild(b);
  });
  document.body.appendChild(menu);

  const r = anchor.getBoundingClientRect();
  const mw = menu.offsetWidth, mh = menu.offsetHeight;
  let left = opts && opts.align === "right" ? r.right - mw : r.left;
  left = Math.max(8, Math.min(left, window.innerWidth - mw - 8));
  let top = r.bottom + 6;
  if (top + mh > window.innerHeight - 8) top = Math.max(8, r.top - mh - 6);
  menu.style.left = Math.round(left) + "px";
  menu.style.top = Math.round(top) + "px";

  openMenuEl = menu;
  setTimeout(() => {
    document.addEventListener("mousedown", menuOutside, true);
    window.addEventListener("resize", closeMenu);
    window.addEventListener("scroll", closeMenu, true);
  }, 0);
  return menu;
}

/* --- theme -------------------------------------------------------------- */

const THEME_STORE = "md2a:theme";

function currentTheme() {
  try { return localStorage.getItem(THEME_STORE) || "system"; } catch (e) { return "system"; }
}

function applyTheme(theme) {
  const dark = theme === "dark" ||
    (theme === "system" && window.matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.setAttribute("data-theme", dark ? "dark" : "light");
  try { localStorage.setItem(THEME_STORE, theme); } catch (e) {}
  renderThemeButton();
}

function renderThemeButton() {
  const btn = $("themeBtn");
  if (!btn) return;
  const t = currentTheme();
  btn.innerHTML = icon(t === "light" ? "sun" : t === "dark" ? "moon" : "monitor");
  btn.title = "外观：" + (t === "light" ? "浅色" : t === "dark" ? "深色" : "跟随系统");
  btn.onclick = () => openMenu(btn, [
    { label: "浅色", icon: "sun", checked: currentTheme() === "light", onClick: () => applyTheme("light") },
    { label: "深色", icon: "moon", checked: currentTheme() === "dark", onClick: () => applyTheme("dark") },
    { label: "跟随系统", icon: "monitor", checked: currentTheme() === "system", onClick: () => applyTheme("system") },
  ], { align: "right" });
}

/* --- shared state ------------------------------------------------------- */

const STATE = { data: null, at: 0 };
const CATALOGS = { data: null, at: 0, loading: null };

async function loadState() {
  STATE.data = await api("/admin/api/state");
  STATE.at = Date.now();
  return STATE.data;
}

// catalogs() caches the media catalogues for a minute — they are fetched from
// the upstream gateway, so re-requesting them on every render would be rude.
async function catalogs(force) {
  if (!force && CATALOGS.data && Date.now() - CATALOGS.at < 60000) return CATALOGS.data;
  if (CATALOGS.loading) return CATALOGS.loading;
  CATALOGS.loading = api("/admin/api/catalogs").then((d) => {
    CATALOGS.data = d;
    CATALOGS.at = Date.now();
    CATALOGS.loading = null;
    return d;
  }, (e) => {
    CATALOGS.loading = null;
    throw e;
  });
  return CATALOGS.loading;
}

/* --- shared markup ------------------------------------------------------ */

function pageHeader(title, desc, actions) {
  return '<header class="page-header"><div><h1 class="page-title">' + esc(title) + "</h1>" +
    '<p class="page-desc">' + esc(desc) + "</p></div>" +
    (actions ? '<div class="page-actions">' + actions + "</div>" : "") + "</header>";
}

function panel(id, title, body, actions, style) {
  return '<section class="panel"' + (id ? ' id="' + id + '"' : "") +
    (style ? ' style="' + style + '"' : "") + ">" +
    '<header class="panel-head"><h2 class="panel-title">' + esc(title) + "</h2>" +
    (actions ? '<div class="panel-actions">' + actions + "</div>" : "") + "</header>" +
    '<div class="panel-body">' + body + "</div></section>";
}

function loadingHtml(minH) {
  return '<div class="loading-state"' + (minH ? ' style="min-height:' + minH + 'px"' : "") + ">" +
    svg(ICONS.loader, "spinner lg") + "</div>";
}

// The media catalogues are fetched from upstream, and the voice table alone is
// 671 rows — on a slow link the first load takes tens of seconds. Say so
// instead of showing an anonymous spinner that looks like a hang.
function catalogLoadingHtml() {
  return '<div class="loading-state" style="min-height:160px;gap:12px;flex-direction:column">' +
    svg(ICONS.loader, "spinner lg") +
    '<p class="hint" style="margin:0">正在从上游拉取目录…首次可能需要 30–60 秒，之后走服务端缓存（10 分钟）。</p>' +
    "</div>";
}

function emptyHtml(message) {
  return '<div class="empty-state">' + icon("inbox") + "<p>" + esc(message || "暂无数据") + "</p></div>";
}

function errorStateHtml(message) {
  return '<div class="error-state">' + icon("alert") + "<p>" + esc(message) + "</p>" +
    '<button class="btn secondary sm" data-retry>重试</button></div>';
}

function protocolBadge(protocol) {
  return '<span class="badge ' + esc(protocol) + '">' + esc(protocol) + "</span>";
}

function selectHtml(id, options, selected, placeholder) {
  let html = '<select class="select" id="' + id + '">';
  if (placeholder) html += '<option value="">' + esc(placeholder) + "</option>";
  options.forEach((o) => {
    const value = typeof o === "string" ? o : o.value;
    const label = typeof o === "string" ? o : o.label;
    html += '<option value="' + esc(value) + '"' +
      (String(value) === String(selected) ? " selected" : "") +
      (o && o.disabled ? " disabled" : "") + ">" + esc(label) + "</option>";
  });
  return html + "</select>";
}

// paramOptions / paramDefault read the media catalogues' upstream parameter
// blocks. The upstream shape is { default, options: [] }, but entries have been
// seen as bare arrays too, so both are accepted.
function paramOptions(params, key) {
  const p = params && params[key];
  if (!p) return [];
  if (Array.isArray(p)) return p.slice();
  if (Array.isArray(p.options)) return p.options.slice();
  return [];
}

function paramDefault(params, key) {
  const p = params && params[key];
  if (p && !Array.isArray(p) && p.default != null && p.default !== "") return String(p.default);
  const o = paramOptions(params, key);
  return o.length ? String(o[0]) : "";
}

function copyToClipboard(value) {
  if (navigator.clipboard && window.isSecureContext) {
    return navigator.clipboard.writeText(value).then(() => true, () => fallbackCopy(value));
  }
  return Promise.resolve(fallbackCopy(value));
}

function fallbackCopy(value) {
  try {
    const ta = document.createElement("textarea");
    ta.value = value;
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    const ok = document.execCommand("copy");
    ta.remove();
    return ok;
  } catch (e) {
    return false;
  }
}

/* --- sidebar ------------------------------------------------------------ */

const NAV = [
  { href: "#/dashboard", label: "仪表盘", icon: "dashboard" },
  { href: "#/models", label: "模型", icon: "box" },
  { href: "#/pool", label: "号池", icon: "users" },
  { href: "#/playground", label: "调试台", icon: "sparkles" },
];

const NAV_FOOT = [
  { href: "#/settings", label: "设置", icon: "settings" },
  { href: "#/about", label: "关于", icon: "info" },
];

const openDocGroups = {};

function renderSidebar() {
  const nav = $("sidebarNav");
  if (!nav) return;
  const route = parseRoute();
  const activeKey = route.path || "dashboard";

  let html = '<div class="nav-group">';
  NAV.forEach((n) => {
    html += '<a class="nav-item' + (activeKey === n.href.slice(2) ? " active" : "") + '" href="' + n.href + '" data-nav>' +
      '<span class="nav-icon">' + icon(n.icon) + "</span>" + esc(n.label) + "</a>";
  });
  html += "</div>";

  html += '<div class="nav-section"><div class="nav-section-title">文档</div><div class="nav-group">';
  DOCS.forEach((g) => {
    const groupActive = g.items.some((i) => route.path === "docs/" + i.slug);
    const open = openDocGroups[g.group] !== undefined ? openDocGroups[g.group] : groupActive;
    html += '<button type="button" class="nav-item" data-docgroup="' + esc(g.group) + '"' +
      ' aria-expanded="' + (open ? "true" : "false") + '">' +
      '<span class="nav-icon">' + icon(g.icon) + "</span>" +
      '<span class="grow" style="text-align:left">' + esc(g.group) + "</span>" +
      '<span class="nav-icon" style="width:12px;height:12px;transform:rotate(' + (open ? "0" : "-90") +
      'deg);transition:transform .2s">' + svg(ICONS.chevron, "") + "</span></button>";
    html += '<div class="nav-sub' + (open ? " open" : "") + '"><div><div class="nav-sub-inner">';
    g.items.forEach((it) => {
      html += '<a class="nav-sub-item' + (route.path === "docs/" + it.slug ? " active" : "") +
        '" href="#/docs/' + it.slug + '" data-nav>' +
        '<span class="label">' + esc(it.title) + "</span>" +
        '<span class="nav-method ' + it.method.toLowerCase() + '">' + esc(it.method) + "</span></a>";
    });
    html += "</div></div></div>";
  });
  html += "</div></div>";

  html += '<div class="nav-section"><div class="nav-group">';
  NAV_FOOT.forEach((n) => {
    html += '<a class="nav-item' + (activeKey === n.href.slice(2) ? " active" : "") + '" href="' + n.href + '" data-nav>' +
      '<span class="nav-icon">' + icon(n.icon) + "</span>" + esc(n.label) + "</a>";
  });
  html += "</div></div>";

  nav.innerHTML = html;
}

function renderFootStatus() {
  const el = $("footStatus");
  if (!el) return;
  const s = STATE.data;
  if (!s) { el.textContent = "连接中…"; return; }
  el.textContent = s.pool_usable + "/" + s.pool.length + " 令牌";
  el.title = "可用令牌 / 全部令牌 · " + s.models.length + " 个模型";
  const brand = $("brandVersion");
  if (brand) brand.textContent = "v" + s.version;
}

/* --- mobile drawer ------------------------------------------------------ */

function openDrawer() {
  const host = $("drawerHost");
  const overlay = document.createElement("div");
  overlay.className = "drawer-overlay";
  overlay.innerHTML = '<div class="drawer"><div class="drawer-head">' +
    '<span class="brand" style="font-size:14px"><span class="brand-name">MiniMax Design 2API</span></span>' +
    '<button class="btn ghost icon" data-close aria-label="关闭">' + svg(ICONS.x, "") + "</button></div></div>";

  const drawer = overlay.querySelector(".drawer");
  const clone = $("sidebarNav").cloneNode(true);
  clone.id = "drawerNav";
  drawer.appendChild(clone);

  const foot = document.createElement("div");
  foot.className = "sidebar-foot";
  foot.style.borderTop = "1px solid var(--sidebar-border)";
  foot.innerHTML = '<div class="account-control"><span class="account-name">' + esc($("footStatus").textContent) + "</span></div>";
  drawer.appendChild(foot);

  overlay.addEventListener("click", (e) => {
    if (e.target.closest("[data-docgroup]")) return;
    if (e.target === overlay || e.target.closest("[data-close]") || e.target.closest("[data-nav]")) overlay.remove();
  });
  host.appendChild(overlay);
}

/* --- router ------------------------------------------------------------- */

function parseRoute() {
  const raw = location.hash.replace(/^#\/?/, "");
  const clean = raw.split("?")[0];
  return { path: clean, parts: clean.split("/").filter(Boolean) };
}

let currentRender = 0;

async function render() {
  const route = parseRoute();
  renderSidebar();

  if (route.parts[0] === "docs") {
    renderDocs(route.parts.slice(1).join("/") || DOCS[0].items[0].slug);
    return;
  }

  const key = route.parts[0] || "dashboard";
  const token = ++currentRender;
  const view = $("view");
  try {
    if (key === "models") await pageModels(token);
    else if (key === "pool") await pagePool(token);
    else if (key === "playground") await pagePlayground(token);
    else if (key === "settings") await pageSettings(token);
    else if (key === "about") await pageAbout(token);
    else await pageDashboard(token);
  } catch (e) {
    if (token !== currentRender) return;
    view.innerHTML = '<div class="page">' + errorStateHtml(e.message || String(e)) + "</div>";
    const btn = view.querySelector("[data-retry]");
    if (btn) btn.onclick = () => render();
  }
}

/* --- global wiring ------------------------------------------------------ */

document.addEventListener("keydown", (e) => {
  if (e.key !== "Escape") return;
  if (dialogStack.length) { closeDialog(); return; }
  closeMenu();
});

document.addEventListener("click", (e) => {
  const group = e.target.closest("[data-docgroup]");
  if (group) {
    const name = group.dataset.docgroup;
    const sub = group.nextElementSibling;
    const open = group.getAttribute("aria-expanded") === "true";
    openDocGroups[name] = !open;
    group.setAttribute("aria-expanded", open ? "false" : "true");
    if (sub) sub.classList.toggle("open", !open);
    const arrow = group.querySelector(".nav-icon:last-child");
    if (arrow) arrow.style.transform = "rotate(" + (open ? "-90" : "0") + "deg)";
    return;
  }

  const copyBtn = e.target.closest("[data-copy]");
  if (copyBtn) {
    copyToClipboard(copyBtn.dataset.copy).then((ok) => {
      if (ok) toast("已复制", "ok");
      else toast("复制失败", "err");
    });
  }
});

window.addEventListener("hashchange", () => {
  if (!location.hash) location.hash = "#/dashboard";
  else render();
});

function boot() {
  applyTheme(currentTheme());
  const menuBtn = $("menuBtn");
  if (menuBtn) menuBtn.onclick = openDrawer;

  window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => {
    if (currentTheme() === "system") applyTheme("system");
  });

  if (!location.hash) {
    location.hash = "#/dashboard";
    return; // the hashchange handler will render
  }
  render();
}

document.addEventListener("DOMContentLoaded", boot);
