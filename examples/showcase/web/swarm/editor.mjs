// Compiles an edited Swarm kernel in the browser. The Fern compiler runs as
// WebAssembly in a worker, and the new module replaces the running kernel.
import { $, status } from "/assets/ui.mjs";

const files = new URL("./compiler/", import.meta.url);
const provided = new Set([
  "wasi_snapshot_preview1.proc_exit",
  "wasi_snapshot_preview1.fd_write",
]);
const required = [
  "fern:showcase/swarm#particle",
  "fern:showcase/swarm#sample",
];

// use(buffer) installs a kernel module; restore() returns to the original.
export function mountEditor({ use, restore }) {
  let compiler, worker, original;
  let sequence = 0;
  const replies = new Map();
  const report = (text, live = false, error = false) =>
    status("compile-status", text, live, error);
  const diagnostics = (text) => {
    $("diagnostics").hidden = !text;
    $("diagnostics").textContent = text;
  };

  async function text(name) {
    const response = await fetch(new URL(name, files));
    if (!response.ok) throw Error(`${name} is not available (${response.status})`);
    return response.text();
  }

  async function open() {
    $("editor-open").disabled = true;
    report("Load the Fern compiler");
    try {
      [original, compiler] = await Promise.all([
        text("kernel.fern"),
        WebAssembly.compileStreaming(fetch(new URL("playground.wasm", files))),
      ]);
      worker = new Worker(new URL("driver-worker.js", files), { type: "module" });
      worker.onmessage = ({ data }) => {
        replies.get(data.id)?.(data);
        replies.delete(data.id);
      };
      $("kernel-source").value = original;
      $("editor").hidden = false;
      $("editor-open").hidden = true;
      $("kernel-source").focus();
      report("Compiler ready", true);
    } catch (error) {
      $("editor-open").disabled = false;
      report(error.message, false, true);
    }
  }

  function compile(src) {
    const id = ++sequence;
    return new Promise((resolve) => {
      replies.set(id, resolve);
      worker.postMessage({
        id,
        module: compiler,
        src,
        args: ["fern", "-emit", "core-module"],
      });
    });
  }

  // A diagnostic starts with line:column. Select that line in the editor.
  function point(stderr) {
    const at = /^(\d+):\d+:/m.exec(stderr);
    if (!at) return;
    const lines = $("kernel-source").value.split("\n"),
      line = Math.min(Number(at[1]), lines.length) - 1,
      start = lines.slice(0, line).join("\n").length + (line ? 1 : 0);
    $("kernel-source").setSelectionRange(start, start + lines[line].length);
    $("kernel-source").focus();
  }

  async function run() {
    $("compile").disabled = true;
    diagnostics("");
    report("Compile with Fern");
    const started = performance.now();
    try {
      const reply = await compile($("kernel-source").value);
      if (reply.error) throw Error(reply.error);
      const elapsed = performance.now() - started,
        { exit, stderr, stdoutBytes } = reply.result;
      if (exit !== 0) {
        diagnostics(stderr.trim());
        point(stderr);
        report("The kernel did not compile", false, true);
        return;
      }
      const module = await WebAssembly.compile(stdoutBytes),
        imports = WebAssembly.Module.imports(module)
          .map((i) => `${i.module}.${i.name}`)
          .filter((name) => !provided.has(name)),
        exports = WebAssembly.Module.exports(module).map((e) => e.name),
        missing = required.filter((name) => !exports.includes(name));
      if (imports.length || missing.length) {
        diagnostics(
          [
            ...imports.map(
              (name) =>
                `The kernel imports ${name}. The page provides only proc_exit and fd_write.`,
            ),
            ...missing.map(
              (name) =>
                `The kernel does not export ${name}. Keep the @export annotations and their parameters.`,
            ),
          ].join("\n"),
        );
        report("The kernel cannot run here", false, true);
        return;
      }
      await use(stdoutBytes.buffer);
      report(
        `Your kernel · compiled in ${elapsed.toFixed(0)} ms · ${(stdoutBytes.length / 1024).toFixed(1)} KiB`,
        true,
      );
    } catch (error) {
      report(error.message, false, true);
    } finally {
      $("compile").disabled = false;
    }
  }

  $("editor-open").onclick = open;
  $("compile").onclick = run;
  $("revert").onclick = async () => {
    $("kernel-source").value = original;
    diagnostics("");
    await restore();
    report("Original kernel", true);
  };
  $("kernel-source").addEventListener("keydown", (event) => {
    if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) {
      event.preventDefault();
      run();
    }
  });
}
