/* Complete Ogg Opus files from libogg, decoded by unmodified libopusfile. */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "opus.h"
#include "opus_multistream.h"
#include "internal.h"

typedef struct { unsigned char *data; size_t length, capacity; } Bytes;

static void check(int code) {
  if (code < 0) { fprintf(stderr, "reference error %d\n", code); exit(2); }
}

static void append(Bytes *out, const void *data, size_t length) {
  if (!length) return;
  size_t end = out->length + length;
  if (end > out->capacity) {
    size_t capacity = out->capacity ? out->capacity : 256;
    while (capacity < end) capacity *= 2;
    out->data = realloc(out->data, capacity);
    if (!out->data) exit(3);
    out->capacity = capacity;
  }
  memcpy(out->data + out->length, data, length);
  out->length = end;
}

static void number(Bytes *out, uint64_t value, int count) {
  unsigned char bytes[8];
  for (int i = 0; i < count; ++i) bytes[i] = (unsigned char)(value >> (8 * i));
  append(out, bytes, (size_t)count);
}

static void flush(ogg_stream_state *stream, Bytes *out) {
  ogg_page page;
  while (ogg_stream_flush(stream, &page)) {
    append(out, page.header, (size_t)page.header_len);
    append(out, page.body, (size_t)page.body_len);
  }
}

static void packet_in(ogg_stream_state *stream, const unsigned char *data,
 int length, int64_t granule, int bos, int eos, int index) {
  ogg_packet packet;
  memset(&packet, 0, sizeof(packet));
  packet.packet = (unsigned char *)data;
  packet.bytes = length;
  packet.granulepos = granule;
  packet.b_o_s = bos;
  packet.e_o_s = eos;
  packet.packetno = index;
  check(ogg_stream_packetin(stream, &packet));
}

static Bytes make_file(int id) {
  const int channels[] = {1, 2, 3, 6, 8, 2, 1, 2};
  const int skips[] = {312, 0, 960, 3840, 1, 312, 312, 0};
  const int gains[] = {0, 768, -1536, 0, 256, -1280, 0, 0};
  const int trims[] = {128, 0, 315, 960, 11, 64, 128, 128};
  const int groups[] = {1, 3, 2, 3, 1, 3, 6, 6};
  const int64_t biases[] = {0, 0, 123456, 17, 90000, 0, 1000000, 0};
  int preset = id >= 9 ? 0 : id;
  int packets = id >= 9 ? 1 : 6;
  int streams, coupled, error;
  unsigned char mapping[8], packet[65536];
  int family = channels[preset] <= 2 ? 0 : 1;
  OpusMSEncoder *encoder = opus_multistream_surround_encoder_create(48000,
   channels[preset], family, &streams, &coupled, mapping, OPUS_APPLICATION_AUDIO, &error);
  check(error);
  check(opus_multistream_encoder_ctl(encoder, OPUS_SET_BITRATE(24000 * channels[preset])));
  Bytes header = {0}, tags = {0}, out = {0};
  append(&header, "OpusHead", 8);
  number(&header, 1, 1);
  number(&header, (unsigned)channels[preset], 1);
  number(&header, (unsigned)skips[preset], 2);
  number(&header, id == 1 ? 44100 : 48000, 4);
  number(&header, (uint16_t)(id == 9 ? 768 : gains[preset]), 2);
  number(&header, (unsigned)family, 1);
  if (family) {
    number(&header, (unsigned)streams, 1);
    number(&header, (unsigned)coupled, 1);
    append(&header, mapping, (size_t)channels[preset]);
  }
  append(&tags, "OpusTags", 8);
  int vendor_length = id == 4 ? 70000 : 14;
  number(&tags, (unsigned)vendor_length, 4);
  for (int i = 0; i < vendor_length; ++i) {
    unsigned char byte = (unsigned char)('a' + i % 26);
    append(&tags, &byte, 1);
  }
  const char *comments[] = {"TITLE=reference", "ARTIST=fern", "title=second"};
  number(&tags, 3, 4);
  for (int i = 0; i < 3; ++i) {
    number(&tags, strlen(comments[i]), 4);
    append(&tags, comments[i], strlen(comments[i]));
  }
  const unsigned char suffix[] = {1, 0, 255, 42};
  append(&tags, suffix, sizeof(suffix));
  ogg_stream_state stream;
  check(ogg_stream_init(&stream, 300 + id));
  packet_in(&stream, header.data, (int)header.length, 0, 1, 0, 0);
  flush(&stream, &out);
  packet_in(&stream, tags.data, (int)tags.length, 0, 0, 0, 1);
  flush(&stream, &out);
  uint32_t seed = 14861;
  for (int p = 0; p < packets; ++p) {
    float pcm[960 * 8];
    for (int i = 0; i < 960 * channels[preset]; ++i) {
      seed = seed * 1664525U + 1013904223U;
      pcm[i] = ((int)(seed >> 18) - 8192) * (1.f / 32768.f);
    }
    int length = opus_multistream_encode_float(encoder, pcm, 960, packet, sizeof(packet));
    check(length);
    if (id == 5 && p == 0) {
      check(opus_multistream_packet_pad(packet, length, 65026, streams));
      length = 65026;
    }
    int64_t gp = biases[preset] + (p + 1) * 960;
    if (p + 1 == packets) gp -= id == 10 ? 648 : trims[preset];
    packet_in(&stream, packet, length, gp, 0, p + 1 == packets, p + 2);
    if ((p + 1) % groups[preset] == 0) flush(&stream, &out);
  }
  flush(&stream, &out);
  check(ogg_stream_clear(&stream));
  opus_multistream_encoder_destroy(encoder);
  free(header.data);
  free(tags.data);
  return out;
}

static void hex(const unsigned char *bytes, size_t length) {
  putchar('"');
  for (size_t i = 0; i < length; ++i) printf("%02x", bytes[i]);
  putchar('"');
}

static void emit(int id, const Bytes *file) {
  int error;
  OggOpusFile *decoder = op_open_memory(file->data, file->length, &error);
  check(error);
  if (!decoder) exit(4);
  int count = op_link_count(decoder);
  if (count < 1 || count > 8) exit(5);
  Bytes pcm[8] = {{0}};
  for (;;) {
    float samples[46080];
    int link = -1;
    int frames = op_read_float(decoder, samples, 46080, &link);
    check(frames);
    if (!frames) break;
    int channels = op_head(decoder, link)->channel_count;
    append(&pcm[link], samples, (size_t)frames * (unsigned)channels * sizeof(float));
  }
  printf("{\"case\":%d,\"bytes\":", id);
  hex(file->data, file->length);
  printf(",\"links\":[");
  for (int i = 0; i < count; ++i) {
    const OpusHead *head = op_head(decoder, i);
    const OpusTags *tags = op_tags(decoder, i);
    int64_t frames = op_pcm_total(decoder, i);
    if (pcm[i].length != (size_t)frames * (unsigned)head->channel_count * sizeof(float)) exit(6);
    if (i) putchar(',');
    printf("{\"serial\":%u,\"initial\":%lld,\"frames\":%lld,\"fields\":[%d,%d,%u,%u,%d,%d,%d,%d],\"mapping\":",
     (unsigned)op_serialno(decoder, i), (long long)decoder->links[i].pcm_start, (long long)frames,
     head->version, head->channel_count, head->pre_skip, head->input_sample_rate,
     head->output_gain, head->mapping_family, head->stream_count, head->coupled_count);
    hex(head->mapping, (size_t)head->channel_count);
    printf(",\"vendor\":");
    hex((const unsigned char *)tags->vendor, strlen(tags->vendor));
    printf(",\"comments\":[");
    for (int c = 0; c < tags->comments; ++c) {
      if (c) putchar(',');
      hex((const unsigned char *)tags->user_comments[c], (size_t)tags->comment_lengths[c]);
    }
    int suffix_length;
    const unsigned char *suffix = opus_tags_get_binary_suffix(tags, &suffix_length);
    printf("],\"suffix\":");
    hex(suffix, (size_t)suffix_length);
    printf(",\"pcm\":[");
    for (size_t s = 0; s < pcm[i].length / sizeof(float); ++s) {
      uint32_t bits;
      memcpy(&bits, pcm[i].data + s * sizeof(float), sizeof(bits));
      printf("%s%u", s ? "," : "", bits);
    }
    printf("]}");
    free(pcm[i].data);
  }
  puts("]}");
  op_free(decoder);
}

int main(void) {
  Bytes files[8];
  for (int i = 0; i < 8; ++i) { files[i] = make_file(i); emit(i, &files[i]); }
  Bytes chain = {0};
  for (int i = 0; i < 3; ++i) append(&chain, files[i].data, files[i].length);
  emit(8, &chain);
  Bytes smoke = make_file(9);
  emit(9, &smoke);
  free(smoke.data);
  Bytes empty = make_file(10);
  emit(10, &empty);
  free(empty.data);
  free(chain.data);
  for (int i = 0; i < 8; ++i) free(files[i].data);
  return 0;
}
