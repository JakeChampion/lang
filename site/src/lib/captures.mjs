// The compiler output that scripts/capture.mjs records at build time.
// A production build without it is an error. `astro dev` before a first
// build gets null, and the sections that use it render nothing.

const found = Object.values(
  import.meta.glob("../generated/captures.json", { eager: true, import: "default" }),
);

if (found.length === 0 && import.meta.env.PROD) {
  throw new Error("src/generated/captures.json is missing: run `node scripts/capture.mjs`");
}

export const captures = found[0] ?? null;

// Human names for the capabilities a language server reports, in the order
// the home page lists them. The server publishes diagnostics as the document
// syncs, so text sync is listed as diagnostics.
export const LSP_FEATURES = [
  ["textDocumentSync", "Diagnostics as you type", "read"],
  ["hoverProvider", "Hover: types and docs", "read"],
  ["definitionProvider", "Go to definition", "read"],
  ["referencesProvider", "Find references", "read"],
  ["documentSymbolProvider", "Document symbols", "read"],
  ["semanticTokensProvider", "Semantic highlighting", "read"],
  ["inlayHintProvider", "Inlay type hints", "read"],
  ["completionProvider", "Completion", "write"],
  ["signatureHelpProvider", "Signature help", "write"],
  ["renameProvider", "Rename", "write"],
  ["codeActionProvider", "Code actions", "write"],
  ["documentFormattingProvider", "Formatting", "write"],
];

// The features for the capabilities in `caps`. A capability with no name in
// the table keeps its protocol name, so a new one is never left out.
export function lspFeatures(caps) {
  const known = new Map(LSP_FEATURES.map(([k, name, group]) => [k, { name, group }]));
  const ordered = LSP_FEATURES.filter(([k]) => caps.includes(k)).map(([k]) => known.get(k));
  const unknown = caps.filter((k) => !known.has(k)).map((k) => ({ name: k, group: "write" }));
  return [...ordered, ...unknown];
}
