// Parses the newest log in docs/startup-log/, which scripts/startup-bench
// writes through .github/workflows/startup-bench.yml.

export function parseStartupLog(path, text) {
  const file = path.slice(path.lastIndexOf("/") + 1);
  const rows = [];
  for (const line of text.split("\n")) {
    const c = line.split("|").map((s) => s.trim());
    if (c.length !== 7 || c[1] === "language" || c[1].startsWith("---")) continue;
    rows.push({
      language: c[1],
      version: c[2],
      mean: Number(c[3]),
      stddev: Number(c[4]),
      bytes: c[5] === "interpreter" ? null : Number(c[5]),
    });
  }
  if (rows.length === 0) throw new Error(`${file}: no table rows`);
  const head = text.split("\n").find((l) => l.startsWith("startup-bench:")) ?? "";
  // The title is "# Startup bench, DATE, PLATFORM".
  const title = text.split("\n")[0];
  const platform = title.slice(title.lastIndexOf(",") + 1).trim();
  return { file, date: file.slice(0, 10), platform, method: head.replace(/^startup-bench:\s*/, ""), rows };
}

// The newest log, by the date its name starts with, or null when there is none.
export function loadStartup(logs) {
  const paths = Object.keys(logs).sort();
  if (paths.length === 0) return null;
  const newest = paths[paths.length - 1];
  return parseStartupLog(newest, logs[newest]);
}
