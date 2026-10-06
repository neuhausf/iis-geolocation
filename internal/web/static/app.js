"use strict";
(() => {
const $ = (s, el = document) => el.querySelector(s);
const $$ = (s, el = document) => [...el.querySelectorAll(s)];
const nf = new Intl.NumberFormat("de-CH");
const pf = new Intl.NumberFormat("de-CH", { maximumFractionDigits: 1, minimumFractionDigits: 1 });
const fmt = n => nf.format(n);
const esc = s => String(s ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const store = {
  get(k, d) { try { const v = localStorage.getItem("iisgeo." + k); return v === null ? d : JSON.parse(v); } catch { return d; } },
  set(k, v) { try { localStorage.setItem("iisgeo." + k, JSON.stringify(v)); } catch { } },
};

const STATUS = [[2, "2xx"], [3, "3xx"], [4, "4xx"], [5, "5xx"], [1, "1xx"], [0, "ohne"]];
const S = {
  from: "", to: "", tz: store.get("tz", "utc"), excludePrivate: store.get("excludePrivate", true),
  status: new Set(), // leer = alle
  country: "", countryName: "", asn: 0, ispName: "", region: "",
  tab: "countries", tlMode: "ips", search: "", sort: {},
  hasCity: false, base: null, drill: null, world: null, dataFrom: "", dataTo: "", initialized: false,
};

async function api(path, opts = {}) {
  const r = await fetch(path, { ...opts, headers: { "X-Token": window.TOKEN, "Content-Type": "application/json", ...(opts.headers || {}) } });
  const ct = r.headers.get("Content-Type") || "";
  const body = ct.includes("json") ? await r.json() : await r.text();
  if (!r.ok) throw new Error((body && body.error) || body || r.statusText);
  return body;
}

// ---------- Tooltip ----------
const tip = $("#tip");
function showTip(e, html) {
  tip.innerHTML = html; tip.hidden = false;
  const pad = 14, w = tip.offsetWidth, h = tip.offsetHeight;
  let x = e.clientX + pad, y = e.clientY + pad;
  if (x + w > innerWidth - 8) x = e.clientX - w - pad;
  if (y + h > innerHeight - 8) y = e.clientY - h - pad;
  tip.style.left = Math.max(8, x) + "px"; tip.style.top = Math.max(8, y) + "px";
}
const hideTip = () => { tip.hidden = true; };
const tipRows = (title, rows) => `<div class="t">${esc(title)}</div>` + rows.map(([k, v]) => `<div class="r"><span>${esc(k)}</span><b>${esc(v)}</b></div>`).join("");

// ---------- Info / Laden ----------
async function init() {
  $("#f-tz").value = S.tz;
  $("#f-private").checked = S.excludePrivate;
  $("#path-input").value = store.get("path", "");
  try {
    const info = await api("/api/info");
    S.hasCity = info.hasCity;
    $("#opt-xff").checked = store.get("xff", info.defaultXff);
    $("#ver").textContent = "iis-geo " + info.version;
    $("#dbinfo").textContent = info.dbs.map(d => `${d.kind}: ${d.type} (Stand ${d.built.slice(0, 10)}${d.source !== "eingebettet" ? ", " + d.source : ""})`).join(" · ")
      + (info.hasCity ? "" : " · Regionen: nicht enthalten");
    $("#f-tz").options[1].textContent = `Lokalzeit (${info.localTz})`;
  } catch (e) { $("#dbinfo").textContent = "Fehler: " + e.message; }
  if (!S.hasCity) {
    const t = $("#tab-regions"); t.disabled = true;
    t.title = "Regionen sind nur in der Variante „iis-geo-regionen.exe“ enthalten (oder dbip-city-lite-*.mmdb neben die EXE legen).";
  }
  fetch("world.json").then(r => r.json()).then(w => { S.world = w; renderMap(); }).catch(() => { });
  bind();
  poll();
}

let wasActive = null, lastLiveRefresh = 0, uploading = false;
async function poll() {
  try {
    const st = await api("/api/status");
    renderFiles(st.files);
    const p = st.progress;
    if (p.active && !uploading) {
      showProgress(p.file ? `Lese ${p.file}  (${p.filesDone + 1}/${p.filesTotal}, ${fmt(p.lines)} Zeilen bisher)` : "Lese …", p.bytesTotal ? p.bytesDone / p.bytesTotal : null);
      if (Date.now() - lastLiveRefresh > 3000) { lastLiveRefresh = Date.now(); refresh(); }
    } else if (!uploading) {
      $("#progress").hidden = true;
    }
    if (wasActive !== false && !p.active) {
      if (st.files.some(f => f.parsed > 0)) refresh(); else showEmpty(true);
    }
    wasActive = p.active;
    setTimeout(poll, p.active ? 500 : 2000);
  } catch (e) {
    showProgress("Verbindung zum Programm verloren – läuft iis-geo.exe noch?", 0, true);
    setTimeout(poll, 3000);
  }
}

function showProgress(label, frac, error) {
  $("#progress").hidden = false;
  $("#progress-label").textContent = label;
  $("#progress-label").classList.toggle("err", !!error);
  const bar = $("#progress .bar");
  bar.classList.toggle("indet", frac == null);
  $("#progress-fill").style.width = frac == null ? "" : Math.min(100, frac * 100).toFixed(1) + "%";
  $("#progress-pct").textContent = frac == null ? "" : Math.min(100, Math.round(frac * 100)) + " %";
}

function renderFiles(files) {
  const box = $("#files-box");
  box.hidden = !files.length;
  if (!files.length) return;
  const ok = files.filter(f => f.parsed > 0);
  const lines = ok.reduce((a, f) => a + f.parsed, 0);
  const errs = files.filter(f => f.error && !f.dupe).length;
  $("#files-summary").innerHTML = `${fmt(ok.length)} Datei(en) geladen · ${fmt(lines)} Zeilen` + (errs ? ` · <span class="err">${errs} mit Fehler</span>` : "");
  const ts = u => u ? new Date(u * 1000).toISOString().slice(0, 16).replace("T", " ") : "";
  $("#files-table tbody").innerHTML = files.map(f => `<tr><td class="mono">${esc(f.name)}</td><td class="num">${fmt(f.parsed)}</td><td class="num">${fmt(f.skipped)}</td><td>${ts(f.from)} – ${ts(f.to)}</td><td class="${f.error && !f.dupe ? "err" : "muted"}">${esc(f.error || (f.usedXff ? `X-Forwarded-For in ${fmt(f.usedXff)} Zeilen verwendet` : ""))}</td></tr>`).join("");
}

function uploadFiles(fileList) {
  const files = [...fileList].filter(f => f.size > 0);
  if (!files.length) return;
  const fd = new FormData();
  const qs = new URLSearchParams({ xff: $("#opt-xff").checked ? "1" : "0" });
  files.forEach((f, i) => { fd.append("f" + i, f, f.name); qs.set("size_f" + i, f.size); });
  const total = files.reduce((a, f) => a + f.size, 0);
  const xhr = new XMLHttpRequest();
  xhr.open("POST", "/api/upload?" + qs);
  xhr.setRequestHeader("X-Token", window.TOKEN);
  uploading = true;
  showProgress(`Lese ${files.length} Datei(en) (${(total / 1048576).toFixed(1)} MB) …`, 0);
  xhr.upload.onprogress = e => { if (e.lengthComputable) showProgress(`Lese ${files.length} Datei(en) (${(total / 1048576).toFixed(1)} MB) …`, e.loaded / e.total * 0.999); };
  xhr.onload = () => {
    uploading = false; $("#progress").hidden = true;
    if (xhr.status !== 200) { let m = xhr.responseText; try { m = JSON.parse(m).error; } catch { } showProgress("Fehler: " + m, 0, true); }
    refresh();
  };
  xhr.onerror = () => { uploading = false; showProgress("Upload fehlgeschlagen", 0, true); };
  xhr.send(fd);
}

async function loadPaths() {
  const v = $("#path-input").value.trim();
  if (!v) { $("#path-input").focus(); return; }
  store.set("path", v);
  const paths = v.split(/[;\n]/).map(s => s.trim().replace(/^"|"$/g, "")).filter(Boolean);
  try {
    showProgress("Suche Dateien …", null);
    await api("/api/load-path", { method: "POST", body: JSON.stringify({ paths, xff: $("#opt-xff").checked }) });
    wasActive = true;
  } catch (e) { showProgress("Fehler: " + e.message, 0, true); }
}

// ---------- Abfrage ----------
function baseQuery() {
  const st = S.status.size ? [...S.status] : [];
  return { from: S.from, to: S.to, tz: S.tz, status: st, excludePrivate: S.excludePrivate };
}
function drillQuery() { return { ...baseQuery(), country: S.country, asn: S.asn, region: S.region }; }
const hasDrill = () => !!(S.country || S.asn || S.region);

let reqSeq = 0;
async function refresh() {
  const seq = ++reqSeq;
  try {
    const [base, drill] = await Promise.all([
      api("/api/query", { method: "POST", body: JSON.stringify(baseQuery()) }),
      hasDrill() ? api("/api/query", { method: "POST", body: JSON.stringify(drillQuery()) }) : null,
    ]);
    if (seq !== reqSeq) return;
    S.base = base; S.drill = drill || base;
    if (!base.hasData) { showEmpty(true); return; }
    showEmpty(false);
    S.dataFrom = base.dataFrom; S.dataTo = base.dataTo;
    if (!S.initialized) { S.initialized = true; setRange("all", false); }
    render();
  } catch (e) {
    if (seq === reqSeq) showProgress("Fehler: " + e.message, 0, true);
  }
}

function showEmpty(empty) {
  $("#empty").hidden = !empty;
  $("#results").hidden = empty;
  if (empty) { S.initialized = false; S.base = S.drill = null; }
}

// ---------- Zeitbereich ----------
function parseInput(v, tz) { return v ? (tz === "utc" ? new Date(v + ":00Z") : new Date(v)) : null; }
function formatInput(d, tz) {
  const p = n => String(n).padStart(2, "0");
  return tz === "utc"
    ? d.toISOString().slice(0, 16)
    : `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
}
function setRange(q, doRefresh = true) {
  const end = parseInput(S.dataTo, S.tz);
  if (q === "all" || !end) { S.from = ""; S.to = ""; }
  else {
    const h = { "24h": 24, "7d": 168, "30d": 720 }[q];
    S.from = formatInput(new Date(end - h * 3600e3), S.tz); S.to = "";
  }
  $("#f-from").value = S.from || S.dataFrom;
  $("#f-to").value = S.to || S.dataTo;
  $$("#quick button").forEach(b => b.classList.toggle("on", b.dataset.q === q));
  if (doRefresh) refresh();
}
function onDateInput() {
  const f = $("#f-from").value, t = $("#f-to").value;
  S.from = f && f !== S.dataFrom ? f : "";
  S.to = t && t !== S.dataTo ? t : "";
  $$("#quick button").forEach(b => b.classList.toggle("on", b.dataset.q === "all" && !S.from && !S.to));
  refresh();
}

// ---------- Rendern ----------
function render() {
  const b = S.base, d = S.drill;
  if (document.activeElement !== $("#f-from")) $("#f-from").value = S.from || S.dataFrom;
  if (document.activeElement !== $("#f-to")) $("#f-to").value = S.to || S.dataTo;
  renderStatusChips(b.status);
  renderDrill();
  $("#k-ips").textContent = fmt(d.ips);
  $("#k-hits").textContent = fmt(d.hits);
  const realCountries = b.countries.filter(c => c.cc !== "PRIVATE" && c.cc !== "-");
  $("#k-countries").textContent = fmt(realCountries.length);
  const top = realCountries[0];
  $("#k-top").textContent = top ? `${top.name} (${pf.format(top.ips * 100 / Math.max(1, b.ips))} %)` : "–";
  $("#k-top").title = $("#k-top").textContent;
  const range = `${(S.from || S.dataFrom).replace("T", " ")} – ${(S.to || S.dataTo).replace("T", " ")} (${b.tzName})`;
  $("#countries-sub").textContent = `${fmt(b.ips)} eindeutige IPs · ${range}`;
  renderBars();
  renderMap();
  renderTimeline();
  renderTable();
}

function renderStatusChips(counts) {
  const el = $("#status-chips");
  el.innerHTML = STATUS.filter(([c]) => counts[c] || S.status.has(c) || c >= 2).map(([c, l]) =>
    `<button class="chip ${S.status.size === 0 || S.status.has(c) ? "on" : ""}" data-s="${c}" title="Klick: nur/auch ${l} · Alle aus = alle">${l} <span class="c">${fmt(counts[c] || 0)}</span></button>`).join("");
}

function renderDrill() {
  const parts = [];
  if (S.country) parts.push(["country", "Land", S.countryName || S.country]);
  if (S.asn) parts.push(["asn", "ISP", S.ispName || "AS" + S.asn]);
  if (S.region) parts.push(["region", "Region", S.region]);
  const el = $("#drill");
  el.hidden = !parts.length;
  el.innerHTML = parts.length ? `<span class="muted small">Gefiltert auf</span>` + parts.map(([k, l, v]) =>
    `<button class="chip" data-clear="${k}" title="Filter entfernen">${l}: <b>${esc(v)}</b> <span class="x">×</span></button>`).join("")
    + (parts.length > 1 ? `<button class="btn small ghost" data-clear="all">alle entfernen</button>` : "") : "";
}

function selectCountry(cc, name) {
  if (S.country === cc) { S.country = ""; S.countryName = ""; }
  else { S.country = cc; S.countryName = name; }
  S.region = ""; S.asn = 0; S.ispName = "";
  refresh();
}

function renderBars() {
  const b = S.base, rows = b.countries.slice(0, 15);
  const max = Math.max(1, ...rows.map(r => r.ips));
  const el = $("#bars");
  if (!rows.length) { el.innerHTML = `<div class="muted">Keine Anfragen im gewählten Zeitraum.</div>`; return; }
  el.innerHTML = `<div class="bars">` + rows.map((r, i) => {
    const sel = S.country === r.cc ? " sel" : "";
    return `<div class="name${sel}" data-i="${i}" title="${esc(r.name)}">${esc(r.name)}<span class="cc">${esc(r.cc.length === 2 ? r.cc : "")}</span></div>
      <div class="track${sel}" data-i="${i}"><div class="lane"><div class="fill" style="width:${(r.ips / max * 100).toFixed(2)}%"></div><span class="val">${fmt(r.ips)}</span></div></div>`;
  }).join("") + (b.countries.length > rows.length ? `<div class="more">+ ${fmt(b.countries.length - rows.length)} weitere Länder – siehe Tabelle „Alle Länder“</div>` : "") + `</div>`;
  $$(".bars [data-i]", el).forEach(n => {
    const r = rows[+n.dataset.i];
    n.addEventListener("mousemove", e => showTip(e, tipRows(r.name, [["IP-Adressen", fmt(r.ips)], ["Anteil", pf.format(r.ips * 100 / Math.max(1, b.ips)) + " %"], ["Anfragen", fmt(r.hits)]])));
    n.addEventListener("mouseleave", hideTip);
    n.addEventListener("click", () => selectCountry(r.cc, r.name));
  });
}

// Karte: logarithmische Klassen, eine Farbe hell -> dunkel
function mapBins(max) {
  const nice = v => { const p = Math.pow(10, Math.floor(Math.log10(v))); const m = v / p; return (m < 1.5 ? 1 : m < 3.5 ? 2 : m < 7.5 ? 5 : 10) * p; };
  if (max <= 6) return Array.from({ length: max }, (_, i) => i + 1);
  const out = [1];
  for (let k = 1; k < 6; k++) { const v = nice(Math.pow(max, k / 6)); if (v > out[out.length - 1] && v < max) out.push(v); }
  return out;
}
function renderMap() {
  const el = $("#map");
  if (!S.world) { el.innerHTML = `<div class="muted small">Karte wird geladen …</div>`; return; }
  if (!el.firstChild || el.firstChild.tagName !== "svg") {
    el.innerHTML = `<svg viewBox="0 0 ${S.world.w} ${S.world.h}" role="img" aria-label="Weltkarte der IP-Adressen nach Land">${S.world.c.map(c => `<path data-cc="${c.id}" d="${c.d}" fill-rule="evenodd"/>`).join("")}</svg>`;
    $$("path", el).forEach(p => {
      p.addEventListener("mousemove", e => {
        const cc = p.dataset.cc, r = S.base && S.base.countries.find(x => x.cc === cc);
        const name = r ? r.name : (S.world.c.find(c => c.id === cc) || {}).n;
        showTip(e, tipRows(name || cc, r ? [["IP-Adressen", fmt(r.ips)], ["Anfragen", fmt(r.hits)]] : [["IP-Adressen", "0"]]));
      });
      p.addEventListener("mouseleave", hideTip);
      p.addEventListener("click", () => {
        const cc = p.dataset.cc, r = S.base && S.base.countries.find(x => x.cc === cc);
        if (r) selectCountry(cc, r.name);
      });
    });
  }
  if (!S.base) return;
  const by = Object.fromEntries(S.base.countries.map(c => [c.cc, c]));
  const max = Math.max(1, ...S.base.countries.filter(c => c.cc.length === 2).map(c => c.ips));
  const bins = mapBins(max);
  const steps = ["--seq-1", "--seq-2", "--seq-3", "--seq-4", "--seq-5", "--seq-6"].slice(6 - bins.length);
  $$("path", el).forEach(p => {
    const r = by[p.dataset.cc];
    let k = -1;
    if (r && r.ips > 0) { k = 0; while (k + 1 < bins.length && r.ips >= bins[k + 1]) k++; }
    p.style.fill = k < 0 ? "" : `var(${steps[k]})`;
    p.classList.toggle("sel", S.country === p.dataset.cc);
  });
  $("#map-legend").innerHTML = bins.map((v, i) => {
    const hi = bins[i + 1] ? bins[i + 1] - 1 : max;
    const label = v >= hi ? fmt(v) : `${fmt(v)}–${fmt(hi)}`;
    return `<span><span class="sw" style="background:var(${steps[i]})"></span>${label}</span>`;
  }).join("") + `<span><span class="sw" style="background:var(--no-data)"></span>keine</span>`;
}

function renderTimeline() {
  const d = S.drill, pts = d.timeline || [], mode = S.tlMode;
  const el = $("#timeline");
  const what = mode === "ips" ? "Eindeutige IP-Adressen" : "Anfragen";
  $("#tl-title").textContent = `${what} pro ${d.unit === "hour" ? "Stunde" : "Tag"}`;
  $("#tl-sub").textContent = (hasDrill() ? `${S.countryName || S.ispName || S.region} · ` : "") + d.tzName;
  if (!pts.length) { el.innerHTML = `<div class="muted">Keine Daten.</div>`; return; }
  const W = Math.max(320, el.clientWidth || 800), H = 240, m = { l: 56, r: 12, t: 12, b: 26 };
  const iw = W - m.l - m.r, ih = H - m.t - m.b;
  const vals = pts.map(p => p[mode]);
  const niceMax = v => { if (v <= 5) return 5; const p = Math.pow(10, Math.floor(Math.log10(v))); const s = [1, 2, 2.5, 5, 10].find(s => s * p >= v); return s * p; };
  const ymax = niceMax(Math.max(...vals));
  const x = i => m.l + (pts.length === 1 ? iw / 2 : i * iw / (pts.length - 1));
  const y = v => m.t + ih - v / ymax * ih;
  let g = "";
  for (let k = 0; k <= 4; k++) {
    const v = ymax * k / 4;
    g += `<line class="gridline" x1="${m.l}" x2="${W - m.r}" y1="${y(v)}" y2="${y(v)}"/><text x="${m.l - 8}" y="${y(v) + 4}" text-anchor="end">${fmt(v)}</text>`;
  }
  const labelEvery = Math.max(1, Math.ceil(pts.length / Math.floor(iw / 80)));
  const lab = t => d.unit === "hour" ? t.slice(5, 16).replace("-", ".").replace(/^(\d\d)\.(\d\d)/, "$2.$1.") : t.slice(8, 10) + "." + t.slice(5, 7) + "." + t.slice(2, 4);
  const columns = pts.length <= 45;
  const bw = columns ? Math.min(24, iw / pts.length * 0.7) : 0;
  const xc = i => columns ? m.l + (i + 0.5) * iw / pts.length : x(i);
  pts.forEach((p, i) => { if (i % labelEvery === 0) g += `<text x="${xc(i)}" y="${H - 6}" text-anchor="middle">${lab(p.t)}</text>`; });
  let marks = "";
  if (columns) {
    marks = pts.map((p, i) => {
      const v = p[mode]; if (!v) return "";
      const h = Math.max(1, ih - (y(v) - m.t)), x0 = xc(i) - bw / 2, y0 = m.t + ih - h, r = Math.min(4, bw / 2, h);
      return `<path class="col" d="M${x0} ${m.t + ih}V${y0 + r}Q${x0} ${y0} ${x0 + r} ${y0}H${x0 + bw - r}Q${x0 + bw} ${y0} ${x0 + bw} ${y0 + r}V${m.t + ih}Z"/>`;
    }).join("");
  } else {
    const line = pts.map((p, i) => `${i ? "L" : "M"}${x(i).toFixed(1)} ${y(p[mode]).toFixed(1)}`).join("");
    marks = `<path class="area" d="${line}L${x(pts.length - 1)} ${m.t + ih}L${x(0)} ${m.t + ih}Z"/><path class="line" d="${line}"/>`;
  }
  el.innerHTML = `<svg viewBox="0 0 ${W} ${H}" role="img" aria-label="${what} im Zeitverlauf"><g class="axis">${g}</g>${marks}<line class="cross" id="tl-cross" y1="${m.t}" y2="${m.t + ih}" visibility="hidden"/><circle class="dot" id="tl-dot" r="4.5" visibility="hidden"/><rect x="${m.l}" y="${m.t}" width="${iw}" height="${ih}" fill="transparent" id="tl-hit"/></svg>`;
  const svg = $("svg", el), cross = $("#tl-cross", el), dot = $("#tl-dot", el);
  $("#tl-hit", el).addEventListener("mousemove", e => {
    const r = svg.getBoundingClientRect(), px = (e.clientX - r.left) * W / r.width;
    const i = Math.max(0, Math.min(pts.length - 1, columns ? Math.floor((px - m.l) / (iw / pts.length)) : Math.round((px - m.l) / iw * (pts.length - 1))));
    const p = pts[i];
    cross.setAttribute("x1", xc(i)); cross.setAttribute("x2", xc(i)); cross.setAttribute("visibility", "visible");
    if (!columns) { dot.setAttribute("cx", x(i)); dot.setAttribute("cy", y(p[mode])); dot.setAttribute("visibility", "visible"); }
    const t = d.unit === "hour" ? p.t.replace(/^(\d{4})-(\d\d)-(\d\d)/, "$3.$2.$1") + " Uhr" : p.t.replace(/^(\d{4})-(\d\d)-(\d\d)/, "$3.$2.$1");
    showTip(e, tipRows(t, [["Eindeutige IPs", fmt(p.ips)], ["Anfragen", fmt(p.hits)]]));
  });
  $("#tl-hit", el).addEventListener("mouseleave", () => { hideTip(); cross.setAttribute("visibility", "hidden"); dot.setAttribute("visibility", "hidden"); });
}

// ---------- Tabellen ----------
const share = (n, t) => `<span class="share"><i style="width:${(n / Math.max(1, t) * 100).toFixed(1)}%"></i></span>`;
const TABLES = {
  countries: {
    rows: () => S.base.countries,
    note: () => `Rangliste nach Anzahl eindeutiger IP-Adressen${S.excludePrivate ? "" : " (inkl. interner IPs)"}. Klick auf eine Zeile filtert alle Ansichten auf dieses Land.`,
    cols: [
      { k: "rank", l: "#", num: true, v: (r, i) => i + 1, nosort: true },
      { k: "name", l: "Land", v: r => r.name, h: r => `${esc(r.name)}${r.cc.length === 2 ? `<span class="tag">${esc(r.cc)}</span>` : ""}` },
      { k: "ips", l: "IP-Adressen", num: true, v: r => r.ips, h: r => fmt(r.ips) },
      { k: "share", l: "Anteil", num: true, v: r => r.ips, h: r => pf.format(r.ips * 100 / Math.max(1, S.base.ips)) + " %" + share(r.ips, S.base.ips) },
      { k: "hits", l: "Anfragen", num: true, v: r => r.hits, h: r => fmt(r.hits) },
      { k: "hpi", l: "Anfragen/IP", num: true, v: r => r.hits / r.ips, h: r => pf.format(r.hits / r.ips) },
    ],
    click: r => selectCountry(r.cc, r.name), sel: r => r.cc === S.country,
    search: r => r.name + " " + r.cc,
  },
  isps: {
    rows: () => S.drill.isps,
    note: () => `Provider / Organisation laut ASN-Datenbank${S.country ? " – nur " + S.countryName : ""}. Klick filtert auf diesen Provider.`,
    cols: [
      { k: "org", l: "ISP / Organisation", v: r => r.org, h: r => esc(r.org) },
      { k: "asn", l: "ASN", num: true, v: r => r.asn, h: r => r.asn ? "AS" + r.asn : "–" },
      { k: "cc", l: "Land", v: r => r.cc, h: r => esc(r.cc) },
      { k: "ips", l: "IP-Adressen", num: true, v: r => r.ips, h: r => fmt(r.ips) },
      { k: "share", l: "Anteil", num: true, v: r => r.ips, h: r => pf.format(r.ips * 100 / Math.max(1, S.drill.ips)) + " %" + share(r.ips, S.drill.ips) },
      { k: "hits", l: "Anfragen", num: true, v: r => r.hits, h: r => fmt(r.hits) },
    ],
    click: r => { if (S.asn === r.asn) { S.asn = 0; S.ispName = ""; } else { S.asn = r.asn; S.ispName = r.org; } refresh(); },
    sel: r => r.asn === S.asn && S.asn !== 0,
    search: r => r.org + " AS" + r.asn + " " + r.cc,
  },
  regions: {
    rows: () => S.drill.regions,
    note: () => `Regionen (Kanton / Bundesland / Provinz) laut DB-IP City Lite${S.country ? " – nur " + S.countryName : ""}.`,
    cols: [
      { k: "region", l: "Region", v: r => r.region, h: r => esc(r.region) },
      { k: "cc", l: "Land", v: r => r.cc, h: r => esc(r.cc) },
      { k: "ips", l: "IP-Adressen", num: true, v: r => r.ips, h: r => fmt(r.ips) },
      { k: "share", l: "Anteil", num: true, v: r => r.ips, h: r => pf.format(r.ips * 100 / Math.max(1, S.drill.ips)) + " %" + share(r.ips, S.drill.ips) },
      { k: "hits", l: "Anfragen", num: true, v: r => r.hits, h: r => fmt(r.hits) },
    ],
    click: r => { S.region = S.region === r.region ? "" : r.region; refresh(); }, sel: r => r.region === S.region && !!S.region,
    search: r => r.region + " " + r.cc,
  },
  ips: {
    rows: () => S.drill.ipRows,
    note: () => S.drill.ipTotal > S.drill.ipRows.length
      ? `Zeigt die ${fmt(S.drill.ipRows.length)} IP-Adressen mit den meisten Anfragen von ${fmt(S.drill.ipTotal)}. Der CSV-Export enthält alle.`
      : `${fmt(S.drill.ipTotal)} IP-Adressen. Erste/letzte Anfrage innerhalb des Filters (${S.drill.tzName}).`,
    cols: [
      { k: "ip", l: "IP-Adresse", v: r => r.ip, h: r => esc(r.ip), cls: "mono" },
      { k: "country", l: "Land", v: r => r.private ? "Privat" : r.country, h: r => r.private ? `<span class="muted">Privat / intern</span>` : `${esc(r.country || "Unbekannt")}${r.cc ? `<span class="tag">${esc(r.cc)}</span>` : ""}` },
      { k: "org", l: "ISP / Organisation", v: r => r.org, h: r => esc(r.org) + (r.asn ? `<span class="tag">AS${r.asn}</span>` : "") },
      { k: "hits", l: "Anfragen", num: true, v: r => r.hits, h: r => fmt(r.hits) },
      { k: "first", l: "Erste", v: r => r.first.replace("≈ ", ""), h: r => esc(r.first), cls: "nowrap", title: r => r.first.startsWith("≈") ? "stundengenau (frühere/spätere Anfragen liegen ausserhalb des Filters)" : "" },
      { k: "last", l: "Letzte", v: r => r.last.replace("≈ ", ""), h: r => esc(r.last), cls: "nowrap", title: r => r.last.startsWith("≈") ? "stundengenau (frühere/spätere Anfragen liegen ausserhalb des Filters)" : "" },
      { k: "ua", l: "User-Agent (erster)", v: r => r.ua, h: r => esc(r.ua), cls: "ua", title: r => r.ua },
    ],
    search: r => [r.ip, r.country, r.cc, r.region, r.city, r.org, "AS" + r.asn, r.ua].join(" "),
  },
};

function renderTable() {
  const def = TABLES[S.tab];
  if (S.tab === "ips") TABLES.ips.cols = buildIpCols();
  const cols = def.cols;
  const q = S.search.trim().toLowerCase();
  let rows = def.rows() || [];
  const base = rows;
  if (q) rows = rows.filter(r => def.search(r).toLowerCase().includes(q));
  const so = S.sort[S.tab];
  if (so) {
    const c = cols.find(c => c.k === so.k);
    if (c) {
      const idx = new Map(base.map((r, i) => [r, i]));
      rows = rows.slice().sort((a, b) => {
        const va = c.v(a, idx.get(a)), vb = c.v(b, idx.get(b));
        const r = typeof va === "number" && typeof vb === "number" ? va - vb : String(va).localeCompare(String(vb), "de");
        return so.dir * r;
      });
    }
  }
  $("#t-note").textContent = def.note();
  $("#t-table thead").innerHTML = "<tr>" + cols.map(c => `<th class="${c.num ? "num" : ""}" data-k="${c.k}">${esc(c.l)}${so && so.k === c.k ? `<span class="arr">${so.dir > 0 ? "▲" : "▼"}</span>` : ""}</th>`).join("") + "</tr>";
  const shown = rows.slice(0, 2000);
  const idx = new Map(base.map((r, i) => [r, i]));
  $("#t-table tbody").innerHTML = shown.length ? shown.map((r, i) => `<tr class="${def.click ? "click" : ""}" data-i="${i}" ${def.sel && def.sel(r) ? 'style="font-weight:600"' : ""}>` + cols.map(c =>
    `<td class="${c.num ? "num " : ""}${c.cls || ""}" ${c.title ? `title="${esc(c.title(r))}"` : ""}>${c.h ? c.h(r, idx.get(r)) : esc(c.v(r, idx.get(r)))}</td>`).join("") + "</tr>").join("")
    : `<tr><td colspan="${cols.length}" class="muted">Keine Einträge.</td></tr>`;
  if (def.click) $$("#t-table tbody tr[data-i]").forEach(tr => tr.addEventListener("click", () => def.click(shown[+tr.dataset.i])));
}
function buildIpCols() {
  const cols = TABLES.ips.cols.filter(c => c.k !== "region");
  if (S.hasCity) cols.splice(2, 0, { k: "region", l: "Region / Stadt", v: r => r.region + r.city, h: r => esc([r.region, r.city].filter(Boolean).join(" · ")) });
  return cols;
}

function exportCSV(type) {
  const q = type === "countries" ? baseQuery() : drillQuery();
  const u = `/api/export?type=${type}&t=${window.TOKEN}&q=${encodeURIComponent(JSON.stringify(q))}`;
  const a = document.createElement("a"); a.href = u; a.download = ""; document.body.appendChild(a); a.click(); a.remove();
}

// ---------- Events ----------
function bind() {
  const input = $("#file-input");
  input.addEventListener("change", () => { uploadFiles(input.files); input.value = ""; });
  const drop = $("#drop");
  ["dragenter", "dragover"].forEach(ev => document.addEventListener(ev, e => { e.preventDefault(); drop.classList.add("over"); }));
  ["dragleave", "drop"].forEach(ev => document.addEventListener(ev, e => { if (ev === "drop" || e.target === document.documentElement) drop.classList.remove("over"); }));
  document.addEventListener("drop", e => { e.preventDefault(); drop.classList.remove("over"); if (e.dataTransfer && e.dataTransfer.files.length) uploadFiles(e.dataTransfer.files); });
  $("#btn-path").addEventListener("click", loadPaths);
  $("#path-input").addEventListener("keydown", e => { if (e.key === "Enter") loadPaths(); });
  $("#opt-xff").addEventListener("change", e => store.set("xff", e.target.checked));

  $("#btn-reset").addEventListener("click", async () => {
    if (!confirm("Alle geladenen Logdaten verwerfen?")) return;
    await api("/api/reset", { method: "POST" });
    Object.assign(S, { country: "", countryName: "", asn: 0, ispName: "", region: "", from: "", to: "" });
    renderFiles([]); showEmpty(true);
  });
  $("#btn-quit").addEventListener("click", async () => {
    if (!confirm("iis-geo beenden?")) return;
    try { await api("/api/quit", { method: "POST" }); } catch { }
    document.body.innerHTML = `<main><section class="card empty"><h2>iis-geo wurde beendet.</h2><p class="muted">Dieses Fenster kann geschlossen werden.</p></section></main>`;
  });

  $("#f-from").addEventListener("change", onDateInput);
  $("#f-to").addEventListener("change", onDateInput);
  $("#quick").addEventListener("click", e => { const b = e.target.closest("button"); if (b) setRange(b.dataset.q); });
  $("#f-tz").addEventListener("change", e => {
    const old = S.tz, tz = e.target.value;
    const conv = v => v ? formatInput(parseInput(v, old), tz) : "";
    S.from = conv(S.from); S.to = conv(S.to); S.tz = tz; store.set("tz", tz);
    S.initialized = !!(S.from || S.to);
    refresh().then(() => { $("#f-from").value = S.from || S.dataFrom; $("#f-to").value = S.to || S.dataTo; });
  });
  $("#f-private").addEventListener("change", e => { S.excludePrivate = e.target.checked; store.set("excludePrivate", S.excludePrivate); refresh(); });
  $("#status-chips").addEventListener("click", e => {
    const b = e.target.closest("[data-s]"); if (!b) return;
    const c = +b.dataset.s;
    const all = STATUS.map(s => s[0]);
    if (S.status.size === 0) { S.status = new Set([c]); }          // erster Klick: nur diese Klasse
    else if (S.status.has(c)) { S.status.delete(c); }
    else { S.status.add(c); }
    if (S.status.size === all.length) S.status.clear();
    refresh();
  });
  $("#drill").addEventListener("click", e => {
    const b = e.target.closest("[data-clear]"); if (!b) return;
    const k = b.dataset.clear;
    if (k === "country" || k === "all") { S.country = ""; S.countryName = ""; }
    if (k === "asn" || k === "all") { S.asn = 0; S.ispName = ""; }
    if (k === "region" || k === "all") S.region = "";
    refresh();
  });
  $("#tl-mode").addEventListener("click", e => {
    const b = e.target.closest("button"); if (!b) return;
    S.tlMode = b.dataset.m; $$("#tl-mode button").forEach(x => x.classList.toggle("on", x === b)); renderTimeline();
  });
  $$(".tabs [role=tab]").forEach(t => t.addEventListener("click", () => {
    if (t.disabled) return;
    S.tab = t.dataset.tab; $$(".tabs [role=tab]").forEach(x => x.classList.toggle("on", x === t)); renderTable();
  }));
  $("#t-search").addEventListener("input", e => { S.search = e.target.value; renderTable(); });
  $("#t-table thead").addEventListener("click", e => {
    const th = e.target.closest("th"); if (!th) return;
    const c = TABLES[S.tab].cols.find(c => c.k === th.dataset.k); if (!c || c.nosort) return;
    const cur = S.sort[S.tab];
    S.sort[S.tab] = cur && cur.k === c.k ? { k: c.k, dir: -cur.dir } : { k: c.k, dir: c.num ? -1 : 1 };
    renderTable();
  });
  $("#t-export").addEventListener("click", () => exportCSV(S.tab));
  $$("[data-export]").forEach(b => b.addEventListener("click", () => exportCSV(b.dataset.export)));
  let rt; addEventListener("resize", () => { clearTimeout(rt); rt = setTimeout(() => { if (S.drill) renderTimeline(); }, 150); });
  matchMedia("(prefers-color-scheme: dark)").addEventListener?.("change", () => { if (S.base) render(); });
}

init();
})();
