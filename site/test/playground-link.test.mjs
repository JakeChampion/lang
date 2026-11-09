// Unit tests for the "Open in playground" rehype plugin.

import assert from "node:assert/strict";
import { test } from "node:test";

import { encodeHash, rehypePlaygroundLinks } from "../src/lib/ec-playground-link.mjs";

// The shape that Expressive Code renders: a div.expressive-code holding the
// pre and a copy button whose data-code joins the lines with U+007F.
const block = (language, code) => ({
  type: "element",
  tagName: "div",
  properties: { className: ["expressive-code"] },
  children: [
    { type: "element", tagName: "pre", properties: { dataLanguage: language }, children: [] },
    { type: "element", tagName: "button", properties: { dataCode: code.replace(/\n/g, "\u007f") }, children: [] },
  ],
});

test("links a fern block that defines main, after the block", () => {
  const code = "function main(): i32 {\n    return 0;\n}";
  const tree = { type: "root", children: [block("fern", code)] };
  rehypePlaygroundLinks()(tree);
  assert.equal(tree.children.length, 2);
  const a = tree.children[1].children[0];
  assert.equal(a.properties.href, `/playground/#src=${encodeHash(code)}`);
  assert.equal(Buffer.from(a.properties.href.split("#src=")[1], "base64url").toString(), code);
});

test("leaves a fragment and another language alone", () => {
  const tree = { type: "root", children: [block("fern", "let x = 1;"), block("rust", "fn main() {}")] };
  rehypePlaygroundLinks()(tree);
  assert.equal(tree.children.length, 2);
});
