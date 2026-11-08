# SILK stereo reconstruction

This module decodes stereo prediction weights and reconstructs left and right PCM.
It retains two samples of history per coded channel and interpolates the prediction weights over eight milliseconds.
Mono output retains the reference one-sample delay.

The implementation derives from [Pion Opus](https://github.com/pion/opus/tree/e7b8aa55508cfd30a968dbc695d4272a8e04f2f2) and RFC 6716.
Copyright 2026 The Pion community. Copyright 2006–2011 Skype Limited.
The source uses the MIT and BSD-3-Clause licenses in `licenses/opus/`.

```fern
<<*>>=
// SILK stereo prediction weights, interpolation, and channel reconstruction.
// Copyright 2026 The Pion community. Copyright 2006-2011 Skype Limited.
// SPDX-License-Identifier: MIT AND BSD-3-Clause

import "std/opus/range";
import "std/opus/silk/math";
import "std/opus/silk/tables";

pub struct State { mid0: i32, mid1: i32, side0: i32, side1: i32, prediction0: i32, prediction1: i32 }

const WEIGHTS: i32[] = [-13732, -10050, -8266, -7526, -6500, -5000, -2950, -820, 820, 2950, 5000, 6500, 7526, 8266, 10050, 13732];

pub function state(): State {
  return State { mid0: 0, mid1: 0, side0: 0, side1: 0, prediction0: 0, prediction1: 0 };
}

pub function weights(own decoder: range.Decoder): (range.Decoder, i32, i32) {
  let (a, n) = decoder.icdf(tables.ICDF_STEREO_WEIGHTS_STAGE_ONE, 8);
  let (b, i0) = a.icdf(tables.ICDF_STEREO_WEIGHTS_STAGE_TWO, 8);
  let (c, i1) = b.icdf(tables.ICDF_STEREO_WEIGHTS_STAGE_THREE, 8);
  let (d, i2) = c.icdf(tables.ICDF_STEREO_WEIGHTS_STAGE_TWO, 8);
  let (e, i3) = d.icdf(tables.ICDF_STEREO_WEIGHTS_STAGE_THREE, 8);
  let at0 = i0 + 3 * (n / 5);
  let at1 = i2 + 3 * (n % 5);
  let p1 = WEIGHTS[at1] + ((WEIGHTS[at1 + 1] - WEIGHTS[at1]) * 6554 >> 16) * (2 * i3 + 1);
  let p0 = WEIGHTS[at0] + ((WEIGHTS[at0 + 1] - WEIGHTS[at0]) * 6554 >> 16) * (2 * i1 + 1) - p1;
  return (e, p0, p1);
}

pub function mono(previous: State, input: [i32], count: i32, channels: i32): (State, i32[]) {
  let output: i32[] = [];
  let sample = previous.mid1;
  for i in 0..count {
    for _ in 0..channels {
      output = output.append(sample);
    }
    sample = input[i];
  }
  return (State { ...previous, mid0: input[count - 2], mid1: input[count - 1] }, output);
}

pub function unmix(previous: State, mid: [i32], side: [i32], count: i32, rate: i32, prediction0: i32, prediction1: i32): (State, i32[]) {
  let length = rate * 8;
  let denominator = 65536 / length;
  let delta0 = math.rshift_round32(math.smulbb(prediction0 - previous.prediction0, denominator), 16);
  let delta1 = math.rshift_round32(math.smulbb(prediction1 - previous.prediction1, denominator), 16);
  let p0 = previous.prediction0;
  let p1 = previous.prediction1;
  let output: i32[] = [];
  for i in 0..count {
    if (i < length) {
      p0 += delta0;
      p1 += delta1;
    } else {
      p0 = prediction0;
      p1 = prediction1;
    }
    let before = if (i == 0) { previous.mid0 } else if (i == 1) { previous.mid1 } else { mid[i - 2] };
    let center = if (i == 0) { previous.mid1 } else { mid[i - 1] };
    let coded_side = if (i == 0) { previous.side1 } else { side[i - 1] };
    let sum = before + mid[i] + (center << 1) << 9;
    let value = math.smlawb(math.smlawb(coded_side << 8, sum, p0), center << 11, p1);
    let decoded_side = math.sat16(math.rshift_round32(value, 8));
    output = output.append(math.sat16(center + decoded_side)).append(math.sat16(center - decoded_side));
  }
  return (State { mid0: mid[count - 2], mid1: mid[count - 1], side0: side[count - 2], side1: side[count - 1], prediction0: prediction0, prediction1: prediction1 }, output);
}
```
