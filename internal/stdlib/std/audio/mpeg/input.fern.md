# Sequential MPEG Layer I and II playback

Create a Layer I or Layer II input and call `next` with a borrowed Reader or `next_with` with a bounded byte-source callback. Each successful read returns one frame of interleaved PCM in the caller's buffer. Pass that buffer back on the next call. The sample rate and channel count stay fixed throughout the raw stream. A partial final frame is an error. An empty stream or a complete final frame followed by EOF ends playback. The caller owns and closes the source.

The encoded window is bounded by the source limits. Initial free-format discovery requires room for two frames and a following header. Source errors and codec errors retain their distinct types.

```fern
<<*>>=
import "std/audio/mp1";
import "std/audio/mp2";
import "std/audio/mp2/header";
import "std/audio/mp3/framing";
import "std/audio/pcm";
import "std/audio/source";
import "std/i32";

enum Codec { One(mp1.Decoder), Two(mp2.Decoder, mp2.Workspace) }

pub opaque struct Input { buffer: source.Buffer, codec: Codec, limits: pcm.Limits, free: i32, rate: i32, channels: i32 }

pub struct Frame { rate: i32, channels: i32, frames: i32, samples: f32[] }

pub function layer1(limits: pcm.Limits, window: source.Limits): Result[Input, source.ReadError] {
  return Ok(Input { buffer: source.create(window)?, codec: One(mp1.decoder()), limits: limits, free: 0, rate: 0, channels: 0 });
}

pub function layer2(limits: pcm.Limits, window: source.Limits): Result[Input, source.ReadError] {
  return Ok(Input { buffer: source.create(window)?, codec: Two(mp2.decoder(), mp2.workspace()), limits: limits, free: 0, rate: 0, channels: 0 });
}

function fill(own b: source.Buffer, needed: i32, read: (i32) => Result[u8[], IoError]): Result[source.Buffer, source.ReadError] {
  while (b.available() < needed && !b.eof) {
    let (next, _) = b.ensure(needed, read)?;
    b = next;
  }
  return Ok(b);
}

function extent(input: [u8], one: boolean, free: i32, maximum: i32): Result[i32, pcm.AudioError] {
  if (one) {
    return mp1.frame_bytes(input, free, maximum);
  }
  let h = header.parse(input, 0)?;
  if (free == 0) {
    return Ok(h.bytes);
  }
  let sized = header.resolve(input, 0, h, free, maximum)?;
  return Ok(sized.bytes);
}

function discover(own b: source.Buffer, maximum: i32, read: (i32) => Result[u8[], IoError]): Result[(source.Buffer, i32), source.ReadError] {
  let h = b.view();
  let search = framing.layered(h[1] as i32 >> 1 & 3, h[1] as i32 >> 3 & 3, h[2] as i32 >> 2 & 3, h[3] >> 6u8 == 3u8, h[2] as i32 >> 1 & 1, (h[1] & 1u8) == 0u8, 0);
  while (true) {
    let probe = match (framing.probe(b.view(), search, maximum, b.eof)) { Ok(p) => p, Err(e) => { return Err(source.ReadAudio(e)); } };
    match (probe) {
      framing.Found(n) => {
        return Ok((b, n));
      },
      framing.Pending(next) => {
        search = next;
      },
      framing.Need(next, count) => {
        search = next;
        b = fill(b, count, read)?;
      }
    }
  }
}

function decode(own codec: Codec, packet: [u8], own output: f32[], limits: pcm.Limits): Result[(Codec, Frame), source.ReadError] {
  match (codec) {
    One(d) => {
      let (next, result) = d.decode_packet_into(packet, output, limits);
      let f = match (result) { Ok(v) => v, Err(e) => { return Err(source.ReadAudio(e)); } };
      return Ok((One(next), Frame { rate: f.sample_rate, channels: f.channels, frames: 384, samples: f.samples }));
    },
    Two(d, w) => {
      let (next, work, result) = d.decode_packet_with(packet, output, w, limits);
      let f = match (result) { Ok(v) => v, Err(e) => { return Err(source.ReadAudio(e)); } };
      return Ok((Two(next, work), Frame { rate: f.sample_rate, channels: f.channels, frames: f.frames, samples: f.samples }));
    }
  }
}

function packet(own buffer: source.Buffer, one: boolean, free: i32, limits: pcm.Limits, read: (i32) => Result[u8[], IoError]): Result[(source.Buffer, i32, i32), source.ReadError] {
  let size = match (extent(buffer.view(), one, free, limits.max_input_bytes)) { Ok(n) => n, Err(e) => { return Err(source.ReadAudio(e)); } };
  if (size == 0) {
    let (next, base) = discover(buffer, limits.max_input_bytes, read)?;
    buffer = next;
    free = base;
    size = base + (buffer.view()[2] as i32 >> 1 & 1) * (if (one) { 4 } else { 1 });
  }
  if (size > limits.max_input_bytes) {
    return Err(source.ReadAudio(pcm.Limit));
  }
  buffer = fill(buffer, size, read)?;
  if (buffer.available() < size) {
    return Err(source.ReadAudio(pcm.Truncated));
  }
  return Ok((buffer, free, size));
}

function consistent(rate: i32, channels: i32, frame: Frame): boolean {
  return rate == 0 || frame.rate == rate && frame.channels == channels;
}

pub function (own state: Input) next_with(read: (i32) => Result[u8[], IoError], own output: f32[]): Result[(Input, Option[Frame]), source.ReadError] {
  let Input { buffer, codec, limits, free, rate, channels } = state;
  buffer = fill(buffer, 4, read)?;
  if (buffer.available() == 0) {
    return Ok((Input { buffer: buffer, codec: codec, limits: limits, free: free, rate: rate, channels: channels }, None));
  }
  let one = match (codec) { One(_) => true, Two(_, _) => false };
  let (ready, base, size) = packet(buffer, one, free, limits, read)?;
  let (next, frame) = decode(codec, ready.view()[:size], output, limits)?;
  if (!consistent(rate, channels, frame)) {
    return Err(source.ReadAudio(pcm.Header));
  }
  return Ok((Input { buffer: ready.advance(size)?, codec: next, limits: limits, free: base, rate: frame.rate, channels: frame.channels }, Some(frame)));
}

pub function (own state: Input) next(reader: Reader, own output: f32[]): Result[(Input, Option[Frame]), source.ReadError] {
  return state.next_with((n: i32): Result[u8[], IoError] => reader.read_chunk_bytes(n), output);
}
```
