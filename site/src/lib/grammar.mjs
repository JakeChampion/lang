/* The Fern TextMate grammar, shared by everything that highlights Fern.
 *
 * One source of truth: the grammar the VS Code extension ships. Imported from
 * the repo rather than copied, so editing the extension's grammar re-colours
 * the docs and the landing page together. (This also means the site cannot be
 * built outside the repository — a trade the config has always made.)
 *
 * A static import rather than `readFileSync`: this module is bundled for the
 * page build, and inside the bundle `import.meta.url` points at the chunk, not
 * at this file, so a path relative to it resolves under `dist/`.
 */
import fernTmLanguage from "../../../editors/vscode/syntaxes/fern.tmLanguage.json";

export const fernGrammar = fernTmLanguage;

/* Both halves of the site highlight with the same pair, so a snippet looks
 * the same on the landing page as in the docs. Shiki emits the light colours
 * inline and the dark ones as `--shiki-dark` custom properties; landing.css
 * and fern.css each switch on `[data-theme]`. */
export const CODE_THEMES = { light: "vitesse-light", dark: "vitesse-dark" };
