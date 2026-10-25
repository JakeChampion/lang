import {
  shell,
  $,
  action,
  notice,
  error,
  roomControls,
  bindRoom,
  download,
  fileInput,
  measures,
  percentiles,
} from "./shell.mjs";
import { Session } from "./session.mjs";
shell(
  "Patchbay",
  "Make a loop together. Fern generates the sound inside an AudioWorklet, away from the page animation thread.",
  `${roomControls()}<label>Tempo<input id="tempo" type="range" min="40" max="240" value="110"><output id="tempo-value">110 BPM</output></label><label>Oscillator<select id="wave"><option value="0">Sine</option><option value="1">Triangle</option><option value="2">Saw · PolyBLEP</option><option value="3">Pulse · PolyBLEP</option></select></label><label>Low-pass filter<input id="cutoff" type="range" min="100" max="12000" value="6000"></label><label>Delay mix<input id="delay" type="range" min="0" max="65" value="25"></label><label>Drive<input id="drive" type="range" min="5" max="40" value="5"></label><label>Sequence page<select id="bank"><option value="0">Steps 1–4</option><option value="4">Steps 5–8</option><option value="8">Steps 9–12</option><option value="12">Steps 13–16</option></select></label><h2>Push it</h2><label>Voice limit<select id="polyphony"><option>8</option><option selected>16</option><option>32</option><option>64</option></select></label><button id="chord">Play a full voice bank</button><p class="hint">Enable audio first. Tap the grid to toggle notes. Hold a piano key to play it. All controls work by touch.</p>`,
  `<div class="content"><div class="eyebrow">OSCILLATOR → LOW-PASS → DELAY → DRIVE</div><h2>16-step loop</h2><div class="sequencer" id="sequence" style="grid-template-columns:repeat(4,minmax(0,1fr))"></div><h2>Play live</h2><div class="piano" id="piano"></div><p class="hint">Audio remains local to your device. The project shares notes and effects. Each listener starts playback with its own audio clock.</p></div>`,
);
$("lesson").textContent =
  "Fern computes the oscillators, envelopes, low-pass filter, and soft clipping. The worklet hosts a bounded voice list and a delay buffer. A shared sequence uses the native revision protocol.";
bindRoom();
let context,
  node,
  recorder,
  recorded = [],
  ready = false,
  playing = false,
  lastMetrics,
  session;
const pitch = [48, 50, 52, 55, 57, 60, 62, 64],
  names = ["C3", "D3", "E3", "G3", "A3", "C4", "D4", "E4"];
function refresh() {
  if (!session?.cells) return;
  const cells = session.cells,
    bank = Number($("bank").value);
  $("sequence").replaceChildren();
  for (let row = 7; row >= 0; row--)
    for (let step = bank; step < bank + 4; step++) {
      const at = step * 8 + row,
        b = document.createElement("button");
      b.textContent = `${names[row]} · ${step + 1}`;
      b.style.minHeight = "44px";
      b.classList.toggle("active", !!cells[at]);
      b.setAttribute("aria-label", `${names[row]}, step ${step + 1}`);
      b.setAttribute("aria-pressed", String(!!cells[at]));
      b.dataset.step = step;
      b.onclick = () => session.edit([at], [cells[at] ? 0 : 1]);
      $("sequence").append(b);
    }
  for (const [id, at] of [
    ["tempo", 128],
    ["wave", 129],
    ["cutoff", 130],
    ["delay", 131],
    ["drive", 132],
  ])
    $(id).value = String(cells[at]);
  $("tempo-value").textContent = `${cells[128]} BPM`;
  node?.port.postMessage({ kind: "cells", cells });
}
session = new Session("patchbay", refresh);
async function enable() {
  if (context) {
    await context.resume();
    return;
  }
  if (!window.AudioWorkletNode)
    throw Error("This browser needs AudioWorklet support for live synthesis.");
  context = new AudioContext({ latencyHint: "interactive" });
  await context.audioWorklet.addModule("/labs/patchbay-worklet.mjs");
  const r = await fetch("/labs/patchbay.wasm");
  if (!r.ok) throw Error("Cannot load Fern audio module.");
  node = new AudioWorkletNode(context, "fern-studio", {
    numberOfInputs: 0,
    numberOfOutputs: 1,
    outputChannelCount: [2],
  });
  node.connect(context.destination);
  node.port.onmessage = ({ data }) => {
    if (data.kind === "ready") {
      ready = true;
      notice("Audio enabled.");
      refresh();
    }
    if (data.kind === "error") error(Error(data.message));
    if (data.kind === "metrics") {
      lastMetrics = data;
      for (const b of $("sequence").children)
        b.classList.toggle("beat", Number(b.dataset.step) === data.step);
      measures({
        Voices: data.voices,
        "Quantum budget": data.deadline.toFixed(2) + " ms",
        "Work p95 (1 ms clock)": data.p95 + " ms",
        p99: data.p99 + " ms",
        "Frame gaps": data.gaps,
        "Output latency":
          ((context.baseLatency + (context.outputLatency || 0)) * 1000).toFixed(
            1,
          ) + " ms",
        "Wasm memory": (data.memory / 1048576).toFixed(2) + " MiB",
        "Network p95": percentiles(session.latencies).p95.toFixed(1) + " ms",
      });
    }
  };
  node.port.postMessage({ kind: "init", bytes: await r.arrayBuffer() });
  await context.resume();
}
action("Enable audio", enable, "enable-audio");
action(
  "Play loop",
  async () => {
    await enable();
    if (!ready) {
      notice("Audio module is loading. Try Play loop in a moment.");
      return;
    }
    playing = !playing;
    node.port.postMessage({ kind: "play", value: playing });
    $("play").textContent = playing ? "Stop loop" : "Play loop";
  },
  "play",
);
for (let i = 0; i < pitch.length; i++) {
  const b = document.createElement("button");
  b.textContent = names[i];
  b.onpointerdown = async (e) => {
    b.setPointerCapture(e.pointerId);
    await enable();
    node?.port.postMessage({ kind: "note", note: pitch[i], id: i });
  };
  b.onpointerup = b.onpointercancel = () =>
    node?.port.postMessage({ kind: "release", id: i });
  b.onclick = (e) => {
    if (e.detail === 0)
      enable()
        .then(() => {
          node?.port.postMessage({ kind: "note", note: pitch[i], id: i });
          setTimeout(
            () => node?.port.postMessage({ kind: "release", id: i }),
            200,
          );
        })
        .catch(error);
  };
  $("piano").append(b);
}
for (const [id, at] of [
  ["tempo", 128],
  ["wave", 129],
  ["cutoff", 130],
  ["delay", 131],
  ["drive", 132],
])
  $(id).onchange = () => session.edit([at], [Number($(id).value)]);
$("bank").onchange = refresh;
$("polyphony").onchange = () =>
  node?.port.postMessage({
    kind: "polyphony",
    value: Number($("polyphony").value),
  });
$("chord").onclick = async () => {
  try {
    await enable();
    const count = Number($("polyphony").value);
    node.port.postMessage({ kind: "polyphony", value: count });
    for (let i = 0; i < count; i++)
      node.port.postMessage({ kind: "note", note: 36 + (i % 36), id: 100 + i });
    setTimeout(() => {
      for (let i = 0; i < count; i++)
        node.port.postMessage({ kind: "release", id: 100 + i });
    }, 1500);
  } catch (e) {
    error(e);
  }
};
action(
  "Record loop",
  async () => {
    await enable();
    if (recorder?.state === "recording") {
      recorder.stop();
      $("record").textContent = "Record loop";
      return;
    }
    if (!window.MediaRecorder)
      throw Error(
        "Recording is unavailable in this browser. Export the project to retain the notes.",
      );
    const destination = context.createMediaStreamDestination();
    node.connect(destination);
    recorded = [];
    recorder = new MediaRecorder(destination.stream);
    recorder.ondataavailable = (e) => {
      if (e.data.size) recorded.push(e.data);
    };
    recorder.onstop = () => {
      download(
        "patchbay-loop." + (recorder.mimeType.includes("mp4") ? "mp4" : "webm"),
        new Blob(recorded, { type: recorder.mimeType }),
        recorder.mimeType,
      );
      node.disconnect(destination);
      destination.stream.getTracks().forEach((t) => t.stop());
    };
    recorder.start();
    $("record").textContent = "Stop and save recording";
  },
  "record",
);
action("Export project", () =>
  download(
    "patchbay.json",
    JSON.stringify({ app: "patchbay", cells: Array.from(session.cells || []) }),
  ),
);
fileInput(".json", async (file) => {
  if (file.size > 16384) throw Error("Project exceeds 16 KiB.");
  const data = JSON.parse(await file.text());
  if (
    data.app !== "patchbay" ||
    !Array.isArray(data.cells) ||
    data.cells.length !== 512 ||
    data.cells.some((x) => !Number.isInteger(x) || x < 0 || x > 20000)
  )
    throw Error("Invalid Patchbay project.");
  for (let i = 0; i < 512; i += 128) {
    while (session.pending.size > 256)
      await new Promise((r) => setTimeout(r, 30));
    session.edit(
      data.cells.slice(i, i + 128).map((_, j) => i + j),
      data.cells.slice(i, i + 128),
    );
  }
});
addEventListener(
  "pagehide",
  () => {
    if (recorder?.state === "recording") recorder.stop();
    node?.disconnect();
    context?.close();
  },
  { once: true },
);
