import { $, status, json } from "/assets/ui.mjs";
import { kernel, hex, unhex, pause } from "/bench/kernel.mjs";

const fresh = await kernel("/bench/handshake.wasm", "fern:showcase/handshake").catch(
  (error) => {
    status("connection", error.message, false, true);
    throw error;
  },
);
status("connection", "Ready · Fern Wasm loaded", true);

const step = (name) => document.querySelector(`[data-step="${name}"]`);
function done(name, where, detail) {
  const item = step(name);
  item.dataset.state = "done";
  item.querySelector(".where").textContent = where;
  if (detail) item.querySelector("p").innerHTML = detail;
}
const preview = (bytes) => `<code>${hex(bytes.slice(0, 16))}…</code>`;
const same = (a, b) => a.length === b.length && a.every((v, i) => v === b[i]);

$("start").onclick = async () => {
  $("start").disabled = true;
  $("secret").hidden = true;
  for (const item of document.querySelectorAll("[data-step]"))
    item.dataset.state = "waiting";
  try {
    const seed = crypto.getRandomValues(new Uint8Array(96));
    let instance = await fresh(),
      started = performance.now();
    const keys = instance.list(instance.call("client-keys", seed));
    if (keys.length !== 1184 + 2400 + 32) throw Error("Key generation failed.");
    const ek = keys.slice(0, 1184),
      dk = keys.slice(1184, 3584),
      clientX = keys.slice(3584);
    done(
      "keys",
      `Fern Wasm · ${(performance.now() - started).toFixed(1)} ms`,
      `Encapsulation key, 1,184 bytes: ${preview(ek)} X25519 public key, 32 bytes: ${preview(clientX)} The 2,400-byte decapsulation key stays in this page.`,
    );
    await pause();
    started = performance.now();
    const answer = await json("/handshake/exchange", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ek: hex(ek), x: hex(clientX) }),
    });
    const roundTrip = performance.now() - started;
    const ciphertext = unhex(answer.ciphertext),
      serverX = unhex(answer.x);
    done("send", `${roundTrip.toFixed(0)} ms round trip`);
    done(
      "server",
      `Native Fern · ${answer.server_us} µs`,
      `Ciphertext, ${ciphertext.length.toLocaleString()} bytes: ${preview(ciphertext)} Server X25519 key: ${preview(serverX)}`,
    );
    await pause();
    instance = await fresh();
    started = performance.now();
    const session = instance.list(
      instance.call("client-session", dk, seed.slice(64), ek, clientX, ciphertext, serverX),
    );
    if (session.length !== 64) throw Error("The ciphertext did not open.");
    const key = session.slice(0, 32),
      record = session.slice(32);
    done(
      "derive",
      `Fern Wasm · ${(performance.now() - started).toFixed(1)} ms`,
      `Transcript hash: ${preview(record)} The session key never leaves this page.`,
    );
    const confirmed = same(
      instance.list(instance.call("confirmation", key)),
      unhex(answer.confirmation),
    );
    const plain = instance.list(
      instance.call("open", key, record, unhex(answer.nonce), unhex(answer.sealed)),
    );
    if (!confirmed || !plain.length) throw Error("The server's confirmation does not match.");
    done("confirm", "✓ Confirmed");
    $("secret").textContent = new TextDecoder().decode(plain);
    $("secret").hidden = false;
    status("connection", "Shared key agreed · post-quantum hybrid", true);
  } catch (error) {
    status("connection", error.message, false, true);
  } finally {
    $("start").disabled = false;
  }
};
