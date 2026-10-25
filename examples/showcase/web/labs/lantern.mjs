import {
  shell,
  $,
  action,
  error,
  notice,
  roomControls,
  bindRoom,
  request,
  fileInput,
  download,
  measures,
} from "./shell.mjs";
shell(
  "Lantern",
  "Build a searchable archive. Import documents, inspect ranked results, and measure how much of the index each query visits.",
  `${roomControls()}<h2>Add a document</h2><label>Title<input id="title" value="My field notes" maxlength="256"></label><label>Text<textarea id="text" maxlength="8192">Fern connects native applications and browser experiments.</textarea></label><button id="add">Add to archive</button><h2>Push it</h2><label>Generated documents<select id="count"><option>100</option><option>500</option><option>1000</option></select></label><button id="generate">Import test archive</button><button id="stop-import">Stop import</button><p class="hint">The archive holds 4096 documents. Each document has at most 8192 UTF-8 bytes. NDJSON imports stream one document per line with network backpressure.</p>`,
  `<div class="content"><label>Search<input id="query" type="search" placeholder="Try music, world, café, or 東京" maxlength="1024"></label><div class="buttons"><button id="search">Search archive</button><button data-query="music">Music</button><button data-query="world">Worlds</button><button data-query="CAFÉ">CAFÉ</button><button data-query="東京">東京</button></div><p id="query-info" class="hint"></p><div id="results" aria-live="polite"></div><details><summary>Import format</summary><pre>{"title":"A document","text":"Searchable text"}
{"title":"Another document","text":"One JSON object per line"}</pre></details><p id="storage" class="hint"></p></div>`,
);
$("lesson").textContent =
  "Unicode lowercase conversion and tokenization produce terms. Each term maps to a posting list. The Fern engine computes BM25 scores and keeps the best twenty hits. Documents never enter the page as HTML.";
bindRoom();
const endpoint = (action, params = {}) =>
  `/lantern/${action}?` +
  new URLSearchParams({
    room: new URL(location.href).searchParams.get("room") || "commons",
    ...params,
  });
let revision = 0,
  stopped = false,
  busy = false,
  controller,
  searchId = 0;
async function state() {
  const data = await request(endpoint("state"));
  revision = data.revision;
  $("storage").textContent = data.persistent
    ? "A persistent volume retains the archive across process restarts."
    : "This archive uses temporary server memory. Retain your source files.";
  return data;
}
async function search() {
  const id = ++searchId,
    at = performance.now();
  const data = await request(endpoint("search", { q: $("query").value }));
  if (id !== searchId) return;
  revision = data.revision;
  $("results").replaceChildren();
  for (const hit of data.result.hits) {
    const element = document.createElement("article");
    element.className = "result";
    const h = document.createElement("h3"),
      p = document.createElement("p"),
      small = document.createElement("small");
    h.textContent = hit.title;
    p.textContent = hit.text;
    small.textContent = `BM25 ${hit.score.toFixed(5)} · ${hit.matched} matched terms · document ${hit.id}`;
    element.append(h, p, small);
    $("results").append(element);
  }
  if (!data.result.hits.length)
    $("results").textContent = $("query").value
      ? "No matching documents."
      : "Enter a term or choose an example.";
  $("query-info").textContent =
    "Query terms: " + (data.result.query_terms.join(" · ") || "none");
  measures({
    Documents: data.result.documents,
    Vocabulary: data.result.terms,
    "Posting entries visited": data.result.postings_read,
    Results: data.result.hits.length,
    "Native query": (data.server_us / 1000).toFixed(3) + " ms",
    "Round trip": (performance.now() - at).toFixed(2) + " ms",
  });
}
async function add(title, text) {
  if (
    new TextEncoder().encode(title).length > 256 ||
    new TextEncoder().encode(text).length > 8192
  )
    throw Error("Document title or text exceeds the UTF-8 byte limit.");
  const data = await request(endpoint("import"), { title, text, revision });
  revision = data.revision;
  return data;
}
$("add").onclick = async () => {
  try {
    await state();
    await add($("title").value, $("text").value);
    await search();
    notice("Document indexed.");
  } catch (e) {
    error(e);
  }
};
$("search").onclick = () => search().catch(error);
$("query").onkeydown = (e) => {
  if (e.key === "Enter") search().catch(error);
};
for (const b of document.querySelectorAll("[data-query]"))
  b.onclick = () => {
    $("query").value = b.dataset.query;
    search().catch(error);
  };
async function importing(run) {
  if (busy) throw Error("An import is already active.");
  busy = true;
  stopped = false;
  try {
    await state();
    await run();
    await search();
    notice(
      stopped
        ? "Import stopped. Already committed documents remain."
        : "Import complete.",
    );
  } finally {
    busy = false;
  }
}
$("stop-import").onclick = () => {
  stopped = true;
  controller?.abort();
};
$("generate").onclick = () =>
  importing(async () => {
    const count = Number($("count").value),
      at = performance.now();
    for (let i = 0; i < count && !stopped; i++) {
      await add(
        `Field report ${i}`,
        `${["music audio synthesis", "world factory conveyors", "database index transaction", "browser wasm renderer", "café 東京 Unicode"][i % 5]} — document ${i}. Fern connects these experiments. ${"A short field report. ".repeat((i % 20) + 1)}`,
      );
      notice(`${i + 1} / ${count} documents indexed.`);
    }
    notice(`Import elapsed: ${(performance.now() - at).toFixed(0)} ms.`);
  }).catch(error);
fileInput(".ndjson,.jsonl,application/x-ndjson", (file) =>
  importing(async () => {
    if (file.size > 33554432) throw Error("Archive file exceeds 32 MiB.");
    controller = new AbortController();
    const reader = file.stream().getReader(),
      decoder = new TextDecoder("utf-8", { fatal: true });
    let buffer = "",
      count = 0;
    try {
      while (!stopped) {
        const chunk = await reader.read();
        buffer += decoder.decode(chunk.value || new Uint8Array(), {
          stream: !chunk.done,
        });
        let at;
        while ((at = buffer.indexOf("\n")) >= 0) {
          const line = buffer.slice(0, at).trim();
          buffer = buffer.slice(at + 1);
          if (line) {
            const doc = JSON.parse(line);
            if (typeof doc.title !== "string" || typeof doc.text !== "string")
              throw Error("Each line needs title and text strings.");
            await add(doc.title, doc.text);
            count++;
            notice(`${count} documents indexed.`);
          }
          if (stopped) break;
        }
        if (new TextEncoder().encode(buffer).length > 16384)
          throw Error("A document line exceeds 16 KiB.");
        if (chunk.done) {
          if (buffer.trim() && !stopped) {
            const doc = JSON.parse(buffer);
            if (typeof doc.title !== "string" || typeof doc.text !== "string")
              throw Error("Each line needs title and text strings.");
            await add(doc.title, doc.text);
          }
          break;
        }
      }
    } finally {
      await reader.cancel();
      controller = null;
    }
  }),
);
action("Refresh index", async () => {
  await state();
  await search();
});
state().then(search).catch(error);
