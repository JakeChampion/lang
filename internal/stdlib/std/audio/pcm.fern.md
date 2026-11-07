# Integer PCM audio

Samples use interleaved, signed, right-aligned `i32` values at the declared bit depth.
A frame contains one sample per channel. Limits count samples across all channels.

The modules perform no file, device, or network I/O.

```fern
<<*>>=
import "core/cmp";
import "core/error";

@derive(cmp.Eq, cmp.Debug)
pub enum AudioError { Header, Truncated, Unsupported, Limit, Sample, Checksum, Buffer }

impl error.Error for AudioError {}

impl cmp.Display for AudioError {}

pub function (e: AudioError) message(): string {
  return match (e) {
    Header => "invalid audio header or frame",
    Truncated => "incomplete audio input",
    Unsupported => "unsupported audio format",
    Limit => "audio exceeds the configured limit",
    Sample => "sample is outside the declared bit depth",
    Checksum => "audio checksum mismatch",
    Buffer => "audio buffer is too small",
  };
}

pub function (e: AudioError) to_string(): string {
  return e.message();
}

@derive(cmp.Eq, cmp.Debug)
pub struct Format { sample_rate: i32, channels: i32, bits_per_sample: i32 }

pub struct Block { format: Format, samples: i32[] }

pub struct Limits { max_input_bytes: i32, max_samples: i32 }

pub function default_limits(): Limits {
  return Limits { max_input_bytes: 16777216, max_samples: 1048576 };
}

pub function valid(format: Format): boolean {
  return format.sample_rate > 0 && format.sample_rate <= 16777215
    && format.channels > 0 && format.channels <= 8
    && format.bits_per_sample > 0 && format.bits_per_sample <= 32;
}

pub function check_input(input: [u8], limits: Limits): Result[void, AudioError] {
  if (limits.max_input_bytes < 0 || limits.max_samples < 0 || input.len() > limits.max_input_bytes) {
    return Err(Limit);
  }
  return Ok(());
}

pub function sample_count(frames: i32, channels: i32, limits: Limits): Result[i32, AudioError] {
  if (frames < 0 || channels < 1 || channels > 8 || limits.max_samples < 0) {
    return Err(Limit);
  }
  let Some(count) = frames *? channels else {
    return Err(Limit);
  };
  if (count > limits.max_samples) {
    return Err(Limit);
  }
  return Ok(count);
}

pub function fits(sample: i32, bits: i32): boolean {
  if (bits < 1 || bits > 32) {
    return false;
  }
  let bound = 1i64 << (bits - 1) as i64;
  return sample as i64 >= -bound && sample as i64 < bound;
}

pub function validate(samples: [i32], format: Format, limits: Limits): Result[i32, AudioError] {
  if (!valid(format) || samples.len() % format.channels != 0) {
    return Err(Header);
  }
  let frames = samples.len() / format.channels;
  let _ = sample_count(frames, format.channels, limits)?;
  for s in samples {
    if (!fits(s, format.bits_per_sample)) {
      return Err(Sample);
    }
  }
  return Ok(frames);
}

pub function zeros(n: i32): i32[] {
  let out: i32[] = [];
  for _ in 0..n {
    out = out.append(0);
  }
  return out;
}
```
