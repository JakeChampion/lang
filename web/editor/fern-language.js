// Fern highlighting for the playground editor, driven by the same TextMate
// grammar the VS Code extension and the docs site use, and coloured by the
// token themes the docs site gives Expressive Code.
import {StreamLanguage, HighlightStyle, syntaxHighlighting} from "@codemirror/language";
import {Tag} from "@lezer/highlight";
import grammar from "../../editors/vscode/syntaxes/fern.tmLanguage.json";
import themes from "./fern-themes.json";

function flatten(patterns) {
  return patterns.flatMap(p =>
    p.include ? flatten(grammar.repository[p.include.slice(1)].patterns) : [p]);
}

function compile(p) {
  return {
    scope: p.name,
    match: p.match && new RegExp(p.match, "y"),
    begin: p.begin && new RegExp(p.begin, "y"),
    end: p.end && new RegExp(p.end, "y"),
    inner: flatten(p.patterns ?? []).map(compile),
  };
}

const rules = flatten(grammar.patterns).map(compile);

// One tag per grammar scope. A stream token is a word, so the scope's dots
// become underscores in the token name.
const scopes = new Set();
(function collect(rs) {
  for (const r of rs) {
    scopes.add(r.scope);
    collect(r.inner);
  }
})(rules);
const tokenOf = scope => scope.replaceAll(".", "_");
const tokenTable = {};
for (const scope of scopes) tokenTable[tokenOf(scope)] = Tag.define();

function eat(stream, re) {
  if (!re) return false;
  re.lastIndex = stream.pos;
  const m = re.exec(stream.string);
  if (!m || m[0].length === 0) return false;
  stream.pos += m[0].length;
  return true;
}

export const fernLanguage = StreamLanguage.define({
  name: "fern",
  startState: () => ({open: null}),
  copyState: s => ({open: s.open}),
  token(stream, state) {
    const open = state.open;
    if (open) {
      for (const r of open.inner) {
        if (eat(stream, r.match)) return tokenOf(r.scope);
      }
      if (eat(stream, open.end)) state.open = null;
      else stream.next();
      return tokenOf(open.scope);
    }
    if (stream.eatSpace()) return null;
    for (const r of rules) {
      if (eat(stream, r.match)) return tokenOf(r.scope);
      if (eat(stream, r.begin)) {
        state.open = r;
        return tokenOf(r.scope);
      }
    }
    stream.next();
    return null;
  },
  tokenTable,
  languageData: {commentTokens: {line: "//"}},
});

// TextMate theme matching: the longest selector that is the scope itself or a
// dot-separated prefix of it wins.
function settingsFor(theme, scope) {
  let best = null;
  let bestLen = -1;
  for (const rule of theme.tokenColors) {
    const selectors = [].concat(rule.scope).flatMap(s => s.split(",")).map(s => s.trim());
    for (const sel of selectors) {
      if ((scope === sel || scope.startsWith(sel + ".")) && sel.length > bestLen) {
        best = rule.settings;
        bestLen = sel.length;
      }
    }
  }
  return best;
}

function highlightFor(theme) {
  const specs = [];
  for (const scope of scopes) {
    const s = settingsFor(theme, scope);
    if (!s) continue;
    specs.push({
      tag: tokenTable[tokenOf(scope)],
      color: s.foreground,
      fontStyle: s.fontStyle?.includes("italic") ? "italic" : undefined,
      fontWeight: s.fontStyle?.includes("bold") ? "bold" : undefined,
    });
  }
  return syntaxHighlighting(HighlightStyle.define(specs));
}

const highlights = {
  dark: highlightFor(themes.find(t => t.type === "dark")),
  light: highlightFor(themes.find(t => t.type === "light")),
};

export function fernHighlight(dark) {
  return dark ? highlights.dark : highlights.light;
}
