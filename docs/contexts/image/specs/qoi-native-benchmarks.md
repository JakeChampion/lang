# Native QOI benchmark evidence

**Status:** Pipeline validation. Performance acceptance remains open.
**Context:** Fern-native image engine.
**Date:** 2026-11-07.

## Problem

Correctness tests do not establish codec speed or memory use.
The image objective needs reproducible comparisons against independent implementations.

## Solution

Compare the pure Fern QOI codec with the hash-pinned offline QOI reference.
Keep the reference outside the Fern runtime and its dependency graph.
Start with a small native pipeline check before a controlled performance campaign.
The first checkpoint reports observations without a performance rank.

## Implementation decisions

- Use native Apple Silicon macOS for this first harness.
- Compare RGB and RGBA input with both QOI color declarations.
- Use deterministic black, ramp and noise images with exact input hashes.
- Verify pixels and declarations through the independent reference before acceptance.
- Exclude process startup, file access, preflight and warmup from each inner timer.
- Use the same ARM virtual counter and integer nanosecond conversion in both release workers.
- Retain zero intervals with an explicit resolution flag. Do not infer a rate from them.
- Include codec allocation and release in the measured operation.
- Alternate release implementation order across fixtures and operations.
- Run the Fern sanitizer separately and require a balanced heap with zero live bytes.
- Retain the complete-process maximum RSS from Darwin time without unit conversion.
- Record source, tool, binary and input hashes, commands, host and observed load.
- Keep failed proof directories and reject source changes during a run.

## Validation

The first run uses side 16 and three iterations per operation.
The same pipeline handles a larger side without source changes.
Tests reject corrupt checksums, wrong sizes, invalid times and invalid memory observations.
They also reject missing or unbalanced sanitizer records.
Full codec correctness tests remain separate acceptance gates.

## Remaining acceptance

The host currently has other validation work in progress.
These smoke observations do not support a speed rank or a physical memory bound.
A controlled campaign needs repeated samples, host-load controls and representative image corpora.
It also needs additional competitive implementations before any fastest-in-the-world claim.
Full image feature completeness remains an independent requirement.
This harness changes no public codec API or runtime behavior.
