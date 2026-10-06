// Erzeugt internal/web/static/world.json (vorprojizierte SVG-Pfade, Equal-Earth-Projektion)
// aus world-atlas (countries-110m.json, ISC/Natural Earth) und i18n-iso-countries (codes.json).
//
//   npm pack world-atlas@2 i18n-iso-countries   (und entpacken)
//   node tools/gen-world.js <countries-110m.json> <codes.json> > internal/web/static/world.json
const fs = require("fs");
const topo = JSON.parse(fs.readFileSync(process.argv[2], "utf8"));
const codes = JSON.parse(fs.readFileSync(process.argv[3], "utf8"));

const num2a2 = {};
for (const [a2, , num] of codes) num2a2[num] = a2;
const byName = { "Kosovo": "XK", "N. Cyprus": "CY", "Somaliland": "SO" };

const [sx, sy] = topo.transform.scale, [tx, ty] = topo.transform.translate;
const arcs = topo.arcs.map(arc => {
  let x = 0, y = 0;
  return arc.map(([dx, dy]) => { x += dx; y += dy; return [x * sx + tx, y * sy + ty]; });
});

// Equal Earth
const A1 = 1.340264, A2 = -0.081106, A3 = 0.000893, A4 = 0.003796, M = Math.sqrt(3) / 2;
function proj([lon, lat]) {
  const l = lon * Math.PI / 180, p = lat * Math.PI / 180;
  const t = Math.asin(M * Math.sin(p)), t2 = t * t, t6 = t2 * t2 * t2;
  const x = l * Math.cos(t) / (M * (A1 + 3 * A2 * t2 + t6 * (7 * A3 + 9 * A4 * t2)));
  const y = t * (A1 + A2 * t2 + t6 * (A3 + A4 * t2));
  return [x, y];
}
const SCALE = 150, W = 2 * 2.7066297 * SCALE, H = 2 * 1.3173627 * SCALE;
function pt(c) { const [x, y] = proj(c); return [(x * SCALE + W / 2), (H / 2 - y * SCALE)]; }

function ring(idxs) {
  const pts = [];
  for (const i of idxs) {
    let a = i >= 0 ? arcs[i] : arcs[~i].slice().reverse();
    pts.push(...(pts.length ? a.slice(1) : a));
  }
  // Ringe über die Datumsgrenze (Russland, Fidschi) auf eine Seite legen; der Rest wird vom SVG abgeschnitten.
  if (pts.some((c, k) => k && Math.abs(c[0] - pts[k - 1][0]) > 180)) {
    for (let k = 0; k < pts.length; k++) if (pts[k][0] < 0) pts[k] = [pts[k][0] + 360, pts[k][1]];
  }
  let d = "", lx = null, ly = null;
  for (const c of pts) {
    const [x, y] = pt(c).map(v => Math.round(v * 10) / 10);
    if (x === lx && y === ly) continue;
    d += (d ? "L" : "M") + x + " " + y; lx = x; ly = y;
  }
  return d + "Z";
}

const out = [];
for (const g of topo.objects.countries.geometries) {
  const id = num2a2[g.id] || byName[g.properties.name];
  if (!id || id === "AQ") continue;
  const polys = g.type === "Polygon" ? [g.arcs] : g.arcs;
  const d = polys.map(p => p.map(ring).join("")).join("");
  const prev = out.find(o => o.id === id);
  if (prev) prev.d += d; else out.push({ id, n: g.properties.name, d });
}
process.stdout.write(JSON.stringify({ w: Math.round(W), h: Math.round(H), c: out }));
