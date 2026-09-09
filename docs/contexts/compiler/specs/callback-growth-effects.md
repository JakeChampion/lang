# Callback growth effects

**Status:** Implemented; full integration validation pending
**Context(s):** Bootstrap ownership analysis and self-hosted frontend
**Date:** 2026-09-09

## Problem

A generic recursive visitor can grow a borrowed scope's array fields through a
callback. Treating that callback as having no growth effects lets in-place
updates corrupt an outer immutable scope. This blocks the Fern lexical-binding
identity work needed by the typed pre-RC migration.

## Solution

Function values without an effect contract must conservatively expose possible
growth of parameter-rooted arrays and struct fields. Resolve local callable
identity before consulting named-function summaries. Reuse the existing growth
domain, fixed point and caller-side protection. This fixes an unsafe assumption
in the bootstrap compiler; it introduces no new AST ownership optimization.

## Testing

Pin array, record, rename, field and nested-field effects; cover callbacks held
in parameters, locals, fields, arrays, captures and call results. Preserve direct
pure-call precision, including a control for a callback sharing a global name.

A recursive generic visitor regression independently asserts restored scope
visibility and accumulated binding identities. Start its arrays with spare
capacity so allocation cannot hide unsafe in-place reuse. Execute under the
interpreter, x86-64, ARM64 Linux, ARM64 Darwin and Wasm. Also check the original
Fern lexical resolver against its interpreter oracle.

## Measurement

Matched main/fix CI-harness builds measure the IR driver at 7,422,268/7,426,364
bytes and the compiler at 11,544,812/11,553,004 bytes. A repeated fixed build
reproduces both sizes. The CLI's symbol-bearing builds attribute 8,598 added text
bytes to 11 functions, mainly generic accumulator visitors. These are added
buffer-protection paths, not new runtime helpers. The existing size gate passes
for both measured drivers; all other drivers remain CI coverage. No baseline
changes. Runtime performance still needs CI measurement.

## Out of scope

This is not a complete effect model for arbitrary aggregate containment and
does not add an independent indirect-call lowering path. Typed semantic effects
remain the migration destination. The two enum-root cleanup failures are
separate and remain merge blockers. Track bootstrap convergence in #4451,
frontend identity in #8972, and this repair in #8978.
