import { instantiateCore } from "/assets/wasm.mjs";
let api,
  job = 0,
  active = null;
onmessage = async ({ data }) => {
  try {
    if (data.kind === "cancel") {
      job++;
      active = null;
      return;
    }
    if (data.kind !== "render") return;
    const id = ++job;
    if (!api) {
      const response = await fetch("/labs/prism.wasm");
      if (!response.ok) throw Error("Cannot load Fern renderer.");
      api = await instantiateCore(await response.arrayBuffer());
    }
    if (id !== job) return;
    const width = Math.max(32, Math.min(640, data.width | 0)),
      height = Math.round(width * 0.625),
      target = Math.max(1, Math.min(128, data.samples | 0)),
      lane = Math.max(0, Math.min(3, data.lane || 0)),
      lanes = Math.max(1, Math.min(4, data.lanes || 1)),
      row = Math.floor((height * lane) / lanes),
      stripeHeight = Math.floor((height * (lane + 1)) / lanes) - row,
      sums = new Float32Array(width * stripeHeight * 3),
      counts = new Uint16Array(width * stripeHeight),
      times = [];
    const state = {
      id,
      width,
      height,
      target,
      sums,
      counts,
      at: 0,
      sample: 0,
      rays: 0,
      cpu: 0,
      last: performance.now(),
      start: performance.now(),
      scene: data.scene | 0,
      angle: data.angle,
      exposure: data.exposure,
      times,
    };
    active = state;
    function publish(done) {
      const bytes = new Uint8ClampedArray(width * stripeHeight * 4);
      for (let i = 0; i < counts.length; i++) {
        for (let c = 0; c < 3; c++) {
          const light =
            (sums[i * 3 + c] / Math.max(1, counts[i]) / 255) * state.exposure;
          bytes[i * 4 + c] = Math.round(Math.sqrt(light / (1 + light)) * 255);
        }
        bytes[i * 4 + 3] = 255;
      }
      const sorted = [...times].sort((a, b) => a - b);
      postMessage(
        {
          kind: "image",
          requestID: data.requestID,
          job: id,
          width,
          height: stripeHeight,
          imageHeight: height,
          row,
          lane,
          lanes,
          bytes,
          samples: state.sample,
          progress: (state.sample + state.at / counts.length) / target,
          done,
          rays: state.rays,
          cpu: state.cpu,
          elapsed: performance.now() - state.start,
          p95: sorted[Math.floor(sorted.length * 0.95)] || 0,
          memory: api.memory.buffer.byteLength,
          hostBytes: sums.byteLength + counts.byteLength + bytes.byteLength,
        },
        [bytes.buffer],
      );
      state.last = performance.now();
    }
    function work() {
      if (id !== job || active !== state) return;
      const at = performance.now(),
        pixel = api["fern:showcase/prism#pixel"];
      let n = 0;
      while (state.sample < target) {
        const i = state.at,
          packed =
            pixel(
              i % width,
              Math.floor(i / width) + row,
              width,
              height,
              state.sample,
              state.scene,
              state.angle,
            ) >>> 0;
        sums[i * 3] += (packed >>> 20) & 1023;
        sums[i * 3 + 1] += (packed >>> 10) & 1023;
        sums[i * 3 + 2] += packed & 1023;
        counts[i]++;
        state.at++;
        state.rays++;
        if (state.at === counts.length) {
          state.at = 0;
          state.sample++;
        }
        if (++n % 32 === 0 && performance.now() - at >= 8) break;
      }
      const duration = performance.now() - at;
      state.cpu += duration;
      times.push(duration);
      if (times.length > 256) times.shift();
      const done = state.sample === target;
      if (done || performance.now() - state.last > 150) publish(done);
      if (!done) setTimeout(work, 0);
    }
    work();
  } catch (e) {
    postMessage({ kind: "error", message: e.message });
  }
};
