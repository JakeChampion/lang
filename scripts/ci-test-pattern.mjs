#!/usr/bin/env node
import { liveTests } from "./ci-test-selection.mjs";
const [pkg, fallback] = process.argv.slice(2);
if (!pkg || !fallback) throw new Error("Expected a package and its full test pattern.");
const selection = JSON.parse(process.env.FERN_CI_SELECTION || '{"full":true}');
if (selection.macosFull !== false) {
  console.log(fallback);
} else {
  const names = selection.darwinTests?.[pkg] || [];
  if (!Array.isArray(names) || names.some((name) => !/^Test[A-Za-z0-9_]+$/.test(name))) {
    throw new Error("Invalid Darwin test selection.");
  }
  const selected = names.length ? liveTests(pkg, names) : [];
  console.log(selected.length ? `^(${selected.join("|")})$` : "^$");
}
