# Android video playback controller

This controller connects a file extractor, codec discovery, and surface decode.
It uses bounded packet buffers and retains at most one output buffer.
`tick` performs one input and one output operation without a blocking codec wait.
Call it from the Android loop thread with monotonic nanoseconds.

Pause stops further packet submission and output release.
A seek flushes outstanding buffers, restores codec configuration, and starts at a sync sample.
Frames before the requested media timestamp are discarded.
Runtime fallback reopens a software codec and seeks to a sync sample before its recovery point.
A strict hardware policy never falls back to software.

A seek sets the recovery point to the requested media time after a successful flush.
Each scheduled frame updates that point. Discarded frames do not update it.
Recovery before the first post-seek frame uses the requested time, including while paused.
The last scheduled frame timestamp remains available separately.

This controller plays one unencrypted video track. It does not play audio.
The surface and file descriptor must remain valid until `close`.
Close before the window destruction callback returns.
Each returned player value replaces the previous value.

`synchronize_audio` updates the presentation clock from an existing AAudio stream timestamp and an explicit audio media origin.
An unavailable timestamp leaves the clock unchanged. A rejected anchor leaves the player and pending output buffer unchanged.
The existing video timeline remains active until a valid audio anchor replaces it.

The method requires an active, unpaused player. It queries no codec buffers and performs no audio state or I/O operation.
It allocates timestamp scratch memory, so it must run outside a real-time audio callback.

The app supplies a playback stream and a frame-to-media origin for that stream.
The app keeps the audio stream open throughout each call.
The app must coordinate audio pause, resume, and seek. A video seek or software recovery clears the presentation clock.
The app must supply a new matching audio anchor after such a discontinuity.
This method does not add audio decode or establish physical lip-sync accuracy.

```fern
<<*>>=
// This module controls Android video presentation, pause, seek, and software recovery.
import "std/android/video";
import "std/android/video/capabilities";
import "std/android/video/extractor";
import "std/video/frame";
import "std/video/playback_clock";
import "std/android/audio";

pub struct Player { reader: extractor.Extractor, decoder: video.Decoder, decoder_live: boolean, format: video.CodecFormat, info: capabilities.CodecInfo, request: capabilities.Request, policy: capabilities.Policy, limits: frame.Limits, window: usize, clock: Option[playback_clock.Clock], pending: Option[video.OutputBuffer], paused: boolean, input_eos: boolean, output_eos: boolean, failed: boolean, config_next: i32, target_us: i64, last_input_us: i64, last_frame_us: i64, recovery_us: i64, fallback_used: boolean, scheduled_frames: i64, dropped_frames: i64 }

pub enum Event { Idle, FrameScheduled(i64), FrameDropped(i64), FormatChanged(video.OutputFormat), Recovered(capabilities.CodecInfo), End, Failure(frame.VideoError) }

pub struct Tick { player: Player, event: Event }

function audio_clock(stream: audio.Stream, origin: playback_clock.AudioOrigin): Result[Option[playback_clock.Clock], frame.VideoError] {
  if (origin.frame_position < 0i64 || origin.media_us < 0i64 || stream.rate <= 0) {
    return Err(frame.Header);
  }
  let Some(stamp) = stream.timestamp() else {
    return Ok(None);
  };
  let clock = playback_clock.from_audio(origin, stamp.frame_position, stream.rate, stamp.monotonic_ns)?;
  return Ok(Some(clock));
}

pub function (own player: Player) synchronize_audio(stream: audio.Stream, origin: playback_clock.AudioOrigin): Tick {
  if (!player.decoder_live || player.failed || player.paused || player.output_eos) {
    return Tick { player: player, event: Failure(frame.State) };
  }
  return match (audio_clock(stream, origin)) {
    Ok(Some(clock)) => Tick { player: Player { ...player, clock: Some(clock) }, event: Idle },
    Ok(None()) => Tick { player: player, event: Idle },
    Err(error) => Tick { player: player, event: Failure(error) },
  };
}

enum Feed { Waiting, Configuration, Submitted(i64), InputEnd }

function request_for(format: video.CodecFormat): Result[capabilities.Request, frame.VideoError] {
  return Ok(capabilities.Request { mime: format.text("mime", 128)?, width: format.integer("width", 0), height: format.integer("height", 0), encoder: false, frame_rate: format.integer("frame-rate", 0), bitrate: 0, profile: format.integer("profile", 0), level: format.integer("level", 0), color_format: 0 });
}

function open_track_decoder(env: usize, api_level: i32, format: video.CodecFormat, window: usize, policy: capabilities.Policy, limits: frame.Limits): Result[video.SelectedDecoder, frame.VideoError] {
  let request = request_for(format)?;
  if (!capabilities.valid(request)) {
    return Err(frame.Unsupported);
  }
  let infos = capabilities.discover(env, api_level, request, capabilities.default_limits())?;
  return match (video.select_format(format, window, request, infos, policy, limits)) { Ok(value) => Ok(value), Err(e) => Err(e.error) };
}

function open_track(env: usize, api_level: i32, reader: extractor.Extractor, track: i32, window: usize, policy: capabilities.Policy, limits: frame.Limits): Result[Player, frame.VideoError] {
  let format = reader.track_format(track)?;
  let selected = match (open_track_decoder(env, api_level, format, window, policy, limits)) { Ok(value) => value, Err(e) => { let _ = format.close(); return Err(e); } };
  match (reader.select_track(track)) {
    Err(e) => {
      let _ = selected.decoder.close();
      let _ = format.close();
      return Err(e);
    },
    Ok(_) => {}
  }
  let request = selected.info.request;
  return Ok(Player { reader: reader, decoder: selected.decoder, decoder_live: true, format: format, info: selected.info, request: request, policy: policy, limits: limits, window: window, clock: None, pending: None, paused: false, input_eos: false, output_eos: false, failed: false, config_next: 2, target_us: 0i64, last_input_us: 0i64, last_frame_us: 0i64, recovery_us: 0i64, fallback_used: false, scheduled_frames: 0i64, dropped_frames: 0i64 });
}

pub function open(env: usize, api_level: i32, fd: i32, offset: i64, length: i64, window: usize, policy: capabilities.Policy, limits: frame.Limits): Result[Player, frame.VideoError] {
  if (window == 0 as usize) {
    return Err(frame.Header);
  }
  let reader = extractor.open(fd, offset, length, limits.max_packet_bytes)?;
  let last_error = frame.Unsupported;
  for track in 0..reader.tracks {
    match (open_track(env, api_level, reader, track, window, policy, limits)) {
      Ok(value) => {
        return Ok(value);
      },
      Err(e) => {
        last_error = e;
      }
    }
  }
  let _ = reader.close();
  return Err(last_error);
}

pub function (own player: Player) pause(now_ns: i64): Tick {
  if (now_ns < 0i64) {
    return failure(player, frame.Header);
  }
  let clock = player.clock;
  match (clock) {
    Some(value) => {
      match (value.pause(now_ns)) {
        Ok(paused) => {
          clock = Some(paused);
        },
        Err(e) => {
          return failure(player, e);
        }
      }
    },
    None => {}
  }
  return Tick { player: Player { ...player, paused: true, clock: clock }, event: Idle };
}

pub function (own player: Player) resume(now_ns: i64): Tick {
  if (now_ns < 0i64) {
    return failure(player, frame.Header);
  }
  let clock = player.clock;
  match (clock) {
    Some(value) => {
      match (value.resume(now_ns)) {
        Ok(resumed) => {
          clock = Some(resumed);
        },
        Err(e) => {
          return failure(player, e);
        }
      }
    },
    None => {}
  }
  return Tick { player: Player { ...player, paused: false, clock: clock }, event: Idle };
}

pub function (own player: Player) seek(target_us: i64): Tick {
  if (!player.decoder_live) {
    return failure(player, frame.State);
  }
  if (target_us < 0i64) {
    return Tick { player: player, event: Failure(frame.Header) };
  }
  match (player.decoder.flush()) {
    Err(e) => {
      return failure(player, e);
    },
    Ok(_) => {}
  }
  player = Player { ...player, pending: None, clock: None, input_eos: false, output_eos: false, failed: false, target_us: target_us, last_input_us: target_us, recovery_us: target_us, config_next: 0 };
  match (player.reader.seek(target_us, extractor.PreviousSync)) {
    Err(e) => {
      return failure(player, e);
    },
    Ok(_) => {}
  }
  return Tick { player: player, event: Idle };
}

function feed_config(player: Player): Result[Feed, frame.VideoError] {
  let key = if (player.config_next == 0) { "csd-0" } else { "csd-1" };
  let bytes = player.format.buffer(key, player.limits.max_packet_bytes)?;
  if (bytes.len() == 0) {
    return Ok(Configuration);
  }
  let Some(slot) = player.decoder.input(0i64)? else {
    return Ok(Waiting);
  };
  player.decoder.submit(slot, bytes, 0i64, video.FLAG_CODEC_CONFIG)?;
  return Ok(Configuration);
}

function feed(player: Player): Result[Feed, frame.VideoError] {
  if (player.config_next < 2) {
    return feed_config(player);
  }
  let Some(slot) = player.decoder.input(0i64)? else {
    return Ok(Waiting);
  };
  let sample = player.reader.read_into(slot)?;
  match (sample) {
    Some(value) => {
      player.decoder.submit_input(slot, value.size, value.presentation_time_us, value.flags)?;
      let _ = player.reader.advance();
      return Ok(Submitted(value.presentation_time_us));
    },
    None => {
      player.decoder.submit_input(slot, 0, player.last_input_us, video.FLAG_END_OF_STREAM)?;
      return Ok(InputEnd);
    }
  }
}

function failure(own player: Player, error: frame.VideoError): Tick {
  return Tick { player: Player { ...player, failed: true }, event: Failure(error) };
}

function release(own player: Player, buffer: video.OutputBuffer, render: boolean, deadline: i64): Tick {
  let result = if (render) { player.decoder.release_at(buffer, deadline) } else { player.decoder.release(buffer, false) };
  match (result) {
    Err(e) => {
      return failure(player, e);
    },
    Ok(_) => {}
  }
  let ended = (buffer.flags & video.FLAG_END_OF_STREAM) != 0u32;
  let event = if (ended) { End } else if (render) { FrameScheduled(buffer.presentation_time_us) } else { FrameDropped(buffer.presentation_time_us) };
  if (render) {
    player = Player { ...player, last_frame_us: buffer.presentation_time_us, recovery_us: buffer.presentation_time_us };
  }
  return Tick { player: Player { ...player, pending: None, output_eos: ended, scheduled_frames: player.scheduled_frames + (if (render) { 1i64 } else { 0i64 }), dropped_frames: player.dropped_frames + (if (!render && buffer.size > 0) { 1i64 } else { 0i64 }) }, event: event };
}

function present(own player: Player, buffer: video.OutputBuffer, now_ns: i64): Tick {
  if (buffer.size == 0 || buffer.presentation_time_us < player.target_us) {
    return release(player, buffer, false, 0i64);
  }
  let clock = match (player.clock) {
    Some(value) => value,
    None => match (playback_clock.start(buffer.presentation_time_us, now_ns)) { Ok(value) => value, Err(e) => { return failure(player, e); } },
  };
  player = Player { ...player, clock: Some(clock), pending: Some(buffer) };
  let deadline = match (clock.deadline(buffer.presentation_time_us)) { Ok(value) => value, Err(e) => { return failure(player, e); } };
  return schedule(player, buffer, deadline, now_ns);
}

function schedule(own player: Player, buffer: video.OutputBuffer, deadline: i64, now_ns: i64): Tick {
  if (deadline > now_ns && deadline - now_ns > 5000000i64) {
    return Tick { player: player, event: Idle };
  }
  if (now_ns > deadline && now_ns - deadline > 100000000i64) {
    return release(player, buffer, false, 0i64);
  }
  return release(player, buffer, true, if (deadline < now_ns) { now_ns } else { deadline });
}

pub function (own player: Player) tick(now_ns: i64): Tick {
  if (now_ns < 0i64) {
    return failure(player, frame.Header);
  }
  if (player.failed) {
    return Tick { player: player, event: Failure(frame.State) };
  }
  if (player.output_eos) {
    return Tick { player: player, event: End };
  }
  if (player.paused) {
    return Tick { player: player, event: Idle };
  }
  let input = feed_tick(player);
  match (input.event) {
    Failure(_) => {
      return input;
    },
    _ => {}
  }
  return output_tick(input.player, now_ns);
}

function feed_tick(own player: Player): Tick {
  if (!player.input_eos) {
    match (feed(player)) {
      Err(e) => {
        return failure(player, e);
      },
      Ok(Configuration()) => {
        player = Player { ...player, config_next: player.config_next + 1 };
      },
      Ok(Submitted(pts)) => {
        player = Player { ...player, last_input_us: pts, config_next: 2 };
      },
      Ok(InputEnd()) => {
        player = Player { ...player, input_eos: true, config_next: 2 };
      },
      Ok(Waiting()) => {}
    }
  }
  return Tick { player: player, event: Idle };
}

function output_tick(own player: Player, now_ns: i64): Tick {
  match (player.pending) {
    Some(buffer) => {
      return present(player, buffer, now_ns);
    },
    None => {}
  }
  match (player.decoder.output(0i64)) {
    Err(e) => {
      return failure(player, e);
    },
    Ok(video.Buffer(buffer)) => {
      return present(player, buffer, now_ns);
    },
    Ok(video.FormatChanged(format)) => {
      return Tick { player: player, event: FormatChanged(format) };
    },
    _ => {
      return Tick { player: player, event: Idle };
    }
  }
}

// Recovery invalidates pending buffers and restarts from a sync sample.
pub function (own player: Player) recover_software(env: usize, api_level: i32): Tick {
  if (player.policy == capabilities.HardwareOnly || player.fallback_used) {
    return failure(player, frame.Unsupported);
  }
  let infos = match (capabilities.discover(env, api_level, player.request, capabilities.default_limits())) { Ok(value) => value, Err(e) => { return failure(player, e); } };
  let candidates: capabilities.CodecInfo[] = [];
  for info in infos {
    if (info.name != player.info.name) {
      candidates = candidates.append(info);
    }
  }
  if (player.decoder_live) {
    let _ = player.decoder.close();
  }
  player = Player { ...player, decoder_live: false, pending: None, clock: None, fallback_used: true };
  let selected = match (video.select_format(player.format, player.window, player.request, candidates, capabilities.SoftwareOnly, player.limits)) { Ok(value) => value, Err(e) => { return failure(player, e.error); } };
  player = Player { ...player, decoder: selected.decoder, decoder_live: true, info: selected.info, failed: false };
  let target = player.recovery_us;
  let result = player.seek(target);
  return match (result.event) { Failure(_) => result, _ => Tick { player: result.player, event: Recovered(selected.info) } };
}

pub function (own player: Player) close(): Result[void, frame.VideoError] {
  let decoder: Result[void, frame.VideoError] = if (player.decoder_live) { player.decoder.close() } else { Ok(()) };
  let reader = player.reader.close();
  let format = player.format.close();
  decoder?;
  reader?;
  format?;
  return Ok(());
}
```
