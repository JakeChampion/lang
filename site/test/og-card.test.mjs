// Unit tests for the layout of the social cards.

import assert from "node:assert/strict";
import { test } from "node:test";

import { cardSvg, fitTitle } from "../src/lib/og-card.mjs";

test("a short title takes the largest size on one line", () => {
  assert.deepEqual(fitTitle("Install"), { size: 92, lines: ["Install"] });
});

test("a long title shrinks until it fits in three lines", () => {
  const { size, lines } = fitTitle("Requests and responses with typed JSON bodies, forms and cookies");
  assert.ok(size < 92);
  assert.ok(lines.length <= 3);
});

test("the card has no text elements, so it needs no installed font", () => {
  const svg = cardSvg({ title: "Install", section: "Tutorial", host: "example.org" });
  assert.doesNotMatch(svg, /<text/);
  assert.match(svg, /<path d="M/);
});
