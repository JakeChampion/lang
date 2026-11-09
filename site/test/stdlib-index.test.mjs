// Unit tests for the standard-library function index.

import assert from "node:assert/strict";
import { test } from "node:test";

import { moduleSource, pageEntries } from "../src/lib/stdlib-index.mjs";

const page = [
  "# `std/json`",
  "",
  "## `eq`",
  "",
  "```fern",
  "pub function (self: JsonError) eq(other: JsonError): boolean",
  "```",
  "",
  "## `enum JsonValue`",
  "",
  "```fern",
  "pub enum JsonValue {",
  "```",
  "",
  "## `eq`",
  "",
  "```fern",
  "pub function (self: JsonValue) eq(other: JsonValue): boolean",
  "```",
].join("\n");

test("gives each declaration its name, heading id and signature", () => {
  assert.deepEqual(pageEntries("json", "std/json", page), [
    { name: "eq", module: "std/json", page: "json", id: "eq", signature: "pub function (self: JsonError) eq(other: JsonError): boolean" },
    { name: "JsonValue", module: "std/json", page: "json", id: "enum-jsonvalue", signature: "pub enum JsonValue {" },
    { name: "eq", module: "std/json", page: "json", id: "eq-1", signature: "pub function (self: JsonValue) eq(other: JsonValue): boolean" },
  ]);
});

test("finds a module's literate or plain source", () => {
  const files = new Set(["internal/stdlib/std/json.fern.md", "internal/stdlib/core/cmp.fern"]);
  assert.equal(moduleSource("std/json", (p) => files.has(p)), "internal/stdlib/std/json.fern.md");
  assert.equal(moduleSource("core/cmp", (p) => files.has(p)), "internal/stdlib/core/cmp.fern");
  assert.equal(moduleSource("std/none", (p) => files.has(p)), null);
});
