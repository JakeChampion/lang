import { after, before, test } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:http";
import { mkdir, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const productionSpec = fileURLToPath(new URL("../playground.spec.ts", import.meta.url));
const bombadil = process.env.BOMBADIL ?? "bombadil";
let server;
let origin;
let work;

before(async () => {
  const results = join(dirname(productionSpec), "results");
  await mkdir(results, { recursive: true });
  work = await mkdtemp(join(results, "editor-lifetime-"));
  server = createServer((request, response) => {
    const remove = request.url === "/remove";
    response.writeHead(200, { "Content-Type": "text/html" });
    response.end(`<!doctype html><html><body>
      <button style="position:absolute;left:0;top:0;width:100px;height:40px"
        onclick="const editor = document.querySelector('.cm-editor');
          if (!editor) { document.querySelector('#srcMount').innerHTML = '<div class=cm-editor>Editor</div>'; }
          else { ${remove ? "editor.remove()" : "this.dataset.clicked = 'yes'"} }">Action</button>
      <div id="srcMount"></div>
    </body></html>`);
  });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  origin = `http://127.0.0.1:${server.address().port}`;
});

after(async () => {
  if (server) await new Promise(resolve => server.close(resolve));
});

async function exercise(name, action) {
  const specification = join(work, `${name}.ts`);
  const output = join(work, name);
  await writeFile(specification, `
    export { editorNeverTearsDown } from ${JSON.stringify(productionSpec)};
    import { actions, extract } from "@antithesishq/bombadil";
    const mounted = extract(s => s.document.querySelector(".cm-editor") !== null);
    export const drive = actions(() => [mounted.current ? ${JSON.stringify(action)}
      : { Click: { name: "BUTTON", content: "Action", point: { x: 50, y: 20 } } }]);
  `);
  const child = spawn(bombadil, ["test", `${origin}/${name}`, specification,
    "--output-path", output, "--headless", "--no-sandbox",
    "--time-limit", "2s", "--exit-on-violation"], { timeout: 30_000 });
  let log = "";
  child.stdout.on("data", data => { log += data; });
  child.stderr.on("data", data => { log += data; });
  const exit = await new Promise((resolve, reject) => {
    child.on("error", reject);
    child.on("close", (code, signal) => signal ? reject(new Error(`${signal}\n${log}`)) : resolve(code));
  });
  await writeFile(join(work, `${name}.log`), log);
  const saved = (await readFile(join(output, "trace.jsonl"), "utf8")).trim();
  assert.ok(saved, log);
  const trace = saved.split("\n").map(line => JSON.parse(line));
  assert.ok(trace.some(row => JSON.stringify(row.action) === JSON.stringify(action)), log);
  return { exit, trace, log };
}

test("the editor lifetime property permits a new document after reload", async () => {
  const result = await exercise("reload", "Reload");
  assert.equal(result.exit, 0, result.log);
  assert.ok(result.trace.every(row => row.violations.length === 0), result.log);
  assert.ok(result.trace.some(row => row.snapshots.some(s => s.name === "editorMounted" && s.value === true)), result.log);
  assert.ok(result.trace.some(row => row.action === "Reload" && row.snapshots.some(s => s.name === "editorMounted" && s.value === false)), result.log);
  const origins = result.trace.flatMap(row => row.snapshots)
    .filter(snapshot => snapshot.name === "documentStartedAt").map(snapshot => snapshot.value);
  assert.ok(new Set(origins).size > 1, "the reload must create another document");
});

test("the editor lifetime property refuses removal within one document", async () => {
  const result = await exercise("remove", { Click: { name: "BUTTON", content: "Action", point: { x: 50, y: 20 } } });
  assert.equal(result.exit, 2, result.log);
  assert.ok(result.trace.some(row => row.violations.some(v => v.name === "editorNeverTearsDown")), result.log);
  const origins = result.trace.flatMap(row => row.snapshots)
    .filter(snapshot => snapshot.name === "documentStartedAt").map(snapshot => snapshot.value);
  assert.equal(new Set(origins).size, 1, "the removal must stay in one document");
});

test("the editor lifetime property permits an unchanged editor", async () => {
  const result = await exercise("unchanged", { Click: { name: "BUTTON", content: "Action", point: { x: 50, y: 20 } } });
  assert.equal(result.exit, 0, result.log);
  assert.ok(result.trace.every(row => row.violations.length === 0), result.log);
});
