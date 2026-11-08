/* Header and comment observations from unmodified libopusfile. */
#include <opusfile.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

static uint32_t le32(const unsigned char *p) {
  return p[0] | (uint32_t)p[1] << 8 | (uint32_t)p[2] << 16 | (uint32_t)p[3] << 24;
}

static void hex(const unsigned char *bytes, int count) {
  putchar('"');
  for (int i = 0; i < count; ++i) printf("%02x", bytes[i]);
  putchar('"');
}

int main(void) {
  int kind;
  while ((kind = getchar()) != EOF) {
    unsigned char size_bytes[4];
    if (fread(size_bytes, 1, 4, stdin) != 4) return 1;
    uint32_t length = le32(size_bytes);
    if (length > 4194304) return 2;
    unsigned char *data = malloc(length + 1);
    if (!data) return 3;
    if (fread(data, 1, length, stdin) != length) return 4;
    printf("{\"kind\":%d,\"bytes\":", kind);
    hex(data, (int)length);
    if (kind == 0) {
      OpusHead head;
      int code = opus_head_parse(&head, data, length);
      printf(",\"code\":%d", code);
      if (!code) {
        printf(",\"fields\":[%d,%d,%u,%u,%d,%d,%d,%d],\"mapping\":",
         head.version, head.channel_count, head.pre_skip, head.input_sample_rate,
         head.output_gain, head.mapping_family, head.stream_count, head.coupled_count);
        hex(head.mapping, head.channel_count);
      }
    } else {
      OpusTags tags;
      int code = opus_tags_parse(&tags, data, length);
      printf(",\"code\":%d", code);
      if (!code) {
        int suffix_length;
        const unsigned char *suffix = opus_tags_get_binary_suffix(&tags, &suffix_length);
        printf(",\"vendor\":");
        hex((const unsigned char *)tags.vendor, (int)le32(data + 8));
        printf(",\"suffix\":");
        hex(suffix, suffix_length);
        printf(",\"comments\":[");
        for (int i = 0; i < tags.comments; ++i) {
          if (i) putchar(',');
          hex((const unsigned char *)tags.user_comments[i], tags.comment_lengths[i]);
        }
        putchar(']');
        opus_tags_clear(&tags);
      }
    }
    puts("}");
    free(data);
  }
  return 0;
}
