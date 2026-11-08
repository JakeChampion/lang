import fs from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";

export function liveTests(pkg, names) {
  const defined = new Set();
  for (const file of fs.readdirSync(pkg)) {
    if (!file.endsWith("_test.go")) continue;
    const source = fs.readFileSync(path.join(pkg, file), "utf8");
    for (const match of source.matchAll(/^func\s+(Test[A-Za-z0-9_]+)\s*\(/gm)) defined.add(match[1]);
  }
  for (const name of names) {
    if (!defined.has(name)) throw new Error(`The consumer test ${pkg}.${name} does not exist.`);
  }
  const listing = spawnSync("go", ["test", "-list", "^Test", `./${pkg}/`], { encoding: "utf8", maxBuffer: 16 * 1024 * 1024 });
  if (listing.status !== 0) throw new Error(listing.stderr || "Cannot list consumer tests.");
  const available = new Set(listing.stdout.split("\n"));
  const selected = names.filter((name) => available.has(name));
  if (selected.length !== names.length) console.error(`${pkg}: ${names.length - selected.length} tests do not compile for this host.`);
  return selected;
}
