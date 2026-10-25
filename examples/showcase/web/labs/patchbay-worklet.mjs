class FernStudio extends AudioWorkletProcessor {
  constructor() {
    super();
    this.api = null;
    this.cells = new Int32Array(512);
    this.cells[128] = 110;
    this.cells[130] = 6000;
    this.playing = false;
    this.voices = [];
    this.sequence = 0;
    this.step = -1;
    this.filter = 0;
    this.delay = new Float32Array(sampleRate * 2);
    this.delayAt = 0;
    this.blocks = 0;
    this.times = [];
    this.expected = null;
    this.gaps = 0;
    this.polyphony = 16;
    this.port.onmessage = async ({ data }) => {
      try {
        if (data.kind === "init") {
          const result = await WebAssembly.instantiate(data.bytes, {
            wasi_snapshot_preview1: {
              proc_exit(code) {
                throw Error("Fern audio exited: " + code);
              },
              fd_write() {
                return 8;
              },
            },
          });
          this.api = result.instance.exports;
          this.port.postMessage({
            kind: "ready",
            memory: this.api.memory.buffer.byteLength,
          });
        } else if (data.kind === "cells")
          this.cells = Int32Array.from(data.cells);
        else if (data.kind === "play") {
          this.playing = data.value;
          this.step = -1;
          this.sequence = 0;
        } else if (data.kind === "note") {
          this.voices.push({
            note: data.note,
            start: currentFrame,
            release: null,
            id: data.id,
          });
          if (this.voices.length > this.polyphony) this.voices.shift();
        } else if (data.kind === "release") {
          for (const v of this.voices)
            if (v.id === data.id) v.release = currentFrame;
        } else if (data.kind === "polyphony")
          this.polyphony = Math.max(1, Math.min(64, data.value));
      } catch (e) {
        this.port.postMessage({ kind: "error", message: e.message });
      }
    };
  }
  process(inputs, outputs) {
    const output = outputs[0];
    if (!output?.length) return true;
    const frames = output[0].length;
    if (!this.api) {
      for (const channel of output) channel.fill(0);
      return true;
    }
    const at = Date.now(),
      sample = this.api["fern:showcase/patchbay#sample"],
      filter = this.api["fern:showcase/patchbay#filter"],
      drive = this.api["fern:showcase/patchbay#drive"];
    if (this.expected !== null && currentFrame !== this.expected) this.gaps++;
    this.expected = currentFrame + frames;
    const notes = [48, 50, 52, 55, 57, 60, 62, 64],
      tempo = Math.max(40, Math.min(240, this.cells[128] || 110)),
      stepFrames = (sampleRate * 60) / tempo / 4;
    for (let i = 0; i < frames; i++) {
      const frame = currentFrame + i;
      if (this.playing) {
        const step = Math.floor(this.sequence / stepFrames) % 16;
        if (step !== this.step) {
          this.step = step;
          for (let row = 0; row < 8; row++)
            if (this.cells[step * 8 + row])
              this.voices.push({
                note: notes[row],
                start: frame,
                release: frame + stepFrames * 0.7,
                id: null,
              });
          while (this.voices.length > this.polyphony) this.voices.shift();
        }
        this.sequence++;
      }
      let value = 0;
      for (const voice of this.voices) {
        const age = (frame - voice.start) / sampleRate,
          release =
            voice.release === null || frame < voice.release
              ? -1
              : (frame - voice.release) / sampleRate;
        value += sample(
          frame - voice.start,
          sampleRate,
          voice.note,
          this.cells[129] || 0,
          age,
          release,
        );
      }
      this.filter = filter(
        value,
        this.filter,
        this.cells[130] || 6000,
        sampleRate,
      );
      const delayFrames = Math.round(sampleRate * 0.25),
        read =
          (this.delayAt - delayFrames + this.delay.length) % this.delay.length,
        wet = this.delay[read],
        mix = Math.max(0, Math.min(0.65, (this.cells[131] || 0) / 100));
      value = drive(
        this.filter + wet * mix,
        Math.max(1, (this.cells[132] || 5) / 5),
      );
      this.delay[this.delayAt] = this.filter + wet * 0.4;
      this.delayAt = (this.delayAt + 1) % this.delay.length;
      for (const channel of output) channel[i] = value;
    }
    this.voices = this.voices.filter(
      (v) =>
        (currentFrame + frames - v.start) / sampleRate < 5 &&
        (v.release === null || (currentFrame - v.release) / sampleRate < 0.5),
    );
    this.times.push(Date.now() - at);
    if (this.times.length > 256) this.times.shift();
    if (++this.blocks % 64 === 0) {
      const t = [...this.times].sort((a, b) => a - b);
      this.port.postMessage({
        kind: "metrics",
        voices: this.voices.length,
        p95: t[Math.floor(t.length * 0.95)] || 0,
        p99: t[Math.floor(t.length * 0.99)] || 0,
        deadline: (frames / sampleRate) * 1000,
        gaps: this.gaps,
        step: this.step,
        memory: this.api.memory.buffer.byteLength,
      });
    }
    return true;
  }
}
registerProcessor("fern-studio", FernStudio);
