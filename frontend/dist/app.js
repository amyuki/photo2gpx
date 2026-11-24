(() => {
  "use strict";
  const api = (name, ...args) => window.go.main.App[name](...args);
  // UI errors go to the local log file (Help → Copy diagnostic info)
  const logUI = (msg) => { try { if (window.go) api("LogFrontend", String(msg)); } catch (e) {} };
  window.addEventListener("error", (e) => logUI(`${e.message} @ ${e.filename}:${e.lineno}`));
  window.addEventListener("unhandledrejection", (e) => logUI("promise: " + (e.reason && (e.reason.stack || e.reason))));
  const $ = (id) => document.getElementById(id);
  const store = {
    get(k) { try { return localStorage.getItem(k); } catch (e) { return null; } },
    set(k, v) { try { localStorage.setItem(k, v); } catch (e) {} },
  };

  // ---- language ------------------------------------------------------------------
  function detectLang() {
    const nav = (navigator.languages && navigator.languages[0]) || navigator.language || "en";
    const l = nav.toLowerCase();
    if (l.startsWith("zh")) {
      return /hant|tw|hk|mo/.test(l) ? "zh-Hant" : "zh-Hans";
    }
    if (l.startsWith("fr")) return "fr";
    return "en";
  }
  let lang = detectLang();
  function t(key, args) {
    let s = (window.I18N[lang] && window.I18N[lang][key]) ?? window.I18N.en[key] ?? key;
    if (args) s = s.replace(/\{(\w+)\}/g, (_, k) => (args[k] ?? ""));
    return s;
  }
  // Changes only text: the map layers are left alone so nothing flickers.
  function applyLang() {
    document.documentElement.lang = lang;
    document.title = t("appTitle");
    document.querySelectorAll("[data-i18n]").forEach((el) => (el.textContent = t(el.dataset.i18n)));
    document.querySelectorAll("[data-i18n-html]").forEach((el) => (el.innerHTML = t(el.dataset.i18nHtml)));
    $("sCount").title = t("stPhotosTitle");
    buildLayerControl();
    if (startMarker) startMarker.setTooltipContent(t("start"));
    if (endMarker) endMarker.setTooltipContent(t("end"));
    if (view) renderPanel(view);
    if (!$("about").hidden) showAbout();
    if (!$("upd").hidden && updInfo) showUpdate(updInfo, updAuto);
  }
  function setLang(l) {
    if (!window.I18N[l] || l === lang) return;
    lang = l;
    map.closePopup();
    applyLang();
  }

  const ISSUE = new Set(["snapped", "cached", "outlier", "excluded", "notime"]);
  const COLORS = { ok: "#2f9e44", snapped: "#e8a10c", nogps: "#1c7ed6", bad: "#8a8f8b", excluded: "#c92a2a", track: "#d9480f", route: "#4f5651" };

  // ---- map ---------------------------------------------------------------------
  const map = L.map("map", { zoomControl: true }).setView([46.3, 6.6], 6);
  // tiles are fetched and cached by the Go side (see tiles.go)
  const layers = {
    topo: L.tileLayer("/tiles/topo/{z}/{x}/{y}", { maxZoom: 17, attribution: "© OpenStreetMap contributors, SRTM | © OpenTopoMap (CC-BY-SA)" }),
    osm: L.tileLayer("/tiles/osm/{z}/{x}/{y}", { maxZoom: 19, attribution: "© OpenStreetMap contributors" }),
  };
  const LAYER_KEY = { topo: "layerTopo", osm: "layerOsm" };
  let base = store.get("basemap2");
  if (!layers[base]) base = "topo";
  layers[base].addTo(map);
  let layerControl = null;
  function buildLayerControl() {
    if (layerControl) map.removeControl(layerControl);
    const named = {};
    Object.keys(layers).forEach((k) => (named[t(LAYER_KEY[k])] = layers[k]));
    layerControl = L.control.layers(named, null, { position: "topright" }).addTo(map);
  }
  map.on("baselayerchange", (e) => {
    const k = Object.keys(layers).find((key) => layers[key] === e.layer);
    if (k) store.set("basemap2", k);
  });
  L.control.scale({ imperial: false }).addTo(map);

  const routeLayer = L.layerGroup().addTo(map);
  const trackLayer = L.layerGroup().addTo(map);
  const pointLayer = L.layerGroup().addTo(map);

  let view = null;
  let markers = {};      // id -> marker
  let filter = "all";
  let selected = null;
  let lastCount = -1;
  let lastRoute = null;
  let startMarker = null, endMarker = null;

  // ---- helpers -------------------------------------------------------------------
  function toast(msg, ms = 3500) {
    const el = $("toast");
    el.textContent = msg; el.hidden = false;
    clearTimeout(toast._h);
    toast._h = setTimeout(() => (el.hidden = true), ms);
  }
  async function busy(fn) {
    const b = $("busy");
    const h = setTimeout(() => (b.hidden = false), 150);
    try { return await fn(); } finally { clearTimeout(h); b.hidden = true; }
  }
  function esc(s) { return String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c])); }
  function statusText(p) {
    let s = t("st_" + p.status);
    if (p.status === "snapped") s += t("snappedOff", { m: Math.round(p.offM) });
    if ((p.status === "cached" || p.status === "outlier") && p.offM > 0) s += t("offTrack", { m: Math.round(p.offM) });
    if (p.override === "include") s += t("manualUse");
    return s;
  }

  // ---- rendering -------------------------------------------------------------------
  function render(v, opts = {}) {
    view = v;
    renderPanel(v);
    renderMap(v, opts);
  }

  // side panel: texts, stats, warnings, list
  function renderPanel(v) {
    const n = v.photos ? v.photos.length : 0;
    $("empty").hidden = n > 0 || (v.route && v.route.length);
    $("legend").hidden = n === 0;

    // route card
    const hasRoute = !!(v.route && v.route.length);
    $("routeNone").hidden = hasRoute;
    $("routeSome").hidden = !hasRoute;
    if (hasRoute) {
      $("routeName").textContent = v.routeName || t("refRoute");
      $("dir").value = v.direction || "auto";
    }

    // stats
    const used = (v.photos || []).filter((p) => p.used).length;
    $("sDist").textContent = v.ready ? v.distKm.toFixed(2) + " km" : "–";
    $("sTime").textContent = v.ready ? v.start.slice(11) + "–" + v.end : "–";
    $("sTime").title = v.ready ? v.start.slice(0, 10) : "";
    $("sDur").textContent = v.ready ? v.duration : "–";
    $("sCount").textContent = n ? `${used}/${n}` : "–";
    $("btnSave").disabled = !v.ready;

    // warnings
    const w = $("warnings");
    w.innerHTML = "";
    (v.warnings || []).forEach((m) => {
      const d = document.createElement("div");
      d.className = "warn"; d.textContent = t("w_" + m.code, m.args); w.appendChild(d);
    });
    renderList();
  }

  function renderMap(v, opts = {}) {
    const n = v.photos ? v.photos.length : 0;
    const hasRoute = !!(v.route && v.route.length);

    // reference route
    routeLayer.clearLayers();
    if (hasRoute) {
      L.polyline(v.route, { color: COLORS.route, weight: 3, opacity: 0.55, dashArray: "6 6" }).addTo(routeLayer);
    }
    // track
    trackLayer.clearLayers();
    if (v.track && v.track.length > 1) {
      L.polyline(v.track, { color: "#fff", weight: 7, opacity: 0.8 }).addTo(trackLayer);
      L.polyline(v.track, { color: COLORS.track, weight: 4 }).addTo(trackLayer);
      startMarker = L.circleMarker(v.track[0], { radius: 7, color: "#fff", weight: 2, fillColor: "#2b6a4f", fillOpacity: 1 }).bindTooltip(t("start")).addTo(trackLayer);
      endMarker = L.circleMarker(v.track[v.track.length - 1], { radius: 7, color: "#fff", weight: 2, fillColor: "#1f2421", fillOpacity: 1 }).bindTooltip(t("end")).addTo(trackLayer);
    } else {
      startMarker = endMarker = null;
    }
    // points
    pointLayer.clearLayers();
    markers = {};
    (v.photos || []).forEach((p) => {
      let m = null;
      if (p.used && p.placed) {
        m = L.circleMarker(p.placed, { radius: 6, color: "#fff", weight: 1.5, fillColor: p.status === "snapped" ? COLORS.snapped : COLORS.ok, fillOpacity: 1 });
      } else if (p.hasGPS) {
        // ignored GPS fix: show where the phone claimed to be
        const col = p.status === "excluded" ? COLORS.excluded : COLORS.bad;
        m = L.circleMarker(p.raw, { radius: 5, color: col, weight: 2, fillColor: "#fff", fillOpacity: 0.9 });
      } else if (p.placed) {
        m = L.circleMarker(p.placed, { radius: 4.5, color: "#fff", weight: 1, fillColor: COLORS.nogps, fillOpacity: 1 });
      }
      if (!m) return;
      m.bindPopup(() => popupFor(p), { maxWidth: 270 });
      m.on("popupopen", () => { selected = p.id; highlightList(); });
      m.addTo(pointLayer);
      markers[p.id] = m;
    });

    // fit view when the data set changed
    const routeKey = hasRoute ? v.route.length + ":" + v.route[0] : "";
    if (!opts.keepView && (n !== lastCount || routeKey !== lastRoute)) {
      lastCount = n; lastRoute = routeKey;
      const pts = [];
      (v.track || []).forEach((q) => pts.push(q));
      if (!pts.length && hasRoute) v.route.forEach((q) => pts.push(q));
      if (!pts.length) (v.photos || []).forEach((p) => p.hasGPS && pts.push(p.raw));
      if (pts.length) map.fitBounds(L.latLngBounds(pts).pad(0.08));
    }
    if (opts.reopen && markers[opts.reopen]) markers[opts.reopen].openPopup();
  }

  function popupFor(p) {
    const div = document.createElement("div");
    div.className = "pop";
    const canExclude = p.hasGPS && p.status !== "excluded" && p.override !== "exclude";
    const canInclude = p.hasGPS && (p.status === "cached" || p.status === "outlier");
    const canAuto = p.override !== "auto";
    div.innerHTML = `
      <img alt="">
      <div class="pn">${esc(p.name)}</div>
      <div class="pt">${esc(p.local || "")}</div>
      <div class="ps"><i class="dot ${p.status}"></i> ${esc(statusText(p))}</div>
      <div class="row"></div>`;
    const row = div.querySelector(".row");
    const btn = (label, fn, cls) => {
      const b = document.createElement("button");
      b.textContent = label; if (cls) b.className = cls;
      b.onclick = fn; row.appendChild(b);
    };
    if (canExclude) btn(t("popExclude"), () => setOv(p.id, "exclude"));
    if (canInclude) btn(t("popInclude"), () => setOv(p.id, "include"));
    if (canAuto) btn(t("popAuto"), () => setOv(p.id, "auto"));
    btn(t("popRemove"), () => removePhoto(p.id), "ghost");
    loadThumb(div.querySelector("img"), p.id);
    return div;
  }
  // The thumbnail is loaded straight into this popup's own <img>. (Looking it
  // up by id failed when the same photo was reopened: the closing popup was
  // still fading out with the same id, and received the picture instead.)
  const thumbCache = {};
  async function loadThumb(img, id) {
    if (thumbCache[id]) { img.src = thumbCache[id]; return; }
    try {
      const url = await api("Thumbnail", id);
      if (url) { thumbCache[id] = url; img.src = url; } else img.remove();
    } catch (e) { img.remove(); }
  }

  function renderList() {
    const ul = $("list");
    ul.innerHTML = "";
    const items = (view && view.photos ? view.photos : []).filter((p) => filter === "all" || ISSUE.has(p.status));
    if (!items.length) {
      const li = document.createElement("li");
      li.className = "empty-li";
      li.innerHTML = `<span class="emptyrow">${esc(view && view.photos && view.photos.length ? t("noIssues") : t("noPhotos"))}</span>`;
      ul.appendChild(li);
      return;
    }
    items.forEach((p) => {
      const li = document.createElement("li");
      li.dataset.id = p.id;
      const bad = p.status === "cached" || p.status === "outlier" || p.status === "excluded" || p.status === "notime";
      li.innerHTML = `<i class="dot ${p.status}"></i><span class="t">${esc((p.local || "").slice(0, 8))}</span>
        <span class="n" title="${esc(p.name)}">${esc(p.name)}<span class="s ${bad ? "bad" : ""}">${esc(statusText(p))}</span></span>`;
      li.onclick = () => focusPhoto(p.id);
      ul.appendChild(li);
    });
    highlightList();
  }
  function highlightList() {
    document.querySelectorAll("#list li").forEach((li) => li.classList.toggle("sel", li.dataset.id === selected));
  }
  function focusPhoto(id) {
    selected = id; highlightList();
    const m = markers[id];
    if (!m) { toast(t("cantPlace")); return; }
    // no animation: an animated pan would fight with the popup's auto-pan
    map.setView(m.getLatLng(), Math.max(map.getZoom(), 16), { animate: false });
    m.openPopup();
  }

  // ---- actions -----------------------------------------------------------------------
  async function setOv(id, mode) {
    const v = await busy(() => api("SetOverride", id, mode));
    render(v, { reopen: id });
  }
  async function removePhoto(id) {
    map.closePopup();
    render(await busy(() => api("RemovePhoto", id)));
  }
  async function addPaths(paths) {
    if (!paths || !paths.length) return;
    render(await busy(() => api("AddPaths", paths)));
  }

  $("btnPhotos").onclick = async () => render(await busy(() => api("PickPhotos")));
  $("btnRoute").onclick = async () => render(await busy(() => api("PickRoute")));
  $("btnRemoveRoute").onclick = async () => render(await api("RemoveRoute"));
  $("dir").onchange = async (e) => render(await busy(() => api("SetDirection", e.target.value)));
  $("btnClear").onclick = async () => { map.closePopup(); lastCount = -1; render(await api("Clear")); };
  document.querySelectorAll(".seg button").forEach((b) => {
    b.onclick = () => {
      filter = b.dataset.filter;
      document.querySelectorAll(".seg button").forEach((x) => x.classList.toggle("on", x === b));
      renderList();
    };
  });
  $("btnSave").onclick = async () => {
    const r = await api("Save", $("wpts").checked);
    if (r.error) toast(t("saveFail", { err: r.error === "noTrack" ? t("w_noTrack") : r.error }), 6000);
    else if (r.path) toast(t("saved", { path: r.path }), 6000);
  };

  // drag & drop (paths come from the native side)
  window.addEventListener("dragenter", () => document.body.classList.add("dragging"));
  window.addEventListener("dragleave", (e) => { if (!e.relatedTarget) document.body.classList.remove("dragging"); });
  window.addEventListener("drop", () => document.body.classList.remove("dragging"));

  // ---- about dialog & menu ---------------------------------------------------------
  let info = null;
  function openURL(url) {
    if (window.runtime && window.runtime.BrowserOpenURL) window.runtime.BrowserOpenURL(url);
    else window.open(url, "_blank");
  }
  async function showAbout() {
    if (!info) info = await api("Info");
    $("aboutVer").textContent = t("version", { v: info.version });
    $("aboutSite").textContent = info.website.replace(/^https?:\/\//, "");
    $("aboutCopy").textContent = t("copyright", { year: info.year, author: info.author });
    $("about").hidden = false;
    $("aboutClose").focus();
  }
  const hideAbout = () => ($("about").hidden = true);
  $("aboutClose").onclick = hideAbout;
  $("aboutX").onclick = hideAbout;
  $("about").onclick = (e) => { if (e.target === $("about")) hideAbout(); };
  document.addEventListener("keydown", (e) => { if (e.key === "Escape") { hideAbout(); if (!updBusy) hideUpdate(); } });
  $("aboutSite").onclick = (e) => { e.preventDefault(); if (info) openURL(info.website); };
  $("aboutDonate").onclick = async () => { if (!info) info = await api("Info"); openURL(info.donate); };

  // ---- updates ------------------------------------------------------------------------
  let updInfo = null, updAuto = false, updBusy = false;
  function showUpdate(info, auto) {
    updInfo = info; updAuto = auto;
    $("updTitle").textContent = t("updTitle");
    $("updVer").textContent = t("updVersion", { latest: info.latest, current: info.current });
    $("updNotes").textContent = (info.notes || "").trim();
    $("updNotesWrap").hidden = !(info.notes || "").trim();
    $("updProgress").hidden = true;
    $("updMsg").textContent = "";
    $("updSkip").hidden = !auto;
    $("updNow").textContent = info.canInstall ? t("updNow") : t("updOpenPage");
    ["updNow", "updLater", "updSkip"].forEach((id) => ($(id).disabled = false));
    $("upd").hidden = false;
  }
  function hideUpdate() { $("upd").hidden = true; }
  async function checkUpdate(manual) {
    const info = manual ? await busy(() => api("CheckUpdate")) : await api("AutoCheckUpdate");
    if (info.available) showUpdate(info, !manual);
    else if (manual) toast(info.error ? t("updCheckFail", { err: info.error }) : t("updLatest", { v: info.current }), 5000);
  }
  $("updLater").onclick = hideUpdate;
  $("updX").onclick = () => { if (!updBusy) hideUpdate(); };
  $("updSkip").onclick = () => { api("SkipUpdate", updInfo.latest); hideUpdate(); };
  $("updNow").onclick = async () => {
    if (!updInfo.canInstall) { openURL(updInfo.pageURL); hideUpdate(); return; }
    updBusy = true;
    ["updNow", "updLater", "updSkip"].forEach((id) => ($(id).disabled = true));
    $("updProgress").hidden = false;
    $("updBar").style.width = "0%";
    $("updMsg").textContent = t("updDownloading", { pct: 0 });
    const err = await api("InstallUpdate");
    updBusy = false;
    if (err) {
      $("updMsg").textContent = t("updFail", { err });
      $("updNow").textContent = t("updOpenPage");
      $("updNow").disabled = false; $("updLater").disabled = false;
      updInfo = Object.assign({}, updInfo, { canInstall: false });
    }
  };
  function onUpdateProgress(p) {
    if (p.done < 0) { $("updBar").style.width = "100%"; $("updMsg").textContent = t("updInstalling"); return; }
    const pct = p.total > 0 ? Math.round((100 * p.done) / p.total) : 0;
    $("updBar").style.width = pct + "%";
    $("updMsg").textContent = t("updDownloading", { pct });
  }

  function onMenu(action) {
    switch (action) {
      case "photos": $("btnPhotos").click(); break;
      case "route": $("btnRoute").click(); break;
      case "save": if (!$("btnSave").disabled) $("btnSave").click(); break;
      case "clear": $("btnClear").click(); break;
      case "about": showAbout(); break;
      case "checkUpdate": checkUpdate(true); break;
      case "copyDiag":
        api("CopyDiagnosticInfo").then((ok) => toast(ok ? t("diagCopied") : t("diagFail"), 6000));
        break;
    }
  }
  window.__onMenu = onMenu; // used by the browser preview

  applyLang();
  async function start() {
    if (window.runtime && window.runtime.EventsOn) {
      window.runtime.EventsOn("menu", onMenu);
      window.runtime.EventsOn("lang", setLang);
      window.runtime.EventsOn("update-progress", onUpdateProgress);
    }
    if (window.runtime && window.runtime.OnFileDrop) {
      window.runtime.OnFileDrop((x, y, paths) => { document.body.classList.remove("dragging"); addPaths(paths); }, false);
    }
    // language: stored by the Go side; on first start use the system language
    try {
      const prefs = await api("GetPrefs");
      if (prefs.lang && window.I18N[prefs.lang]) setLang(prefs.lang);
      else api("SetLanguage", lang);
    } catch (e) {}
    render(await api("State"));
    setTimeout(() => checkUpdate(false).catch(() => {}), 1500);
  }
  if (window.go) start();
  else window.addEventListener("load", start);
})();
