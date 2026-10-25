import { mountFullscreen, toast } from "/assets/ui.mjs";
export const $ = (id) => document.getElementById(id);
export function shell(
  name,
  description,
  controls,
  stage = '<canvas id="view" tabindex="0" aria-label="Interactive application view"></canvas>',
) {
  document.title = `${name} · Fern`;
  document.body.innerHTML = `<a class="skip-link" href="#main">Skip to application</a><nav><a href="/">✳ fern / in action</a><a href="/_src/showcase/${name.toLowerCase()}.fern.md">Read the Fern source ↗</a></nav><header class="page-title"><div class="eyebrow">FERN / ${name.toUpperCase()}</div><h1>${name}</h1><p class="description">${description}</p></header><main class="lab" id="main"><div class="toolbar"><div id="actions"></div><div id="fullscreen"></div><span id="connection" class="status" role="status">Ready</span></div><aside class="sidebar">${controls}<details><summary>How this works</summary><p id="lesson" class="hint"></p><a href="/_src/showcase/${name.toLowerCase()}.fern.md">Open the source lesson</a></details></aside><section class="stage" id="stage">${stage}</section><output class="readout" id="metrics" aria-live="off">Start an experiment to measure it.</output></main>`;
  mountFullscreen(document.querySelector(".lab"), {
    container: $("fullscreen"),
  });
}
export function action(label, fn, id) {
  const b = document.createElement("button");
  b.textContent = label;
  if (id) b.id = id;
  b.onclick = () => Promise.resolve(fn()).catch(error);
  $("actions").append(b);
  return b;
}
export function error(e) {
  $("connection").textContent = e.message || String(e);
  $("connection").classList.add("error");
}
export function notice(text) {
  $("connection").textContent = text;
  $("connection").classList.remove("error");
}
export async function request(path, body) {
  const r = await fetch(
    path,
    body === undefined
      ? {}
      : {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
        },
  );
  const data = await r.json();
  if (!r.ok) {
    const e = new Error(
      data.detail || data.title || `Request failed: ${r.status}`,
    );
    e.status = r.status;
    throw e;
  }
  return data;
}
export function room() {
  const r = new URL(location.href).searchParams.get("room") || "commons";
  return /^[a-z0-9_-]{1,32}$/.test(r) ? r : "commons";
}
export function roomControls() {
  return '<label>Shared project<select id="room"><option>commons</option><option>meadow</option><option>harbor</option></select></label><button id="share">Copy project link</button>';
}
export function bindRoom() {
  if (!$("room")) return;
  $("room").value = room();
  $("room").onchange = () => {
    const u = new URL(location.href);
    u.searchParams.set("room", $("room").value);
    location.href = u;
  };
  $("share").onclick = async () => {
    try {
      await navigator.clipboard.writeText(location.href);
      toast("Project link copied.");
    } catch {
      toast("Share this page address.");
    }
  };
}
export function download(name, data, type = "application/json") {
  const url = URL.createObjectURL(new Blob([data], { type }));
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
export function measures(items) {
  $("metrics").replaceChildren();
  for (const [label, value] of Object.entries(items)) {
    const s = document.createElement("span");
    s.append(label + " ");
    const strong = document.createElement("strong");
    strong.textContent = value;
    s.append(strong);
    $("metrics").append(s);
  }
}
export function percentiles(values) {
  if (!values.length) return { p50: 0, p95: 0, p99: 0 };
  const sorted = [...values].sort((a, b) => a - b);
  return Object.fromEntries(
    [
      ["p50", 0.5],
      ["p95", 0.95],
      ["p99", 0.99],
    ].map(([k, q]) => [
      k,
      sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * q))],
    ]),
  );
}
export function fileInput(accept, load) {
  const i = document.createElement("input");
  i.type = "file";
  i.accept = accept;
  i.setAttribute("aria-label", "Import file");
  i.onchange = () => {
    if (i.files[0]) Promise.resolve(load(i.files[0])).catch(error);
  };
  $("actions").append(i);
  return i;
}
