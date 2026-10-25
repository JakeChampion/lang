import assert from "node:assert/strict";
import { join, until } from "./labs-test-client.mjs";
const base = process.argv[2] || "http://localhost:8080",
  mode = process.argv[3] || "write",
  room = process.env.FERN_LABS_TEST_ROOM || "tests",
  peers = [];
async function request(app, action, body, expected = 200) {
  const response = await fetch(
    `${base}/${app}/${action}?room=${room}`,
    body === undefined
      ? {}
      : {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
        },
  );
  const text = await response.text();
  assert.equal(response.status, expected, `${app}/${action}: ${text}`);
  return text ? JSON.parse(text) : null;
}
async function peer(app) {
  const p = await join(base, app, room);
  peers.push(p);
  return p;
}
try {
  if (mode === "recover") {
    const db = await request("fernbase", "state");
    assert.deepEqual(db.rows, [{ key: "alpha", value: "committed" }]);
    assert.equal(db.revision, 1);
    const response = await fetch(
        `${base}/lantern/search?room=${room}&q=violetquartz`,
      ),
      found = await response.json();
    assert.equal(found.result.hits.length, 2);
    for (const app of ["foundry", "circuit", "patchbay"]) {
      const p = await peer(app);
      if (app === "foundry") {
        await until(() => p.frame, "no recovered factory");
        assert.equal(p.snapshot.cells[20], 7);
        assert.equal(p.frame.running, false);
      } else if (app === "circuit") {
        assert.equal(p.snapshot.cells[0], 8);
        assert.equal(p.snapshot.program[0], 18);
      } else assert.equal(p.snapshot.cells[0], 0);
    }
    console.log(
      "committed database, archive, factory cargo, gates, processor program, and sequence recovered",
    );
  } else {
    let result = await request("fernbase", "query", {
      sql: "INSERT INTO entries VALUES ('alpha','draft');",
      revision: 0,
      preview: true,
    });
    assert.equal(result.result.changed, true);
    assert.equal((await request("fernbase", "state")).total, 0);
    await request("fernbase", "query", {
      sql: "INSERT INTO entries VALUES ('alpha','committed');",
      revision: 0,
      preview: false,
    });
    await request(
      "fernbase",
      "query",
      {
        sql: "INSERT INTO entries VALUES ('beta','must roll back'); INSERT INTO entries VALUES ('alpha','duplicate');",
        revision: 1,
        preview: false,
      },
      400,
    );
    assert.equal((await request("fernbase", "state")).total, 1);
    await request(
      "fernbase",
      "query",
      {
        sql: "UPDATE entries SET value='stale' WHERE key='alpha';",
        revision: 0,
        preview: false,
      },
      409,
    );
    await request(
      "fernbase",
      "query",
      { sql: "SELECT * FROM entries;", revision: 0, preview: true },
      409,
    );
    const archive = await request("lantern", "state");
    await request("lantern", "import", {
      title: "Violet notebook",
      text: "violetquartz violetquartz violetquartz café 東京",
      revision: archive.revision,
    });
    await request(
      "lantern",
      "import",
      {
        title: "Other notebook",
        text: "violetquartz " + "other ".repeat(40),
        revision: archive.revision,
      },
      409,
    );
    await request("lantern", "import", {
      title: "Other notebook",
      text: "violetquartz " + "other ".repeat(40),
      revision: archive.revision + 1,
    });
    const response = await fetch(
        `${base}/lantern/search?room=${room}&q=violetquartz`,
      ),
      search = await response.json();
    assert.equal(search.result.hits.length, 2);
    assert.equal(search.result.hits[0].title, "Violet notebook");
    assert.equal(search.result.postings_read, 2);
    const exported = await (
      await fetch(`${base}/lantern/export?room=${room}`)
    ).text();
    const documents = exported.trim().split("\n").map(JSON.parse);
    const tokenize = (text) =>
      text
        .toLowerCase()
        .replace(/[\n\r\t.,;:!?()[\]{}"'\/\\\-—　]/gu, " ")
        .split(" ")
        .filter((t) => t.length > 0 && Buffer.byteLength(t) <= 128);
    const lengths = documents.map(
        (d) => tokenize(d.title + " " + d.text).length,
      ),
      average = lengths.reduce((a, b) => a + b, 0) / documents.length;
    for (const hit of search.result.hits) {
      const words = tokenize(
          documents[hit.id].title + " " + documents[hit.id].text,
        ),
        frequency = words.filter((t) => t === "violetquartz").length,
        idf = Math.log(1 + (documents.length - 2 + 0.5) / (2 + 0.5)),
        expected =
          (idf * frequency * 2.2) /
          (frequency + 1.2 * (0.25 + (0.75 * lengths[hit.id]) / average));
      assert.ok(
        Math.abs(hit.score - expected) < 1e-10,
        "BM25 differs from independent reference",
      );
    }
    for (const app of ["foundry", "circuit", "patchbay"]) {
      const a = await peer(app),
        b = await peer(app);
      assert.notEqual(a.snapshot.client_id, b.snapshot.client_id);
      assert.equal(a.snapshot.persistent, true);
      if (app === "foundry") {
        await until(() => a.frame && b.frame, "no authoritative factory state");
        a.command("pause");
        await until(
          () => a.frame && !a.frame.running && b.frame && !b.frame.running,
          "shared pause failed",
        );
        a.command("edit", [20], [7]);
        await until(
          () => b.snapshot.cells[20] === 7,
          "factory layout edit did not reach peer",
        );
        a.command("step");
        await until(
          () =>
            a.frame.tick === b.frame.tick &&
            a.frame.sequence === b.frame.sequence,
          "factory peers disagree",
        );
        assert.deepEqual(a.frame.cargo, b.frame.cargo);
      } else if (app === "circuit") {
        a.command("edit", [0], [8]);
        await until(
          () => b.snapshot.cells[0] === 8,
          "gate edit did not reach peer",
        );
        a.command("program", [0], [18]);
        await until(
          () => b.snapshot.program[0] === 18,
          "processor program did not reach peer",
        );
      } else {
        a.command("edit", [0], [0]);
        await until(
          () => b.snapshot.cells[0] === 0,
          "sequence edit did not reach peer",
        );
        a.command("note", [60], [1]);
        await until(
          () =>
            b.messages.some(
              (m) =>
                m.kind === "note" &&
                m.points[0] === 60 &&
                m.sender === a.snapshot.client_id,
            ),
          "live note did not reach peer",
        );
        a.command("note", [60], [0]);
      }
      await request(app, "checkpoint", {});
    }
    const frame = peers.find((p) => p.frame)?.frame;
    console.log(
      JSON.stringify({
        tick: frame.tick,
        produced: frame.produced,
        consumed: frame.consumed,
        refined: frame.refined,
      }),
    );
  }
} catch (e) {
  console.error(e);
  process.exitCode = 1;
} finally {
  for (const p of peers) p.socket.close();
}
