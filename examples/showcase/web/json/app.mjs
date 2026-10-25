import { $, status, json } from "/assets/ui.mjs";
import { kernel, mib, size, pause } from "/bench/kernel.mjs";

const fresh = await kernel("/bench/measure.wasm", "fern:showcase/measure").catch(
  (error) => {
    status("connection", error.message, false, true);
    throw error;
  },
);
status("connection", "Ready · Fern Wasm loaded", true);
const decoder = new TextDecoder();
const budget = 4194304;

function count(value) {
  if (Array.isArray(value))
    return value.reduce((total, item) => total + count(item), 1);
  if (value && typeof value === "object")
    return Object.values(value).reduce((total, item) => total + count(item), 1);
  return 1;
}

function row(engine, where, ms, bytes, values, fastest, fern) {
  const rate = mib(bytes, ms);
  return `<tr><td class="engine"><strong>${engine}</strong><span>${where}</span></td>
    <td class="number">${ms.toFixed(2)} ms</td>
    <td class="rate"><div class="bar${fern ? " fern" : ""}"><i style="width:${Math.max(1, (70 * rate) / fastest)}%"></i>${rate.toFixed(0)} MiB/s</div></td>
    <td class="number">${values.toLocaleString()}</td></tr>`;
}

$("corpus").onchange = () => {
  $("file-field").hidden = $("corpus").value !== "file";
};

$("controls").onsubmit = async (event) => {
  event.preventDefault();
  $("run").disabled = true;
  try {
    const own = $("corpus").value === "file";
    const seed = Number($("seed").value) || 0;
    const target = own ? 0 : Number($("corpus").value);
    let bytes;
    if (own) {
      const file = $("file").files[0];
      if (!file) throw Error("Choose a JSON file first.");
      if (file.size > 8388608) throw Error("Choose a file of 8 MiB or less.");
      bytes = new Uint8Array(await file.arrayBuffer());
    } else {
      const generator = await fresh();
      bytes = generator.list(generator.call("json-document", seed, target));
    }
    const text = decoder.decode(bytes);
    const rounds = Math.max(1, Math.min(32, Math.floor(budget / bytes.length)));
    const server = own
      ? null
      : json(`/json/bench?seed=${seed}&size=${target}`).catch((e) => e);
    const results = [];
    const local = await fresh();
    const checksum = local.call("checksum", bytes) | 0;
    for (const [name, label] of [
      ["parse-tree", "Fern tree parser"],
      ["parse-tape", "Fern tape parser"],
    ]) {
      status("connection", `Measure the ${label.toLowerCase()} in WebAssembly`);
      await pause();
      const instance = await fresh(),
        started = performance.now(),
        values = instance.call(name, text, rounds),
        ms = (performance.now() - started) / rounds;
      if (values < 0) throw Error("Fern could not parse this document.");
      results.push({ engine: label, where: "WebAssembly · this browser", ms, values, fern: true });
    }
    status("connection", "Measure JSON.parse");
    await pause();
    let started = performance.now(),
      parsed;
    for (let i = 0; i < rounds; i++) parsed = JSON.parse(text);
    results.push({
      engine: "JSON.parse",
      where: "Browser engine · native code",
      ms: (performance.now() - started) / rounds,
      values: count(parsed),
    });
    let agreement = "";
    if (server) {
      status("connection", "Wait for the native server");
      const answer = await server;
      if (answer instanceof Error) {
        agreement = ` The server did not measure: ${answer.message}`;
      } else {
        results.unshift(
          { engine: "Fern tree parser", where: "Native · server", ms: answer.tree.ns / 1e6 / answer.tree.rounds, values: answer.tree.values, fern: true },
          { engine: "Fern tape parser", where: "Native · server", ms: answer.tape.ns / 1e6 / answer.tape.rounds, values: answer.tape.values, fern: true },
        );
        agreement =
          answer.checksum === checksum && answer.bytes === bytes.length
            ? ` The server generated identical bytes (checksum ${(checksum >>> 0).toString(16)}).`
            : " The server's bytes differ from this page's bytes.";
      }
    }
    const fastest = Math.max(...results.map((r) => mib(bytes.length, r.ms)));
    $("results").innerHTML = results
      .map((r) => row(r.engine, r.where, r.ms, bytes.length, r.values, fastest, r.fern))
      .join("");
    const counts = new Set(results.map((r) => r.values));
    $("summary").innerHTML =
      `${size(bytes.length)}, ${rounds} rounds per engine. ` +
      (counts.size === 1
        ? `<strong>Every engine counted ${[...counts][0].toLocaleString()} values.</strong>`
        : "<strong>The engines counted different values.</strong>") +
      agreement;
    $("results").dataset.agree = String(counts.size === 1);
    status("connection", "Measured", true);
  } catch (error) {
    status("connection", error.message, false, true);
  } finally {
    $("run").disabled = false;
  }
};
