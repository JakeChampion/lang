#define QOA_IMPLEMENTATION
#include "qoa.h"
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <time.h>

static uint64_t now_ns(void) {
    struct timespec t;
    clock_gettime(CLOCK_MONOTONIC, &t);
    return (uint64_t)t.tv_sec * 1000000000u + (uint64_t)t.tv_nsec;
}

/* Linux keeps ru_maxrss across execve, so read the high-water mark of this image. */
static long peak_rss_kb(void) {
    FILE *status = fopen("/proc/self/status", "r");
    char line[256];
    long value = 0;
    while (status && fgets(line, sizeof(line), status)) {
        if (sscanf(line, "VmHWM: %ld", &value) == 1) break;
    }
    if (status) fclose(status);
    return value;
}

/* Decodes every frame of the file into one reusable buffer and returns the decoded sample frames. */
static long long pass(const unsigned char *input, unsigned int size, unsigned int first, qoa_desc desc, short *output, uint32_t *checksum, int *observed) {
    unsigned int at = first;
    long long frames = 0;
    while (at < size) {
        unsigned int length = 0;
        unsigned int used = qoa_decode_frame(input + at, size - at, &desc, output, &length);
        if (!used) return -1;
        *observed ^= output[0];
        if (checksum) {
            for (unsigned int i = 0; i < length * desc.channels; i++) *checksum = (*checksum ^ (uint32_t)(int32_t)output[i]) * 16777619u;
        }
        frames += length;
        at += used;
    }
    return frames;
}

/* Decodes every frame of the file in each round and records the time of each frame decode. */
static int time_frames(const unsigned char *input, unsigned int size, unsigned int first, qoa_desc desc, short *output, int *observed, int rounds, uint64_t *times, long count) {
    long index = 0;
    for (int round = 0; round < rounds; round++) {
        unsigned int at = first;
        while (at < size) {
            unsigned int length = 0;
            uint64_t started = now_ns();
            unsigned int used = qoa_decode_frame(input + at, size - at, &desc, output, &length);
            uint64_t elapsed = now_ns() - started;
            if (!used) return -1;
            if (index < count) times[index] = elapsed;
            index++;
            *observed ^= output[0];
            at += used;
        }
    }
    return index == count ? 0 : -1;
}

static int compare(const void *a, const void *b) {
    uint64_t x = *(const uint64_t *)a, y = *(const uint64_t *)b;
    return (x > y) - (x < y);
}

/* Returns the nearest-rank value at per_mille thousandths of the sorted times. */
static uint64_t rank(const uint64_t *sorted, long count, long per_mille) {
    long at = (count * per_mille + 999) / 1000 - 1;
    return sorted[at < 0 ? 0 : at];
}

int main(int argc, char **argv) {
    if (argc != 3) return 1;
    int rounds = atoi(argv[2]);
    if (rounds < 1 || rounds > 100000) return 1;
    FILE *file = fopen(argv[1], "rb");
    if (!file || fseek(file, 0, SEEK_END)) return 2;
    long size = ftell(file);
    if (size < 16 || size > 268435456 || fseek(file, 0, SEEK_SET)) return 2;
    unsigned char *input = malloc(size);
    if (!input || fread(input, 1, size, file) != (size_t)size) return 2;
    fclose(file);
    qoa_desc desc = {0};
    unsigned int first = qoa_decode_header(input, size, &desc);
    if (!first) return 3;
    short *output = malloc(sizeof(short) * QOA_FRAME_LEN * desc.channels);
    if (!output) return 3;
    int observed = 0;
    for (int i = 0; i < 4; i++) {
        if (pass(input, size, first, desc, output, NULL, &observed) != desc.samples) return 4;
    }
    long long frames = 0;
    uint64_t started = now_ns();
    for (int i = 0; i < rounds; i++) {
        long long decoded = pass(input, size, first, desc, output, NULL, &observed);
        if (decoded < 0) return 4;
        frames += decoded;
    }
    uint64_t elapsed = now_ns() - started;
    if (frames != (long long)desc.samples * rounds) return 5;
    long count = (long)((desc.samples + QOA_FRAME_LEN - 1) / QOA_FRAME_LEN) * rounds;
    uint64_t *times = malloc(sizeof(uint64_t) * count);
    if (!times || time_frames(input, size, first, desc, output, &observed, rounds, times, count)) return 4;
    qsort(times, count, sizeof(uint64_t), compare);
    uint32_t checksum = 2166136261u;
    if (pass(input, size, first, desc, output, &checksum, &observed) != desc.samples) return 4;
    printf("samples=%lld\n", frames * desc.channels);
    printf("checksum=%u\n", checksum);
    printf("wall_ns=%llu\n", (unsigned long long)elapsed);
    printf("frame_p50_ns=%llu\n", (unsigned long long)rank(times, count, 500));
    printf("frame_p90_ns=%llu\n", (unsigned long long)rank(times, count, 900));
    printf("frame_p99_ns=%llu\n", (unsigned long long)rank(times, count, 990));
    printf("frame_max_ns=%llu\n", (unsigned long long)rank(times, count, 1000));
    printf("peak_rss_kb=%ld\n", peak_rss_kb());
    printf("observed=%d\n", observed);
    free(times);
    free(output);
    free(input);
    return 0;
}
