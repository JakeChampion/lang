// Unit tests for the nesting of the standard-library sidebar.

import assert from "node:assert/strict";
import { test } from "node:test";

import { stdlibItems } from "../src/lib/stdlib-sidebar.mjs";

const link = (s) => ({ label: s, link: `/stdlib/${s}/` });

test("sorts a group and nests each family under its nearest parent", () => {
  assert.deepEqual(stdlibItems(["opus_silk_tables", "zstd", "opus", "opus_silk", "opus_range", "base64"]), [
    link("base64"),
    {
      label: "opus",
      collapsed: true,
      items: [
        link("opus"),
        link("opus_range"),
        { label: "opus_silk", collapsed: true, items: [link("opus_silk"), link("opus_silk_tables")] },
      ],
    },
    link("zstd"),
  ]);
});

test("leaves a family with no parent module flat", () => {
  assert.deepEqual(stdlibItems(["quic_frames", "quic_conn"]), [link("quic_conn"), link("quic_frames")]);
});
