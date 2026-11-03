// Reads the newest coreutils bench log (docs/coreutils-log/*-bench.md) for the
// coreutils page, so the page and the doc cannot disagree.

const logs = import.meta.glob("../../../docs/coreutils-log/*-bench.md", {
  query: "?raw",
  import: "default",
  eager: true,
});

// Splits a Markdown table row on unescaped pipes; `\|` stays in the cell.
function cells(line) {
  return line
    .slice(1, -1)
    .split(/(?<!\\)\|/)
    .map((c) => c.trim().replace(/\\\|/g, "|"));
}

// "307.97 ± 61.88" → 307.97. A failed run ("exit 1") → null.
function ms(cell) {
  const m = /^([0-9.]+) ±/.exec(cell);
  return m ? Number(m[1]) : null;
}

function ratio(cell) {
  const m = /^([0-9.]+)×$/.exec(cell);
  return m ? Number(m[1]) : null;
}

function median(xs) {
  const s = [...xs].sort((a, b) => a - b);
  const mid = s.length >> 1;
  return s.length % 2 ? s[mid] : (s[mid - 1] + s[mid]) / 2;
}

export function loadBench() {
  const path = Object.keys(logs)
    .filter((p) => /\/\d{4}-\d{2}-\d{2}-bench\.md$/.test(p))
    .sort()
    .at(-1);
  if (!path) throw new Error("no docs/coreutils-log/*-bench.md");

  const file = path.slice(path.lastIndexOf("/") + 1);
  const text = logs[path];
  const rows = [];
  for (const line of text.split("\n")) {
    if (!line.startsWith("| `")) continue;
    const c = cells(line);
    if (c.length !== 7) throw new Error(`${file}: bad row: ${line}`);
    rows.push({
      utility: c[0].replace(/`/g, ""),
      workload: c[1],
      fern: ms(c[2]),
      gnu: ms(c[3]),
      uutils: ms(c[4]),
      vsGnu: ratio(c[5]),
      vsUutils: ratio(c[6]),
    });
  }

  const byUtility = new Map();
  for (const r of rows) {
    if (!byUtility.has(r.utility)) byUtility.set(r.utility, []);
    byUtility.get(r.utility).push(r);
  }
  const utilities = [...byUtility].map(([name, rs]) => {
    const g = rs.map((r) => r.vsGnu).filter((x) => x !== null);
    const u = rs.map((r) => r.vsUutils).filter((x) => x !== null);
    return {
      name,
      rows: rs,
      medianGnu: g.length ? median(g) : null,
      medianUutils: u.length ? median(u) : null,
      worstGnu: g.length ? Math.min(...g) : null,
      winsGnu: g.filter((x) => x > 1).length,
      winsUutils: u.filter((x) => x > 1).length,
      comparedUutils: u.length,
    };
  });

  const g = rows.map((r) => r.vsGnu).filter((x) => x !== null);
  const u = rows.map((r) => r.vsUutils).filter((x) => x !== null);
  return {
    file,
    date: file.slice(0, 10),
    rows,
    utilities,
    summary: {
      rows: rows.length,
      utilities: utilities.length,
      fasterThanGnu: g.filter((x) => x > 1).length,
      comparedGnu: g.length,
      fasterThanUutils: u.filter((x) => x > 1).length,
      comparedUutils: u.length,
      medianGnu: median(g),
      medianUutils: median(u),
      everyRowBeatsGnu: utilities.filter((t) => t.winsGnu === t.rows.length).length,
    },
  };
}
