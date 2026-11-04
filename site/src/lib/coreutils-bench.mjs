// Parses the newest coreutils bench log (docs/coreutils-log/*-bench.md) for
// the coreutils page. `logs` maps each log's path to its text.

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

export function loadBench(logs) {
  const path = Object.keys(logs)
    .filter((p) => /\/\d{4}-\d{2}-\d{2}-bench\.md$/.test(p))
    .sort()
    .at(-1);
  if (!path) throw new Error("no docs/coreutils-log/*-bench.md");

  const file = path.slice(path.lastIndexOf("/") + 1);
  const text = logs[path];
  // Columns are found by name: the script drops the uutils columns when it
  // finds no uutils.
  const lines = text.split("\n");
  const head = lines.find((l) => l.startsWith("| utility |"));
  if (!head) throw new Error(`${file}: no table header`);
  const col = Object.fromEntries(cells(head).map((name, i) => [name, i]));
  for (const name of ["utility", "workload", "fern (ms)", "gnu (ms)", "gnu / fern"]) {
    if (!(name in col)) throw new Error(`${file}: no "${name}" column`);
  }
  const hasUutils = "uutils (ms)" in col && "uutils / fern" in col;

  const rows = [];
  for (const line of lines) {
    if (!line.startsWith("| `")) continue;
    const c = cells(line);
    if (c.length !== Object.keys(col).length) throw new Error(`${file}: bad row: ${line}`);
    rows.push({
      utility: c[col["utility"]].replace(/`/g, ""),
      workload: c[col["workload"]],
      fern: ms(c[col["fern (ms)"]]),
      gnu: ms(c[col["gnu (ms)"]]),
      uutils: hasUutils ? ms(c[col["uutils (ms)"]]) : null,
      vsGnu: ratio(c[col["gnu / fern"]]),
      vsUutils: hasUutils ? ratio(c[col["uutils / fern"]]) : null,
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
    // The script writes "(GNU coreutils) 9.12" and "uutils 0.0.24"; a log
    // written by hand says "GNU coreutils 9.12".
    gnuVersion: /GNU coreutils\)? (\d+(?:\.\d+)+)/.exec(text)?.[1] ?? null,
    uutilsVersion: hasUutils ? (/uutils (\d+(?:\.\d+)+)/.exec(text)?.[1] ?? null) : null,
    hasUutils,
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
      medianUutils: u.length ? median(u) : null,
      everyRowBeatsGnu: utilities.filter((t) => t.winsGnu === t.rows.length).length,
    },
  };
}
