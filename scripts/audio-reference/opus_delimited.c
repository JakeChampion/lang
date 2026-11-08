/* Independent multistream packet boundaries from unmodified libopus. */
#include <opus.h>
#include "src/opus_private.h"
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

static uint32_t random_state = 0x6b9d18a3U;
static uint32_t next(void) {
  random_state = random_state * 1664525U + 1013904223U;
  return random_state;
}

static void emit(const unsigned char *bytes, int length) {
  const unsigned char *frames[48], *padding;
  opus_int16 sizes[48];
  opus_int32 consumed = 0, pad = 0;
  unsigned char toc = 0;
  int offset = 0;
  int count = opus_packet_parse_impl(bytes, length, 1, &toc, frames, sizes,
   &offset, &consumed, &padding, &pad);
  printf("{\"bytes\":\"");
  for (int i = 0; i < length; ++i) printf("%02x", bytes[i]);
  printf("\",\"count\":%d", count);
  if (count >= 0) {
    uint32_t crc = 0xffffffffU;
    printf(",\"consumed\":%d,\"offset\":%d,\"padding\":%d,\"toc\":%u,\"sizes\":[",
     consumed, offset, pad, toc);
    for (int i = 0; i < count; ++i) {
      printf("%s%d", i ? "," : "", sizes[i]);
      for (int j = 0; j < sizes[i]; ++j) {
        crc ^= frames[i][j];
        for (int b = 0; b < 8; ++b)
          crc = (crc >> 1) ^ (0xedb88320U & (0U - (crc & 1U)));
      }
    }
    printf("],\"crc\":%u", crc ^ 0xffffffffU);
  }
  puts("}");
}

int main(void) {
  static const int edges[] = {0, 1, 2, 251, 252, 253, 1274, 1275};
  static const int counts[] = {1, 2, 3, 6, 12, 24, 48};
  static const int pads[] = {0, 1, 2, 254, 255, 256, 510};
  unsigned char output[66000], frame[48][1276];
  OpusRepacketizer *rp = opus_repacketizer_create();
  if (!rp) return 1;
  for (int test = 0; test < 224; ++test) {
    int count = counts[test % 7];
    int mixed = test / 7 & 1;
    opus_repacketizer_init(rp);
    for (int i = 0; i < count; ++i) {
      int size = edges[(test / 14 + (mixed ? i : 0)) % 8];
      frame[i][0] = (unsigned char)(128 | ((test & 1) << 2));
      for (int j = 0; j < size; ++j)
        frame[i][j + 1] = (unsigned char)(j * 13 + i * 7 + test * 19);
      if (opus_repacketizer_cat(rp, frame[i], size + 1) < 0) return 2;
    }
    int size = opus_repacketizer_out_range_impl(rp, 0, count, output, 65000, 1, 0, NULL, 0);
    if (size < 0) return 3;
    int pad = pads[test / 32];
    if (pad) {
      size = opus_repacketizer_out_range_impl(rp, 0, count, output, size + pad, 1, 1, NULL, 0);
      if (size < 0) return 4;
    }
    emit(output, size);
    int tail = test % 128;
    for (int j = 0; j < tail; ++j) output[size + j] = (unsigned char)next();
    emit(output, size + tail);
    emit(output, (int)(next() % (unsigned)(size + 1)));
    output[test % size] ^= (unsigned char)(1U << (test % 8));
    emit(output, size + tail);
  }
  for (int test = 0; test < 1000; ++test) {
    int length = (int)(next() % 129U);
    for (int i = 0; i < length; ++i) output[i] = (unsigned char)(next() >> 24);
    emit(output, length);
  }
  opus_repacketizer_destroy(rp);
  return 0;
}
