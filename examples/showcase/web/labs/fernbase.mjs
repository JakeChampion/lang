import {
  shell,
  $,
  action,
  error,
  notice,
  roomControls,
  bindRoom,
  request,
  download,
  measures,
} from "./shell.mjs";
shell(
  "Fernbase",
  "Run a transaction and inspect its index. See exactly when a change becomes visible and what survives recovery.",
  `${roomControls()}<h2>Try a query</h2><label>Example<select id="example"><option value="select">Read the table</option><option value="lookup">Find a primary key</option><option value="insert">Insert a row</option><option value="update">Update a row</option><option value="delete">Delete a row</option><option value="explain">Explain a lookup</option></select></label><button id="load-example">Load example</button><h2>Push it</h2><label>Rows to insert<select id="rows"><option>100</option><option>1000</option><option>5000</option></select></label><button id="seed">Insert test data</button><p class="hint">The entries table has two text columns: key and value. This engine supports SELECT *, equality WHERE clauses, INSERT, UPDATE, DELETE, and EXPLAIN SELECT. Text uses single quotes.</p>`,
  `<div class="content"><label>SQL<textarea id="sql" spellcheck="false">SELECT * FROM entries;</textarea></label><div class="buttons"><button id="run">Run SQL</button><button id="begin">BEGIN</button><button id="commit" disabled>COMMIT</button><button id="rollback" disabled>ROLLBACK</button></div><p id="transaction" role="status">Each request is one atomic transaction.</p><details><summary>Draft statements</summary><pre id="draft">No draft.</pre></details><h2>Query plan</h2><pre id="plan">Ordered primary index</pre><div class="table-wrap"><table><thead><tr><th>Key</th><th>Value</th></tr></thead><tbody id="results"></tbody></table></div><h2>Primary index</h2><pre id="index"></pre><h2>Recovery</h2><p class="hint" id="storage"></p><button id="recover">Discard draft and recover</button><p class="hint">A persistent commit writes a checksummed checkpoint, synchronizes the file, replaces the old checkpoint, then synchronizes the directory. A temporary room resets to an empty database on recovery.</p></div>`,
);
$("lesson").textContent =
  "The Fern lexer and parser produce a complete statement batch. The engine applies it to a private database value. A failed statement discards the batch. An ordered array supplies binary-search lookups; it is an intentionally small SQL engine, not SQLite.";
bindRoom();
const endpoint = (action) =>
  `/fernbase/${action}?room=${encodeURIComponent(new URL(location.href).searchParams.get("room") || "commons")}`;
let revision = 0,
  draft = null,
  transactionRevision = 0,
  busy = false,
  stopped = false,
  latest;
function table(rows) {
  $("results").replaceChildren();
  for (const row of rows) {
    const tr = document.createElement("tr");
    for (const v of [row.key, row.value]) {
      const td = document.createElement("td");
      td.textContent = v;
      tr.append(td);
    }
    $("results").append(tr);
  }
}
function transaction() {
  const active = draft !== null;
  $("begin").disabled = active;
  $("commit").disabled = $("rollback").disabled = !active;
  $("transaction").textContent = active
    ? `Private draft at revision ${transactionRevision}. Other visitors cannot see it.`
    : "Each request is one atomic transaction.";
  $("draft").textContent = draft || "No draft.";
}
async function refresh() {
  const data = await request(endpoint("state"));
  latest = data;
  revision = data.revision;
  table(data.rows);
  $("index").textContent =
    data.rows
      .map((r, i) => `${String(i).padStart(4, " ")}  ${r.key}`)
      .join("\n") || "The index is empty.";
  $("storage").textContent = data.persistent
    ? "Durable volume enabled. Successful commits survive a server restart."
    : "Temporary server memory. Export the table before a restart.";
  measures({
    Revision: revision,
    Rows: data.total,
    Storage: data.persistent ? "persistent checkpoint" : "temporary",
    Index: "ordered keys",
    Fault: data.fault || "none",
  });
  return data;
}
async function run() {
  if (busy) return;
  busy = true;
  try {
    const sql = (draft ?? "") + "\n" + $("sql").value;
    const at = performance.now();
    const data = await request(endpoint("query"), {
      sql,
      revision: draft === null ? revision : transactionRevision,
      preview: draft !== null,
    });
    if (draft !== null) {
      draft = sql;
      transaction();
    } else revision = data.revision;
    table(data.result.rows);
    $("plan").textContent =
      `${data.result.plan}\n${data.result.comparisons} key comparisons\n${data.result.changed ? "Database changes in this batch." : "Read-only statement."}`;
    measures({
      Revision: revision,
      Rows: data.rows,
      Server: (data.server_us / 1000).toFixed(3) + " ms",
      "Round trip": (performance.now() - at).toFixed(2) + " ms",
      Comparisons: data.result.comparisons,
      Draft: draft !== null ? "private" : "none",
    });
    notice(
      draft !== null
        ? "Draft evaluated. Nothing committed."
        : "Transaction complete.",
    );
  } catch (e) {
    error(e);
  } finally {
    busy = false;
  }
}
$("run").onclick = run;
$("begin").onclick = async () => {
  try {
    const data = await refresh();
    transactionRevision = data.revision;
    draft = "";
    transaction();
    notice("Private transaction started.");
  } catch (e) {
    error(e);
  }
};
$("commit").onclick = async () => {
  try {
    const data = await request(endpoint("query"), {
      sql: draft || "",
      revision: transactionRevision,
      preview: false,
    });
    revision = data.revision;
    draft = null;
    transaction();
    await refresh();
    notice("Commit confirmed.");
  } catch (e) {
    error(e);
  }
};
$("rollback").onclick = async () => {
  draft = null;
  transaction();
  try {
    await refresh();
    notice("Draft discarded.");
  } catch (e) {
    error(e);
  }
};
const samples = {
  select: "SELECT * FROM entries;",
  lookup: "SELECT * FROM entries WHERE key = 'hello';",
  insert: "INSERT INTO entries VALUES ('hello', 'A transaction in Fern');",
  update: "UPDATE entries SET value = 'Changed together' WHERE key = 'hello';",
  delete: "DELETE FROM entries WHERE key = 'hello';",
  explain: "EXPLAIN SELECT * FROM entries WHERE key = 'hello';",
};
$("load-example").onclick = () =>
  ($("sql").value = samples[$("example").value]);
$("recover").onclick = async () => {
  try {
    await request(endpoint("recover"), {});
    draft = null;
    transaction();
    await refresh();
    notice("Recovery complete.");
  } catch (e) {
    error(e);
  }
};
$("seed").onclick = async () => {
  if (busy) return;
  busy = true;
  $("seed").disabled = true;
  try {
    const start = performance.now(),
      number = Number($("rows").value),
      prefix = Date.now().toString(36);
    for (let at = 0; at < number; at += 64) {
      const data = await request(endpoint("state"));
      const sql = Array.from(
        { length: Math.min(64, number - at) },
        (_, i) =>
          `INSERT INTO entries VALUES ('${prefix}-${String(at + i).padStart(5, "0")}','Generated row ${at + i}');`,
      ).join("\n");
      const result = await request(endpoint("query"), {
        sql,
        revision: data.revision,
        preview: false,
      });
      revision = result.revision;
      notice(`${Math.min(at + 64, number)} / ${number} rows inserted.`);
    }
    await refresh();
    notice(
      `${number} rows inserted in ${(performance.now() - start).toFixed(0)} ms, including network and checkpoints.`,
    );
  } catch (e) {
    error(e);
  } finally {
    busy = false;
    $("seed").disabled = false;
  }
};
action("Refresh", refresh);
action("Export rows", async () => {
  let data = await request(endpoint("state"));
  if (data.total > 200)
    throw Error(
      "The table view exports the first 200 rows. Use the SQL view to select specific keys.",
    );
  download("fernbase.json", JSON.stringify(data.rows, null, 2));
});
transaction();
refresh().catch(error);
