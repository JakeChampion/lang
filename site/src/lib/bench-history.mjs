// Every run of the coreutils bench and the startup bench, oldest first, with
// the machine that made it. A trend is only drawn between runs on the same
// machine: a run by hand on another computer is not comparable with a run on
// a GitHub-hosted runner.

import { loadBench } from "./coreutils-bench.mjs";
import { parseStartupLog } from "./startup-bench.mjs";

const RUNNER = "GitHub-hosted runner";

// The machine that a log names. The weekly workflows write the first form.
export function machineOf(text) {
  return /GitHub-hosted/.test(text.split("\n").slice(0, 4).join("\n")) ? RUNNER : "another machine (see the log)";
}

export function coreutilsRuns(logs) {
  return Object.keys(logs).sort().map((path) => {
    const b = loadBench({ [path]: logs[path] });
    return {
      file: b.file,
      date: b.date,
      machine: machineOf(logs[path]),
      workloads: b.summary.rows,
      vsGnu: b.summary.medianGnu,
      vsUutils: b.summary.medianUutils,
      fasterThanGnu: b.summary.fasterThanGnu,
      comparedGnu: b.summary.comparedGnu,
    };
  });
}

export function startupRuns(logs) {
  return Object.keys(logs).sort().map((path) => ({ ...parseStartupLog(path, logs[path]), machine: machineOf(logs[path]) }));
}

// The runs on the weekly runner: the only series that a chart may join.
export const onRunner = (runs) => runs.filter((r) => r.machine === RUNNER);
