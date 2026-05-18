// Bombadil specification for the lang playground.
//
// Bombadil is a property-based browser tester (antithesishq/bombadil)
// that autonomously fuzzes the page: random clicks, random keystrokes,
// random scrolling, navigation back/forward. We export a set of
// "always-true" properties — Bombadil checks them in every state it
// explores and shouts when one breaks.
//
// Default action generators (clicks, typing, etc.) come from the
// re-export below; the spec focuses on what's true about the page,
// not what the test should do step-by-step.
//
// Run via web/test/run.sh; that script builds the wasm, starts a
// local server, downloads + invokes the bombadil binary against this
// spec.

import { extract, always } from "@antithesishq/bombadil";
export * from "@antithesishq/bombadil/defaults";

// ---- State extractors ----
//
// extract() reads a value out of the DOM. The returned object's
// .current always reflects the latest read, recomputed when the page
// state changes. Properties below read .current to assert invariants.

const statusText = extract(
  (s) => s.document.querySelector("#status")?.textContent ?? ""
);

const runButtonDisabled = extract((s) => {
  const btn = s.document.querySelector("#run") as HTMLButtonElement | null;
  return btn ? btn.disabled : true;
});

const outputText = extract(
  (s) => s.document.querySelector("#out")?.textContent ?? ""
);

const problemCountText = extract(
  (s) => s.document.querySelector("#problemCount")?.textContent ?? ""
);

const problemRealItemCount = extract((s) => {
  // The list always renders SOMETHING — either real <li> entries or
  // a single `.empty` placeholder. Filter out the placeholder so
  // "real diagnostics" is countable.
  const items = s.document.querySelectorAll("#problemList li");
  let n = 0;
  items.forEach((li) => {
    if (!li.classList.contains("empty")) n++;
  });
  return n;
});

const editorMounted = extract(
  (s) => s.document.querySelector("#srcMount .cm-editor") !== null
);

// ---- Invariants ----
//
// Each export marks a property Bombadil enforces on every state it
// reaches. A violation produces a counter-example trace + a sequence
// of actions that reproduces it.

// 1. The wasm runtime never reports a load failure. If the CDN goes
//    down or wasm_exec.js is broken, the status text would carry
//    "wasm load failed: …" and we'd want to know.
export const wasmNeverFailsToLoad = always(
  () => !statusText.current.includes("wasm load failed")
);

// 2. The output pane never surfaces a JS-side exception. Real lang
//    errors get a structured "[error] …" prefix; "JS error:" only
//    fires when the playground's own JS throws, which is a bug.
export const noJsExceptionsInOutput = always(
  () => !outputText.current.includes("JS error:")
);

// 3. The Problems count text never lies about the visible item count.
//    The empty-state placeholder always corresponds to count "0";
//    a non-zero count must equal the number of real <li> entries.
//    The check tolerates the count being missing while the page
//    is still booting (initial empty string).
export const problemCountMatchesItems = always(() => {
  const text = problemCountText.current;
  if (text === "") return true;
  const parsed = parseInt(text, 10);
  if (Number.isNaN(parsed)) return false;
  return parsed === problemRealItemCount.current;
});

// `notBooted` is the pre-wasm guard used by the eventually-true
// properties below. The HTML ships with `id="status"` carrying
// "loading runtime…" and the runtime swaps it for "ready · …" once
// wasm + LSP init finish, so the prefix is a stable boot sentinel.
// Bombadil samples states from t=0 (before any script has run);
// without this guard, properties about post-boot state would fire
// on the initial HTML and report false violations.
function notBooted(): boolean {
  return statusText.current.startsWith("loading");
}

// 4. Once mounted, the editor stays mounted. CodeMirror loads from a
//    CDN; a dropped fetch (or a setSrc() that accidentally tears the
//    EditorView down) would surface as the .cm-editor disappearing
//    after we've seen it.
export const editorStaysMountedAfterBoot = always(
  () => editorMounted.current || notBooted()
);

// 5. After boot, the Run button is enabled. The notBooted() guard
//    means the property is vacuously true while the runtime is
//    still loading.
export const runButtonReachableAfterBoot = always(
  () => !runButtonDisabled.current || notBooted()
);
