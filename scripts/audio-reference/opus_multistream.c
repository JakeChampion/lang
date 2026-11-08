/* Multistream packets, PCM, and entropy ranges from unmodified libopus. */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "opus.h"
#include "opus_multistream.h"
#include "src/opus_private.h"

static void check(int value) {
  if (value < 0) { fprintf(stderr, "libopus error %d\n", value); exit(2); }
}

static void emit(OpusMSDecoder *decoder, int id, int rate, int channels,
 int streams, int coupled, const unsigned char *mapping, int action,
 const unsigned char *packet, int length, int frames) {
  float pcm[46080];
  if (action == 3) check(opus_multistream_decoder_ctl(decoder, OPUS_RESET_STATE));
  int count = opus_multistream_decode_float(decoder, packet, length, pcm, frames, action == 2);
  check(count);
  if (count != frames) exit(3);
  opus_uint32 range = 0;
  check(opus_multistream_decoder_ctl(decoder, OPUS_GET_FINAL_RANGE(&range)));
  printf("{\"case\":%d,\"rate\":%d,\"channels\":%d,\"streams\":%d,\"coupled\":%d,\"mapping\":[",
   id, rate, channels, streams, coupled);
  for (int i = 0; i < channels; ++i) printf("%s%u", i ? "," : "", mapping[i]);
  printf("],\"action\":%d,\"frames\":%d,\"range\":%u,\"packet\":\"", action, frames, range);
  for (int i = 0; i < length; ++i) printf("%02x", packet[i]);
  printf("\",\"pcm\":[");
  for (int i = 0; i < count * channels; ++i) {
    uint32_t bits;
    memcpy(&bits, &pcm[i], sizeof(bits));
    printf("%s%u", i ? "," : "", bits);
  }
  puts("]}");
}

static int encode(OpusMSEncoder *encoder, int channels, int streams,
 int mode, int frames, unsigned char *packet, uint32_t *seed, int *time) {
  float pcm[23040];
  for (int s = 0; s < streams; ++s) {
    OpusEncoder *single = NULL;
    check(opus_multistream_encoder_ctl(encoder, OPUS_MULTISTREAM_GET_ENCODER_STATE(s, &single)));
    check(opus_encoder_ctl(single, OPUS_SET_FORCE_MODE(mode)));
    check(opus_encoder_ctl(single, OPUS_SET_BANDWIDTH(mode == MODE_SILK_ONLY ? OPUS_BANDWIDTH_WIDEBAND : OPUS_BANDWIDTH_FULLBAND)));
  }
  for (int i = 0; i < frames; ++i) {
    for (int c = 0; c < channels; ++c) {
      *seed = *seed * 1664525U + 1013904223U;
      int noise = (int)(*seed >> 19) - 4096;
      int tone = ((*time * (c + 1) + i * (c + 3)) % 257 - 128) * 48;
      pcm[i * channels + c] = (noise + tone) * (1.f / 32768.f);
    }
  }
  *time += frames;
  int length = opus_multistream_encode_float(encoder, pcm, frames, packet, 65536);
  check(length);
  return length;
}

int main(void) {
  const int rates[] = {8000, 12000, 16000, 24000, 48000};
  unsigned char packet[65536];
  int id = 0;
  for (int target = 0; target < 5; ++target) {
    for (int layout = 0; layout < 11; ++layout, ++id) {
      int channels = layout + 1, input_channels, streams, coupled, error;
      unsigned char mapping[8], encoder_mapping[8];
      OpusMSEncoder *encoder;
      if (layout < 8) {
        input_channels = channels;
        encoder = opus_multistream_surround_encoder_create(48000, channels, 1,
         &streams, &coupled, mapping, OPUS_APPLICATION_VOIP, &error);
      } else {
        streams = layout == 8 ? 1 : 2;
        coupled = layout == 9 ? 0 : 1;
        input_channels = streams + coupled;
        for (int i = 0; i < input_channels; ++i) encoder_mapping[i] = (unsigned char)i;
        channels = layout == 8 ? 4 : layout == 9 ? 3 : 2;
        mapping[0] = layout == 8 ? 0 : layout == 9 ? 1 : 2;
        mapping[1] = layout == 8 ? 0 : layout == 9 ? 0 : 255;
        mapping[2] = layout == 8 ? 255 : 1;
        mapping[3] = 1;
        encoder = opus_multistream_encoder_create(48000, input_channels, streams,
         coupled, encoder_mapping, OPUS_APPLICATION_VOIP, &error);
      }
      check(error);
      OpusMSDecoder *decoder = opus_multistream_decoder_create(rates[target], channels,
       streams, coupled, mapping, &error);
      check(error);
      check(opus_multistream_encoder_ctl(encoder, OPUS_SET_BITRATE(24000 * input_channels)));
      check(opus_multistream_encoder_ctl(encoder, OPUS_SET_INBAND_FEC(1)));
      check(opus_multistream_encoder_ctl(encoder, OPUS_SET_PACKET_LOSS_PERC(30)));
      uint32_t seed = 19937;
      int time = 0;
#define EMIT(action, data, length, duration) emit(decoder, id, rates[target], channels, streams, coupled, mapping, action, data, length, duration)
      EMIT(1, NULL, 0, rates[target] / 400);
      int length = encode(encoder, input_channels, streams, MODE_SILK_ONLY, 960, packet, &seed, &time);
      EMIT(0, packet, length, rates[target] / 50);
      length = encode(encoder, input_channels, streams, MODE_HYBRID, 480, packet, &seed, &time);
      EMIT(0, packet, length, rates[target] / 100);
      length = encode(encoder, input_channels, streams, MODE_SILK_ONLY, 960, packet, &seed, &time);
      EMIT(2, packet, length, rates[target] / 50);
      EMIT(0, packet, length, rates[target] / 50);
      EMIT(1, NULL, 0, rates[target] / 25);
      length = encode(encoder, input_channels, streams, MODE_CELT_ONLY, 960, packet, &seed, &time);
      EMIT(0, packet, length, rates[target] / 50);
      length = encode(encoder, input_channels, streams, MODE_CELT_ONLY, 120, packet, &seed, &time);
      EMIT(0, packet, length, rates[target] / 400);
      EMIT(1, NULL, 0, rates[target] / 400);
      length = encode(encoder, input_channels, streams, MODE_SILK_ONLY, 2880, packet, &seed, &time);
      EMIT(0, packet, length, rates[target] * 60 / 1000);
      EMIT(3, NULL, 0, rates[target] / 400);
#undef EMIT
      opus_multistream_encoder_destroy(encoder);
      opus_multistream_decoder_destroy(decoder);
    }
  }
  return 0;
}
