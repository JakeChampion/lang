# Borrowed PCM playback

This module submits signed 16-bit MediaCodec output directly to an AAudio `Music` stream.
It reuses `std/android/audio/codec` and the synchronous AAudio write operation.
It creates no PCM array and does not repack the samples.
Android can still copy samples inside its audio implementation.

## Contracts

`write` returns the number of accepted frames, with all channels in each frame.
A result of zero means that the stream accepted no frames before the timeout.
The caller supplies the actual `FormatChanged` format and a live output buffer from the same decoder.
The method accepts native signed 16-bit PCM with the stream's sample rate and channel count.
The stream must come from `audio.music` and remain open throughout the call.

An explicit channel mask must describe mono or stereo. A zero mask leaves the layout unspecified.


`offset_frames` identifies the first frame to submit within the output buffer.
The complete buffer must contain whole channel frames.
The method checks the codec view against the platform buffer capacity before each write.
A negative platform result or an impossible accepted-frame count returns `Platform`.
Codec configuration buffers return `Header` because their bytes do not contain PCM.
The method does not release the output buffer or change the stream state.

Keep the decoder buffer live until all writes return.
After a partial write, add the accepted frames to `offset_frames` before the next call.
After the final write or a discarded buffer, release the output buffer exactly once.
Do not release, flush, close, or seek the decoder while a write uses its buffer.
After a seek, discard the old buffer and reset the offset for each new output buffer.

This path serves callers that explicitly control MediaCodec and AAudio.
Plex uses Media3 for managed audio and video playback.
The platform MediaPlayer path and the lower video-only controller remain separate APIs.
Host ABI tests do not measure hardware decode, device playback, power, or acoustic synchronization.

## Module assembly

```fern
<<*>>=
<<implementation>>
```

### `implementation`

```fern
<<implementation>>=
// This module submits borrowed MediaCodec PCM to an AAudio music stream.
import "std/android/audio";
import "std/android/audio/codec";
import "std/android/video";
import "std/video/frame";

function matches_format(stream: audio.Music, format: codec.Format): boolean {
  if (format.encoding != codec.PCM_I16 || format.sample_rate <= 0 || format.sample_rate != stream.rate || format.channels < 1 || format.channels > 2 || format.channels != stream.channels) {
    return false;
  }
  return format.channel_mask == 0 || format.channel_mask == (if (format.channels == 1) { 4 } else { 12 });
}

pub function write(stream: audio.Music, decoder: codec.Decoder, format: codec.Format, buffer: video.OutputBuffer, offset_frames: i32, timeout_ms: i32): Result[i32, frame.VideoError] {
  if (stream.ptr == 0 as usize || decoder.handle == 0 as usize) {
    return Err(frame.State);
  }
  if (timeout_ms < 0 || offset_frames < 0 || (buffer.flags & codec.FLAG_CODEC_CONFIG) != 0u32) {
    return Err(frame.Header);
  }
  if (!matches_format(stream, format)) {
    return Err(frame.Unsupported);
  }
  return submit(stream, decoder, format.channels, buffer, offset_frames, timeout_ms);
}

function submit(stream: audio.Music, decoder: codec.Decoder, channels: i32, buffer: video.OutputBuffer, offset_frames: i32, timeout_ms: i32): Result[i32, frame.VideoError] {
  video.inspect_output(decoder.handle, decoder.scratch, decoder.limits.max_output_bytes, buffer)?;
  let frame_bytes = channels * 2;
  if (buffer.size % frame_bytes != 0 || offset_frames > buffer.size / frame_bytes) {
    return Err(frame.Buffer);
  }
  let frames = buffer.size / frame_bytes - offset_frames;
  if (frames == 0) {
    return Ok(0);
  }
  unsafe {
    let first = __load_ptr(decoder.scratch) + (offset_frames * frame_bytes) as usize;
    let accepted = audio.c_write(stream.ptr, first, frames, timeout_ms as i64 * 1000000i64);
    if (accepted < 0 || accepted > frames) {
      return Err(frame.Platform);
    }
    return Ok(accepted);
  }
}
```
