// Run the compiler at build time and record what it prints, for the home
// page: the assembly and the executable size of src/captures/area.fern, the
// usage text of `fern -help`, the capabilities that `fern -lsp` reports, and
// the diagnostics for the programs in src/captures/diagnostics/. The output
// is src/generated/captures.json.
//
// `npm run build` runs this first. The compiler is $FERN_SELFHOST, or
// ../bin/fern-selfhost, which every build recipe makes before the site build.

import { execFileSync, spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const site = fileURLToPath(new URL("..", import.meta.url));
const repo = join(site, "..");
const fern = process.env.FERN_SELFHOST ?? join(repo, "bin/fern-selfhost");
const stdlib = join(repo, "internal/stdlib");
const source = join(site, "src/captures/area.fern");
const targets = ["arm64-linux", "x86-64-linux"];

function run(args, input) {
  return execFileSync(fern, args, { input, encoding: "utf8", maxBuffer: 64 << 20 });
}

// The `main` function from the assembly listing, without the CFI and literal
// pool directives. It ends at the next top-level label.
export function mainFunction(asm) {
  const out = [];
  let inside = false;
  for (const line of asm.split("\n")) {
    const label = /^([A-Za-z_][\w.]*):$/.exec(line);
    if (label) {
      if (inside && !label[1].startsWith("__fn_main")) break;
      if (label[1] === "__fn_main") inside = true;
    }
    if (inside && !/^\s*\.(cfi_\w+|ltorg)\b/.test(line)) out.push(line);
  }
  return out.join("\n");
}

// Each capability key the language server reports in its initialize result.
function lspCapabilities() {
  const msgs = [
    { id: 1, method: "initialize", params: {} },
    { id: 2, method: "shutdown" },
    { method: "exit" },
  ];
  const input = msgs.map((m) => {
    const body = JSON.stringify({ jsonrpc: "2.0", ...m });
    return `Content-Length: ${Buffer.byteLength(body)}\r\n\r\n${body}`;
  }).join("");
  const out = run(["-lsp", stdlib], input);
  for (const part of out.split(/Content-Length: \d+\r\n\r\n/)) {
    if (!part.trim()) continue;
    const msg = JSON.parse(part);
    if (msg.id === 1) return Object.keys(msg.result.capabilities);
  }
  throw new Error("fern -lsp gave no initialize result");
}

// What `fern -check` prints for each program in src/captures/diagnostics/.
// It runs in that directory, so the output names the file and not a
// temporary path. The home page says that each program fails, and that the
// compiler suggests the fix for the typo.
function diagnostics() {
  const dir = join(site, "src/captures/diagnostics");
  return readdirSync(dir).filter((f) => f.endsWith(".fern")).sort().map((file) => {
    const res = spawnSync(fern, ["-check", file, stdlib], { cwd: dir, encoding: "utf8" });
    const output = (res.stdout + res.stderr).trimEnd();
    if (res.status === 0 || !output.includes("error[")) {
      throw new Error(`capture: ${file} no longer fails to check; update the home page`);
    }
    if (file === "typo.fern" && !output.includes("help:")) {
      throw new Error("capture: the typo no longer gets a help line; update the home page");
    }
    return { file, code: readFileSync(join(dir, file), "utf8"), output };
  });
}

function capture() {
  const work = mkdtempSync(join(tmpdir(), "fern-capture-"));
  const code = readFileSync(source, "utf8");
  const compiled = targets.map((target) => {
    const asm = run(["-target", target, "-O", source, stdlib]);
    const exe = join(work, target);
    run(["-target", target, "-O", "-o", exe, source, stdlib]);
    const main = mainFunction(asm);
    // The home page says that the compiler folds area(Circle(5)) to 75.
    if (!/[#$]75\b/.test(main)) {
      throw new Error(`capture: main for ${target} no longer loads the folded constant 75; update the home page`);
    }
    return { target, asm: main, bytes: statSync(exe).size };
  });
  const help = run(["-help"]);
  return {
    revision: execFileSync("git", ["rev-parse", "--short=12", "HEAD"], { cwd: repo, encoding: "utf8" }).trim(),
    source: { path: "site/src/captures/area.fern", code },
    compiled,
    usage: help.slice(0, help.indexOf("Flags go before FILE")).trimEnd(),
    lsp: lspCapabilities(),
    diagnostics: diagnostics(),
  };
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const out = join(site, "src/generated");
  mkdirSync(out, { recursive: true });
  writeFileSync(join(out, "captures.json"), JSON.stringify(capture(), null, 2) + "\n");
  console.log(`capture: wrote src/generated/captures.json with ${fern}`);
}
