import { $, status, json } from "/assets/ui.mjs";
import { kernel, mib, size, pause } from "/bench/kernel.mjs";

const fresh = await kernel("/bench/measure.wasm", "fern:showcase/measure").catch(
  (error) => {
    status("connection", error.message, false, true);
    throw error;
  },
);
status("connection", "Ready · Fern Wasm loaded", true);
const codecs = ["gzip", "Brotli", "zstd"];
const browserFormats = { gzip: "gzip", Brotli: "brotli", zstd: "zstd" };

async function stream(bytes, transform) {
  return new Uint8Array(
    await new Response(new Blob([bytes]).stream().pipeThrough(transform)).arrayBuffer(),
  );
}
function decoder(format) {
  try {
    return new DecompressionStream(format);
  } catch {
    return null;
  }
}
const same = (a, b) => a.length === b.length && a.every((v, i) => v === b[i]);

function row(r, original, fastest) {
  const ratio = original / r.bytes,
    encode = mib(original, r.encodeMs),
    decode = mib(original, r.decodeMs);
  return `<tr><td class="engine"><strong>${r.engine}</strong><span>${r.where}</span></td>
    <td class="number">${ratio.toFixed(2)}× · ${size(r.bytes)}</td>
    <td class="rate"><div class="bar${r.fern ? " fern" : ""}"><i style="width:${Math.max(1, (70 * encode) / fastest)}%"></i>${encode.toFixed(0)} MiB/s</div></td>
    <td class="number">${decode.toFixed(0)} MiB/s</td>
    <td><span class="verdict${r.lossless ? "" : " failed"}">${r.lossless ? "✓ lossless" : "✗ differs"}</span></td></tr>`;
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
      if (!file) throw Error("Choose a file first.");
      if (file.size > 4194304) throw Error("Choose a file of 4 MiB or less.");
      bytes = new Uint8Array(await file.arrayBuffer());
    } else {
      const generator = await fresh();
      bytes = generator.list(generator.call("access-log", seed, target));
    }
    const rounds = Math.max(1, Math.min(16, Math.floor(1048576 / bytes.length)));
    const server = own
      ? null
      : json(`/compress/bench?seed=${seed}&size=${target}`).catch((e) => e);
    const checksum = (await fresh()).call("checksum", bytes) | 0;
    const results = [],
      interop = [];
    for (let codec = 0; codec < 3; codec++) {
      status("connection", `Measure Fern ${codecs[codec]} in WebAssembly`);
      await pause();
      const instance = await fresh();
      let started = performance.now();
      const packed = instance.list(instance.call("compress", codec, bytes, rounds));
      const encodeMs = (performance.now() - started) / rounds;
      started = performance.now();
      const sum = instance.call("decompress", codec, packed, bytes.length, rounds) | 0;
      const decodeMs = (performance.now() - started) / rounds;
      results.push({ engine: `Fern ${codecs[codec]}`, where: "WebAssembly · this browser", bytes: packed.length, encodeMs, decodeMs, lossless: sum === checksum, fern: true });
      const native = decoder(browserFormats[codecs[codec]]);
      if (native)
        interop.push(`${codecs[codec]} ${same(await stream(packed, native), bytes) ? "✓" : "✗"}`);
    }
    status("connection", "Measure the browser's gzip");
    await pause();
    let started = performance.now(),
      packed;
    for (let i = 0; i < rounds; i++)
      packed = await stream(bytes, new CompressionStream("gzip"));
    const encodeMs = (performance.now() - started) / rounds;
    started = performance.now();
    let plain;
    for (let i = 0; i < rounds; i++)
      plain = await stream(packed, new DecompressionStream("gzip"));
    results.push({ engine: "CompressionStream gzip", where: "Browser engine · native code", bytes: packed.length, encodeMs, decodeMs: (performance.now() - started) / rounds, lossless: same(plain, bytes) });
    let agreement = "";
    if (server) {
      status("connection", "Wait for the native server");
      const answer = await server;
      if (answer instanceof Error) {
        agreement = ` The server did not measure: ${answer.message}`;
      } else {
        results.unshift(
          ...answer.codecs.map((c) => ({ engine: `Fern ${c.name}`, where: "Native · server", bytes: c.bytes, encodeMs: c.encode_ns / 1e6 / c.rounds, decodeMs: c.decode_ns / 1e6 / c.rounds, lossless: c.lossless, fern: true })),
        );
        agreement =
          answer.checksum === checksum
            ? ` The server generated identical bytes (checksum ${(checksum >>> 0).toString(16)}).`
            : " The server's bytes differ from this page's bytes.";
      }
    }
    const fastest = Math.max(...results.map((r) => mib(bytes.length, r.encodeMs)));
    $("results").innerHTML = results.map((r) => row(r, bytes.length, fastest)).join("");
    $("results").dataset.lossless = String(results.every((r) => r.lossless));
    $("summary").innerHTML = `${size(bytes.length)}, ${rounds} rounds per codec.${agreement}`;
    $("interop").textContent = interop.length
      ? `Your browser's decoders read Fern's output: ${interop.join(" · ")}.`
      : "Your browser has no decoder for these formats in DecompressionStream.";
    $("interop").dataset.ok = String(interop.every((entry) => entry.endsWith("✓")));
    status("connection", "Measured", true);
  } catch (error) {
    status("connection", error.message, false, true);
  } finally {
    $("run").disabled = false;
  }
};
