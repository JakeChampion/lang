# ISO audio sample entries

This module checks common audio-entry headers and self-contained data references.
Version-zero and version-one audio headers supply bounded child-box offsets.
Codec modules validate the decoder-specific child boxes.

```fern
<<*>>=
// Validate common ISO audio sample entries and data references.
import "std/audio/mp4/boxes";
import "std/audio/pcm";
import "std/audio/wire";

// Check the common header before any codec-specific child reads.
// fern-lint: allow cyclomatic-complexity
pub function header(input: [u8], entry: boxes.Box, kind: u32): Result[i32, pcm.AudioError] {
  if (entry.data < 0 || entry.end > input.len() || entry.end < entry.data) {
    return Err(pcm.Header);
  }
  if (entry.kind != kind) {
    return Err(pcm.Unsupported);
  }
  if (entry.end - entry.data < 28) {
    return Err(pcm.Truncated);
  }
  if (wire.be(input, entry.data, 6) != 0u64 || wire.be(input, entry.data + 6, 2) != 1u64) {
    return Err(pcm.Unsupported);
  }
  let version = wire.be(input, entry.data + 8, 2);
  let bytes = if (version == 0u64) { 28 } else if (version == 1u64) { 44 } else { return Err(pcm.Unsupported); };
  if (bytes > entry.end - entry.data) {
    return Err(pcm.Truncated);
  }
  return Ok(entry.data + bytes);
}

// Prove that the single sample data reference identifies this same file.
// fern-lint: allow cyclomatic-complexity
pub function data_reference(input: [u8], minf: [boxes.Box], budget: boxes.Budget): Result[boxes.Budget, pcm.AudioError] {
  let dinf = boxes.required(minf, "dinf")?;
  let (b1, children) = boxes.children(input, dinf.data, dinf.end, budget)?;
  let dref = boxes.required(children, "dref")?;
  if (boxes.full(input, dref, 8)? != 0 || wire.be(input, dref.data + 4, 4) != 1u64) {
    return Err(pcm.Unsupported);
  }
  let (b2, references) = boxes.children(input, dref.data + 8, dref.end, b1)?;
  if (references.len() != 1) {
    return Err(pcm.Header);
  }
  let reference = references[0];
  if (reference.kind != boxes.tag("url ") || reference.end - reference.data != 4 || wire.be(input, reference.data, 4) != 1u64) {
    return Err(pcm.Unsupported);
  }
  return Ok(b2);
}
```
