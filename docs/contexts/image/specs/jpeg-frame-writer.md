# JPEG baseline frame writer

**Status:** Internal prerequisite in progress.
**Context:** Fern-native image engine.
**Date:** 2026-11-07.

## Problem

The entropy writer produces scan bytes without a complete JPEG frame.
An image encoder also needs checked marker segments and quantizer declarations.

## Intended behavior

Serialize complete one-component baseline JPEG frames from explicit quantized blocks and caller tables.
Preserve natural block order and transmitted quantizer order.
Use the entropy writer for canonical codes, byte escapes, bit padding and restart markers.

## Implementation decisions

- Serialize JFIF, DQT, SOF0, DHT, optional DRI, SOS and EOI in Fern.
- Require complete blocks for the padded image dimensions.
- Require valid nonzero eight-bit quantizers and explicit DC/AC tables.
- Check dimensions and byte limits before an output can exceed its allowance.
- Return cancellation without a partial public result. Retain input aliases.
- Keep public image controls and alpha policy separate from this internal serializer.

## Validation

Recover coefficients through the Fern decoder and offline libjpeg.
Compare complete emitted bytes with an independent Python marker and entropy reference.
Test odd dimensions, restarts, malformed inputs, exact byte limits, cancellation and aliases.
Run native and Wasm checks before broader suite qualification.

## Remaining scope

This prerequisite does not complete JPEG or expose a public pixel encoder.
Pixel assembly, color, precision and other JPEG modes remain active requirements.
No speed or physical memory claim follows from this stage.
