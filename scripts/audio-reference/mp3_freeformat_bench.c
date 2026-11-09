#define MINIMP3_IMPLEMENTATION
#define MINIMP3_FLOAT_OUTPUT
#define MINIMP3_ONLY_MP3
#ifndef FERN_MP3_BENCH_SIMD
#define MINIMP3_NO_SIMD
#endif
#include "minimp3_ex.h"
#include <stdio.h>
#include <time.h>

static uint32_t checksum = 2166136261u;
static uint64_t samples;

static void hash(const float *pcm, size_t count) {
    for (size_t i = 0; i < count; i++) {
        uint32_t bits;
        memcpy(&bits, pcm + i, sizeof(bits));
        for (int shift = 0; shift < 32; shift += 8)
            checksum = (checksum ^ ((bits >> shift) & 255u)) * 16777619u;
    }
    samples += count;
}

static int decode(const unsigned char *input, int size, int frames) {
    mp3dec_t state;
    mp3dec_init(&state);
    if (!frames) {
        mp3dec_file_info_t info = {0};
        if (mp3dec_load_buf(&state, input, size, &info, NULL, NULL) || !info.samples) return 1;
        hash(info.buffer, info.samples);
        free(info.buffer);
        return 0;
    }
    float pcm[2304];
    int at = 0;
    while (at < size) {
        mp3dec_frame_info_t info = {0};
        int count = mp3dec_decode_frame(&state, input + at, size - at, pcm, &info);
        if (!count || info.frame_bytes < 1) return 1;
        hash(pcm, (size_t)count * info.channels);
        at += info.frame_bytes;
    }
    return 0;
}

static uint64_t now(void) {
    struct timespec value;
    if (clock_gettime(CLOCK_MONOTONIC, &value)) exit(8);
    return (uint64_t)value.tv_sec * 1000000000u + value.tv_nsec;
}

static long peak_rss(void) {
    FILE *file = fopen("/proc/self/status", "r");
    if (!file) return 0;
    char line[256];
    long value = 0;
    while (fgets(line, sizeof(line), file)) {
        if (sscanf(line, "VmHWM: %ld kB", &value) == 1) break;
    }
    fclose(file);
    return value;
}

int main(int argc, char **argv) {
    if (argc != 5 || (strcmp(argv[2], "whole") && strcmp(argv[2], "frames"))) return 1;
    FILE *file = fopen(argv[1], "rb");
    if (!file || fseek(file, 0, SEEK_END)) return 2;
    long size = ftell(file);
    if (size < 4 || size > 16777216 || fseek(file, 0, SEEK_SET)) return 2;
    unsigned char *input = malloc(size);
    if (!input || fread(input, 1, size, file) != (size_t)size) return 2;
    fclose(file);
    int rounds = atoi(argv[3]), frames = !strcmp(argv[2], "frames");
    if (rounds < 1) return 3;
    for (int i = 0; i < 2; i++) if (decode(input, size, frames)) return 4;
    checksum = 2166136261u;
    samples = 0;
    uint64_t start = now();
    for (int i = 0; i < rounds; i++) if (decode(input, size, frames)) return 5;
    uint64_t elapsed = now() - start;
    printf("wall_ns=%llu\nchecksum=%u\nsamples=%llu\nnative_peak_rss_kb=%ld\n",
           (unsigned long long)elapsed, checksum, (unsigned long long)samples, peak_rss());
    mp3dec_t state;
    mp3dec_init(&state);
    mp3dec_file_info_t info = {0};
    if (mp3dec_load_buf(&state, input, size, &info, NULL, NULL)) return 7;
    file = fopen(argv[4], "wb");
    if (!file || fwrite(info.buffer, sizeof(float), info.samples, file) != info.samples) return 7;
    if (fclose(file)) return 7;
    free(info.buffer);
    free(input);
    return 0;
}
