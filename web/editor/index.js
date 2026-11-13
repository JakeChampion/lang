// Load the editor modules as separate jobs before the page uses their exports.
const [state, view, commands, language, autocomplete, lint, fern] = await Promise.all([
  import("./modules/state.js"),
  import("./modules/view.js"),
  import("./modules/commands.js"),
  import("./modules/language.js"),
  import("./modules/autocomplete.js"),
  import("./modules/lint.js"),
  import("./modules/fern.js"),
]);

export const {EditorState, Compartment} = state;
export const {EditorView, keymap, lineNumbers, highlightActiveLine, drawSelection,
  rectangularSelection, hoverTooltip, dropCursor} = view;
export const {history, defaultKeymap, historyKeymap, indentWithTab} = commands;
export const {bracketMatching, indentOnInput} = language;
export const {autocompletion, completionKeymap, closeBrackets, closeBracketsKeymap} = autocomplete;
export const {linter, lintGutter, forceLinting} = lint;
export const {fernLanguage, fernHighlight} = fern;
