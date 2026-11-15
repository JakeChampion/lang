// cbench MODE FILE ROUNDS: decode each record of FILE ROUNDS times.
// MODE zinit makes a zlib state for each stream, as git does. zreset keeps
// one state and resets it for each stream. ldeflate uses libdeflate, when
// the build defines WITH_LIBDEFLATE. Each stream gets a new output buffer,
// as std/deflate makes one. The output is the count of decoded bytes.
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <zlib.h>

#ifdef WITH_LIBDEFLATE
struct libdeflate_decompressor;
struct libdeflate_decompressor *libdeflate_alloc_decompressor(void);
int libdeflate_zlib_decompress(struct libdeflate_decompressor *, const void *, size_t, void *, size_t, size_t *);
#endif

static uint32_t le32(const unsigned char *p) { return p[0] | p[1] << 8 | p[2] << 16 | (uint32_t)p[3] << 24; }

int main(int argc, char **argv) {
  if (argc != 4) return 2;
  const char *mode = argv[1];
  FILE *f = fopen(argv[2], "rb");
  if (!f) return 2;
  fseek(f, 0, SEEK_END);
  long n = ftell(f);
  fseek(f, 0, SEEK_SET);
  unsigned char *data = malloc(n);
  if (fread(data, 1, n, f) != (size_t)n) return 2;
  int rounds = atoi(argv[3]);
  int lib = strcmp(mode, "ldeflate") == 0, reset = strcmp(mode, "zreset") == 0;
#ifdef WITH_LIBDEFLATE
  struct libdeflate_decompressor *ld = lib ? libdeflate_alloc_decompressor() : 0;
#else
  if (lib) return 2;
#endif
  z_stream zs;
  memset(&zs, 0, sizeof zs);
  if (reset) inflateInit(&zs);
  long total = 0;
  for (int r = 0; r < rounds; r++) {
    long at = 0;
    while (at + 8 <= n) {
      uint32_t zlen = le32(data + at), size = le32(data + at + 4);
      unsigned char *out = malloc(size ? size : 1);
      size_t got = 0;
      if (lib) {
#ifdef WITH_LIBDEFLATE
        if (libdeflate_zlib_decompress(ld, data + at + 8, zlen, out, size, &got) != 0) return 3;
#endif
      } else {
        if (!reset) {
          memset(&zs, 0, sizeof zs);
          inflateInit(&zs);
        } else {
          inflateReset(&zs);
        }
        zs.next_in = data + at + 8;
        zs.avail_in = zlen;
        zs.next_out = out;
        zs.avail_out = size;
        if (inflate(&zs, Z_FINISH) != Z_STREAM_END) return 3;
        got = zs.total_out;
        if (!reset) inflateEnd(&zs);
      }
      total += got;
      free(out);
      at += 8 + zlen;
    }
  }
  printf("%ld\n", total);
  return 0;
}
