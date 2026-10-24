// cbench — libdeflate on one file, the same two modes as fbench.
#include <libdeflate.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

static long long now_ns(void) {
  struct timespec ts;
  clock_gettime(CLOCK_MONOTONIC, &ts);
  return (long long)ts.tv_sec * 1000000000LL + ts.tv_nsec;
}
static int cmp(const void *a, const void *b) {
  long long x = *(const long long *)a, y = *(const long long *)b;
  return x < y ? -1 : x > y;
}
static long long median(long long *t, int n) { qsort(t, n, sizeof *t, cmp); return t[n / 2]; }
static unsigned char *load(const char *p, size_t *n) {
  FILE *f = fopen(p, "rb");
  if (!f) { perror(p); exit(2); }
  fseek(f, 0, SEEK_END); *n = ftell(f); fseek(f, 0, SEEK_SET);
  unsigned char *b = malloc(*n ? *n : 1);
  if (fread(b, 1, *n, f) != *n) { perror(p); exit(2); }
  fclose(f);
  return b;
}
static long long decode_runs(const unsigned char *s, size_t sn, const unsigned char *orig, size_t on, int runs) {
  struct libdeflate_decompressor *d = libdeflate_alloc_decompressor();
  unsigned char *out = malloc(on ? on : 1);
  long long *t = malloc(sizeof(long long) * runs);
  for (int r = 0; r < runs; r++) {
    size_t got = 0;
    long long t0 = now_ns();
    enum libdeflate_result res = libdeflate_deflate_decompress(d, s, sn, out, on, &got);
    t[r] = now_ns() - t0;
    if (res != LIBDEFLATE_SUCCESS || got != on || memcmp(out, orig, on) != 0) { fprintf(stderr, "cbench: round trip differs\n"); exit(4); }
  }
  long long m = median(t, runs);
  free(t); free(out); libdeflate_free_decompressor(d);
  return m;
}
int main(int argc, char **argv) {
  if (argc >= 6 && strcmp(argv[1], "c") == 0) {
    size_t n; unsigned char *in = load(argv[2], &n);
    int level = atoi(argv[3]), runs = atoi(argv[4]);
    struct libdeflate_compressor *c = libdeflate_alloc_compressor(level);
    size_t cap = libdeflate_deflate_compress_bound(c, n);
    unsigned char *out = malloc(cap);
    long long *t = malloc(sizeof(long long) * runs);
    size_t sz = 0;
    for (int r = 0; r < runs; r++) {
      long long t0 = now_ns();
      sz = libdeflate_deflate_compress(c, in, n, out, cap);
      t[r] = now_ns() - t0;
    }
    FILE *f = fopen(argv[5], "wb"); fwrite(out, 1, sz, f); fclose(f);
    long long d = decode_runs(out, sz, in, n, runs);
    printf("in=%zu out=%zu comp_ns=%lld decomp_ns=%lld\n", n, sz, median(t, runs), d);
    return 0;
  }
  if (argc >= 5 && strcmp(argv[1], "d") == 0) {
    size_t sn, on; unsigned char *s = load(argv[2], &sn), *o = load(argv[3], &on);
    printf("decomp_ns=%lld\n", decode_runs(s, sn, o, on, atoi(argv[4])));
    return 0;
  }
  fprintf(stderr, "usage: cbench c FILE LEVEL RUNS OUT | cbench d STREAM ORIG RUNS\n");
  return 2;
}
