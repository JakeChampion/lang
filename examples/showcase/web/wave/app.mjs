import { $, status, toast, copy, bytes } from "/assets/ui.mjs";

const names = ["C4", "D4", "E4", "G4", "A4", "C5", "D5", "E5"];
const defaultNotes = [0, -1, 2, -1, 4, -1, 5, 4, 2, -1, 1, -1, 0, 2, 4, -1];
let generation = 0,
  controller;
let notes = [...defaultNotes],
  audio,
  audioUrl,
  renderedKey = "",
  loading = false,
  focused = 0,
  currentStep = -1;
const params = new URLSearchParams(location.hash.slice(1));
if (params.has("notes")) {
  const value = params.get("notes").split(",").map(Number);
  if (
    value.length === 16 &&
    value.every((n) => Number.isInteger(n) && n >= -1 && n <= 7)
  )
    notes = value;
}
if (+params.get("bpm") >= 60 && +params.get("bpm") <= 180)
  $("bpm").value = String(Math.round(+params.get("bpm")));
if (["sine", "triangle", "square", "saw"].includes(params.get("voice")))
  $("voice").value = params.get("voice");
for (const name of [...names].reverse()) {
  const label = document.createElement("span");
  label.textContent = name;
  $("note-labels").append(label);
}
for (let step = 1; step <= 16; step++) {
  const label = document.createElement("span");
  label.textContent = step;
  $("step-numbers").append(label);
}
const cells = [];
for (let row = 0; row < 8; row++)
  for (let step = 0; step < 16; step++) {
    const pitch = 7 - row,
      button = document.createElement("button");
    button.type = "button";
    button.dataset.step = step;
    button.dataset.pitch = pitch;
    button.setAttribute("aria-label", `Step ${step + 1}, ${names[pitch]}`);
    button.tabIndex = cells.length === 0 ? 0 : -1;
    button.onclick = () => {
      notes[step] = notes[step] === pitch ? -1 : pitch;
      changed();
    };
    button.onfocus = () => {
      cells[focused]?.setAttribute("tabindex", "-1");
      focused = cells.indexOf(button);
      button.tabIndex = 0;
    };
    cells.push(button);
    $("sequencer").append(button);
  }
$("sequencer").onkeydown = (event) => {
  let next = focused;
  if (event.key === "ArrowLeft")
    next = focused % 16 ? focused - 1 : focused + 15;
  else if (event.key === "ArrowRight")
    next = focused % 16 < 15 ? focused + 1 : focused - 15;
  else if (event.key === "ArrowUp")
    next = focused >= 16 ? focused - 16 : focused + 112;
  else if (event.key === "ArrowDown")
    next = focused < 112 ? focused + 16 : focused - 112;
  else return;
  event.preventDefault();
  cells[next].focus();
};
function query() {
  return new URLSearchParams({
    notes: notes.join(","),
    bpm: $("bpm").value,
    voice: $("voice").value,
  });
}
function update() {
  for (const button of cells) {
    button.setAttribute(
      "aria-pressed",
      String(notes[+button.dataset.step] === +button.dataset.pitch),
    );
    button.classList.toggle("current", +button.dataset.step === currentStep);
  }
  $("bpm-value").textContent = $("bpm").value;
  $("download").href = `/wave/render.wav?${query()}&download=1`;
}
function stop() {
  audio?.pause();
  if (audio) audio.currentTime = 0;
  currentStep = -1;
  update();
  $("play").textContent = "Play your loop ▶";
}
function changed() {
  generation++;
  controller?.abort();
  loading = false;
  $("play").disabled = false;
  stop();
  history.replaceState(null, "", `#${query()}`);
  status("connection", "Pattern changed · ready to render");
  $("audio-size").textContent = "Play to render this pattern.";
  update();
}
$("bpm").oninput = changed;
$("voice").onchange = changed;
$("stop").onclick = () => {
  stop();
  status("connection", "Loop stopped");
};
$("clear").onclick = () => {
  notes = Array(16).fill(-1);
  changed();
};
$("shuffle").onclick = () => {
  const random = crypto.getRandomValues(new Uint8Array(16));
  notes = [...random].map((n, i) =>
    i % 4 === 0 ? n % 6 : n % 3 === 0 ? -1 : n % 8,
  );
  changed();
};
$("share").onclick = () => {
  history.replaceState(null, "", `#${query()}`);
  copy(location.href);
};
function drawWave(buffer) {
  const data = new DataView(buffer),
    canvas = $("waveform"),
    context = canvas.getContext("2d"),
    samples = (buffer.byteLength - 44) / 2;
  context.clearRect(0, 0, canvas.width, canvas.height);
  context.strokeStyle = "#dce394";
  context.lineWidth = 1.5;
  context.beginPath();
  for (let x = 0; x < canvas.width; x++) {
    const start = Math.floor((x / canvas.width) * samples),
      end = Math.min(
        samples,
        Math.floor(((x + 1) / canvas.width) * samples) + 1,
      );
    let low = 0,
      high = 0;
    for (let i = start; i < end; i++) {
      const sample = data.getInt16(44 + i * 2, true) / 32768;
      low = Math.min(low, sample);
      high = Math.max(high, sample);
    }
    context.moveTo(x, 64 + low * 60);
    context.lineTo(x, 64 + high * 60);
  }
  context.stroke();
}
$("play").onclick = async () => {
  if (loading) return;
  const key = query().toString(),
    run = generation;
  try {
    if (key !== renderedKey || !audio) {
      loading = true;
      $("play").disabled = true;
      status("connection", "Fern is synthesizing your loop");
      controller = new AbortController();
      const response = await fetch(`/wave/render.wav?${key}`, {
        signal: controller.signal,
      });
      if (!response.ok)
        throw new Error(`Synthesis failed (${response.status})`);
      const buffer = await response.arrayBuffer();
      if (run !== generation) return;
      if (
        buffer.byteLength < 44 ||
        new DataView(buffer).getUint32(0) !== 0x52494646
      )
        throw new Error("Invalid WAV response");
      stop();
      if (audioUrl) URL.revokeObjectURL(audioUrl);
      audioUrl = URL.createObjectURL(new Blob([buffer], { type: "audio/wav" }));
      audio = new Audio(audioUrl);
      audio.loop = true;
      audio.volume = 0.6;
      renderedKey = key;
      drawWave(buffer);
      const duration = /synthesis;dur=([\d.]+)/.exec(
        response.headers.get("server-timing") || "",
      );
      $("render-ms").textContent = duration
        ? `${Number(duration[1]).toFixed(2)} ms`
        : "—";
      $("audio-size").textContent =
        `${bytes(buffer.byteLength)} · 24 kHz / 16-bit`;
    }
    await audio.play();
    status("connection", "Playing audio synthesized by Fern", true);
    $("play").textContent = "Playing your loop ♪";
  } catch (error) {
    if (error.name !== "AbortError" && run === generation) {
      status("connection", error.message, false, true);
      toast(error.message);
    }
  } finally {
    if (run === generation) {
      loading = false;
      $("play").disabled = false;
    }
  }
};
function animate() {
  const next =
    audio && !audio.paused && Number.isFinite(audio.duration)
      ? Math.min(15, Math.floor((audio.currentTime / audio.duration) * 16))
      : -1;
  if (next !== currentStep) {
    currentStep = next;
    update();
  }
  requestAnimationFrame(animate);
}
update();
requestAnimationFrame(animate);
addEventListener("pagehide", () => {
  stop();
  if (audioUrl) URL.revokeObjectURL(audioUrl);
});
