# Wave lab: native audio from an HTTP request

The browser edits sixteen musical steps. Fern generates the audio samples and returns a standard WAV file.
The browser plays the response through an ordinary audio element.

This document is the source program. Fern expands the named sections in `<<*>>` before compilation.
The HTML source view links each section and each local import.

```fern
<<*>>=
<<Audio data>>
<<Validate a pattern>>
<<Write PCM samples>>
<<Build a WAV file>>
<<Cache audio responses>>
```

## Audio data

Each note is a scale index. The value -1 means a musical rest, not a parse failure.
The sample rate is 24000 Hz and the output contains one signed 16-bit channel.

```fern
<<Audio data>>=
// Render a sixteen-step melody as a standard mono PCM WAV file.
import "std/http";
import "std/result";
import "std/option";
import "std/array";
import "std/http/router";
import "std/platform";
import "std/i64";
import "std/number_parse";
import "std/f64";
import "std/crypto";
import "std/hex";
import "std/async";
import "./common";

pub const RATE: i32 = 24000;

const FREQUENCIES: f64[] = [261.625565, 293.664768, 329.627557, 391.995436, 440.0, 523.251131, 587.329536, 659.255114];

pub struct Pattern { notes: i32[], bpm: i32, voice: string }
```

## Validate a pattern

Require exactly sixteen steps, a tempo from 60 to 180, and a known oscillator.
Parse failures return `Result` with HTTP status 400. Validate before any sample allocation.

```fern
<<Validate a pattern>>=
pub function parse_pattern(req: HttpRequest): Result[Pattern, http.HttpError] {
  let bpm: i32 = common.integer(req, "bpm", 112, 60, 180)?;
  let voice: string = common.query(req, "voice", "sine");
  if (voice != "sine" && voice != "triangle" && voice != "square" && voice != "saw") {
    return Err(http.fail(400, "unknown oscillator"));
  }
  let notes: i32[] = [];
  let fields: string[] = common.query(req, "notes", "0,-1,2,-1,4,-1,5,4,2,-1,1,-1,0,2,4,-1").split(",");
  if (fields.len() != 16) {
    return Err(http.fail(400, "a pattern needs sixteen steps"));
  }
  for field in fields {
    let note: i64 = i64.parse_i64(field).map_err((e: number_parse.ParseNumberError): http.HttpError => http.fail(400, "invalid note"))?;
    if (note < -1 || note > 7) {
      return Err(http.fail(400, "note is outside the scale"));
    }
    notes = notes.append(note as i32);
  }
  return Ok(Pattern { notes: notes, bpm: bpm, voice: voice });
}
```

## Write PCM samples

WAV uses little-endian integers. The helper writes each byte explicitly so all Fern targets produce the same file.
Four oscillator formulas generate the waveform. An attack and release envelope reduces clicks at note boundaries.

```fern
<<Write PCM samples>>=
function little16(own bytes: u8[], at: i32, value: i32): u8[] {
  return bytes.with(at, (value & 255) as u8).with(at + 1, (value >> 8 & 255) as u8);
}

function little32(own bytes: u8[], at: i32, value: i32): u8[] {
  bytes = little16(bytes, at, value & 65535);
  return little16(bytes, at + 2, value >> 16);
}

function oscillator(phase: f64, voice: string): f64 {
  let p: f64 = phase - phase.floor();
  if (voice == "square") {
    if (p < 0.5) {
      return 0.7;
    }
    return -0.7;
  }
  if (voice == "saw") {
    return (p * 2.0 - 1.0) * 0.7;
  }
  if (voice == "triangle") {
    return (1.0 - 4.0 * (p - 0.5).abs()) * 0.9;
  }
  return (p * 6.283185307179586).sin();
}
```

## Build a WAV file

A sixteenth note lasts 15 divided by the tempo in seconds. Sixteen steps form one bar.
The 44-byte RIFF header describes PCM data. Each sample uses two bytes.
The loop yields every 4096 samples so another connection can run during synthesis.

```fern
<<Build a WAV file>>=
pub function render(pattern: Pattern): u8[] {
  let step: i32 = RATE * 15 / pattern.bpm;
  let samples: i32 = step * 16;
  let bytes: u8[] = __alloc_u8(44 + samples * 2);
  let magic: u8[] = "RIFF".bytes();
  let at: i32 = 0;
  while (at < 4) {
    bytes = bytes.with(at, magic[at]).with(8 + at, "WAVE".bytes()[at]).with(12 + at, "fmt ".bytes()[at]).with(36 + at, "data".bytes()[at]);
    at = at + 1;
  }
  bytes = little32(bytes, 4, 36 + samples * 2);
  bytes = little32(bytes, 16, 16);
  bytes = little16(bytes, 20, 1);
  bytes = little16(bytes, 22, 1);
  bytes = little32(bytes, 24, RATE);
  bytes = little32(bytes, 28, RATE * 2);
  bytes = little16(bytes, 32, 2);
  bytes = little16(bytes, 34, 16);
  bytes = little32(bytes, 40, samples * 2);
  at = 0;
  while (at < samples) {
    let note: i32 = pattern.notes[at / step];
    let value: f64 = 0.0;
    if (note >= 0) {
      let local: i32 = at % step;
      let t: f64 = local as f64 / (RATE as f64);
      let envelope: f64 = (local as f64 / 160.0).min(1.0) * ((step - local) as f64 / 700.0).min(1.0);
      value = oscillator(t * FREQUENCIES[note], pattern.voice) * envelope * 0.65;
    }
    bytes = little16(bytes, 44 + at * 2, (value * 32767.0) as i32);
    if (at % 4096 == 0 && async.in_task()) {
      let ready: i32 = async.wait_any([], 1);
    }
    at = at + 1;
  }
  return bytes;
}
```

## Cache audio responses

An ETag identifies the request parameters. A matching validator returns 304 before synthesis.
The response reports synthesis time and sample count. A download request adds an attachment header.

```fern
<<Cache audio responses>>=
pub function audio(req: HttpRequest): Result[HttpResponse, http.HttpError] {
  let pattern: Pattern = parse_pattern(req)?;
  let etag: string = "\"" + hex.encode(crypto.sha256(req.path.bytes())).take(24) + "\"";
  if (req.headers.get("If-None-Match").unwrap_or("") == etag) {
    return Ok(http.bytes(304, []).with_header("ETag", etag));
  }
  let started: i64 = now_ns();
  let bytes: u8[] = render(pattern);
  let response: HttpResponse = http.bytes(200, bytes).with_content_type("audio/wav").with_header("ETag", etag).with_header("Cache-Control", "public, max-age=3600").with_header("X-Fern-Audio-Samples", ((bytes.len() - 44) / 2).to_string()).with_header("Server-Timing", "synthesis;dur=" + ((now_ns() - started) as f64 / 1000000.0).to_string());
  if (common.query(req, "download", "0") == "1") {
    response = response.with_header("Content-Disposition", "attachment; filename=fern-loop.wav");
  }
  return Ok(response);
}
```

## Check the behavior

This example compiles against the same sections as the application. Run it with `fern -doctest`.

```fern test name=wave-example
<<Audio data>>
<<Write PCM samples>>
<<Build a WAV file>>
function main(): i32 {
  let bytes = render(Pattern { notes: [0, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1], bpm: 120, voice: "sine" });
  if (bytes.len() != 96044 || bytes[0] != b'R' || bytes[8] != b'W') { return 1; }
  return 0;
}
```
