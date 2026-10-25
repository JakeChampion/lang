import assert from "node:assert/strict";
const base = process.argv[2] || "http://localhost:8080";
const room = "rpc-" + Date.now().toString(36);
async function channel(app) {
  const ws = new WebSocket(
    base.replace(/^http/, "ws") + `/${app}/ws?room=${room}`,
  );
  const pending = new Map();
  let seq = 0;
  ws.onmessage = ({ data }) => {
    const reply = JSON.parse(data),
      task = pending.get(reply.seq);
    if (task) {
      pending.delete(reply.seq);
      clearTimeout(task.timer);
      task.resolve(reply);
    }
  };
  await new Promise((resolve, reject) => {
    ws.onopen = resolve;
    ws.onerror = reject;
  });
  return {
    ws,
    async call(action, body, want = 200) {
      const id = ++seq;
      const result = new Promise((resolve, reject) => {
        const timer = setTimeout(() => reject(Error("RPC timeout")), 10000);
        pending.set(id, { resolve, timer });
      });
      ws.send(
        JSON.stringify({
          seq: id,
          path: `/${app}/${action}${action.includes("?") ? "&" : "?"}room=${room}`,
          post: body !== undefined,
          body: body === undefined ? "" : JSON.stringify(body),
        }),
      );
      const reply = await result;
      assert.equal(reply.status, want, reply.body);
      return action.startsWith("export") && app === "lantern"
        ? reply.body
        : JSON.parse(reply.body);
    },
  };
}
const channels = [];
try {
  const db = await channel("fernbase");
  channels.push(db);
  const db2 = await channel("fernbase");
  channels.push(db2);
  assert.equal((await db.call("state")).revision, 0);
  await db.call("query", {
    sql: "INSERT INTO entries VALUES ('café','東京');",
    revision: 0,
    preview: true,
  });
  assert.equal((await db2.call("state")).total, 0);
  await db.call("query", {
    sql: "INSERT INTO entries VALUES ('café','東京');",
    revision: 0,
    preview: false,
  });
  assert.deepEqual((await db2.call("state")).rows, [
    { key: "café", value: "東京" },
  ]);
  await db2.call(
    "query",
    {
      sql: "DELETE FROM entries WHERE key='café';",
      revision: 0,
      preview: false,
    },
    409,
  );
  const archive = await channel("lantern");
  channels.push(archive);
  const state = await archive.call("state");
  await archive.call("import", {
    title: "WebSocket document",
    text: "violetrpc CAFÉ 東京",
    revision: state.revision,
  });
  assert.equal(
    (await archive.call("search?q=violetrpc")).result.hits.length,
    1,
  );
  assert.ok((await archive.call("export")).includes("violetrpc"));
  const metrics = await (await fetch(base + "/pulse/snapshot")).json();
  assert.ok(
    metrics.websocket_connections >= 3,
    "Pulse omitted command connections",
  );
  assert.ok(metrics.websocket_messages >= 8, "Pulse omitted command messages");
  const rejected = new Promise(
    (resolve) => (db2.ws.onclose = (event) => resolve(event.code)),
  );
  db2.ws.send(
    JSON.stringify({
      seq: 999,
      path: `/lantern/state?room=${room}`,
      body: "",
      post: false,
    }),
  );
  assert.equal(await rejected, 1008);
  console.log(
    "Persistent command sessions: shared transactions, stale writes, Unicode search/export, and route scope OK",
  );
} finally {
  for (const c of channels) c.ws.close();
}
