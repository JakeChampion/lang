/* The libopus side of scripts/bench-opus, with the same arguments as
 * bench/audio/opus_codec.fern. The prototypes are the public libopus 1.x API,
 * declared here so the system library is enough without its headers. */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

typedef struct OpusEncoder OpusEncoder;
typedef struct OpusDecoder OpusDecoder;
OpusEncoder *opus_encoder_create(int32_t fs, int channels, int application, int *error);
int opus_encoder_ctl(OpusEncoder *st, int request, ...);
int32_t opus_encode_float(OpusEncoder *st, const float *pcm, int frame_size, unsigned char *data, int32_t max_data_bytes);
void opus_encoder_destroy(OpusEncoder *st);
OpusDecoder *opus_decoder_create(int32_t fs, int channels, int *error);
int opus_decoder_ctl(OpusDecoder *st, int request, ...);
int opus_decode_float(OpusDecoder *st, const unsigned char *data, int32_t len, float *pcm, int frame_size, int decode_fec);
void opus_decoder_destroy(OpusDecoder *st);

enum { RESTRICTED_LOWDELAY = 2051, SET_BITRATE = 4002, SET_VBR = 4006, SET_COMPLEXITY = 4010,
       SET_VBR_CONSTRAINT = 4020, GET_FINAL_RANGE = 4031, FRAME = 960, MAX_BYTES = 1276 };

static unsigned char *slurp(const char *path, long *size) {
  FILE *f = fopen(path, "rb");
  if (!f) exit(3);
  fseek(f, 0, SEEK_END);
  *size = ftell(f);
  fseek(f, 0, SEEK_SET);
  unsigned char *data = malloc(*size ? *size : 1);
  if (fread(data, 1, *size, f) != (size_t)*size) exit(3);
  fclose(f);
  return data;
}

static void hex(const unsigned char *data, long n) {
  for (long i = 0; i < n; i++) printf("%02x", data[i]);
  putchar('\n');
}

static int64_t encode_all(const float *pcm, long samples, int bitrate, int dump) {
  int error = 0;
  OpusEncoder *enc = opus_encoder_create(48000, 1, RESTRICTED_LOWDELAY, &error);
  if (!enc || error) return -1;
  opus_encoder_ctl(enc, SET_BITRATE, (int32_t)bitrate);
  opus_encoder_ctl(enc, SET_VBR, 1);
  opus_encoder_ctl(enc, SET_VBR_CONSTRAINT, 1);
  opus_encoder_ctl(enc, SET_COMPLEXITY, 5);
  unsigned char packet[MAX_BYTES];
  int64_t checksum = 0;
  for (long at = 0; at + FRAME <= samples; at += FRAME) {
    int32_t n = opus_encode_float(enc, pcm + at, FRAME, packet, MAX_BYTES);
    if (n < 0) return -1;
    checksum += n;
    if (dump) hex(packet, n);
  }
  opus_encoder_destroy(enc);
  return checksum;
}

static int64_t decode_all(const unsigned char *data, long size, int dump) {
  int error = 0;
  OpusDecoder *dec = opus_decoder_create(48000, 1, &error);
  if (!dec || error) return -1;
  float pcm[5760];
  int64_t checksum = 0;
  for (long i = 0; i + 2 <= size;) {
    int n = data[i] | data[i + 1] << 8;
    int count = opus_decode_float(dec, data + i + 2, n, pcm, 5760, 0);
    if (count < 0) return -1;
    uint32_t range = 0;
    opus_decoder_ctl(dec, GET_FINAL_RANGE, &range);
    checksum += range;
    if (dump) hex((const unsigned char *)pcm, (long)count * 4);
    i += 2 + n;
  }
  opus_decoder_destroy(dec);
  return checksum;
}

int main(int argc, char **argv) {
  if (argc != 6) return 2;
  long size = 0;
  unsigned char *input = slurp(argv[2], &size);
  int iterations = atoi(argv[3]), bitrate = atoi(argv[4]), dump = strcmp(argv[5], "dump") == 0;
  int encoding = strcmp(argv[1], "encode") == 0;
  int64_t checksum = 0;
  struct timespec start, end;
  clock_gettime(CLOCK_MONOTONIC, &start);
  for (int i = 0; i < iterations; i++) {
    int64_t sum = encoding ? encode_all((const float *)input, size / 4, bitrate, dump) : decode_all(input, size, dump);
    if (sum < 0) return 4;
    checksum += sum;
  }
  clock_gettime(CLOCK_MONOTONIC, &end);
  if (!dump) printf("%lld %lld\n", (long long)((end.tv_sec - start.tv_sec) * 1000000000LL + end.tv_nsec - start.tv_nsec), (long long)checksum);
  return 0;
}
