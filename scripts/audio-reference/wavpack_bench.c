// Measure complete lossless PCM with a borrowed input and reusable output.
#include "wavpack.h"
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

typedef struct { const unsigned char *data; int64_t bytes, at; } Input;
static int32_t read_bytes(void *opaque, void *out, int32_t count) {
    Input *in = opaque;
    if (count < 0) return 0;
    if (count > in->bytes - in->at) count = (int32_t)(in->bytes - in->at);
    memcpy(out, in->data + in->at, (size_t)count); in->at += count;
    return count;
}
static int64_t position(void *opaque) { return ((Input *)opaque)->at; }
static int seek_absolute(void *opaque, int64_t at) {
    Input *in = opaque;
    if (at < 0 || at > in->bytes) return -1;
    in->at = at; return 0;
}
static int seek_relative(void *opaque, int64_t delta, int mode) {
    Input *in = opaque;
    int64_t base = mode == SEEK_SET ? 0 : mode == SEEK_CUR ? in->at : in->bytes;
    if (delta < -base || delta > in->bytes - base) return -1;
    return seek_absolute(in, base + delta);
}
static int push_back(void *opaque, int byte) {
    Input *in = opaque;
    if (!in->at) return EOF;
    --in->at; return byte;
}
static int64_t length(void *opaque) { return ((Input *)opaque)->bytes; }
static int can_seek(void *opaque) { (void)opaque; return 1; }
static WavpackStreamReader64 reader = {
    .read_bytes = read_bytes, .get_pos = position, .set_pos_abs = seek_absolute,
    .set_pos_rel = seek_relative, .push_back_byte = push_back,
    .get_length = length, .can_seek = can_seek
};
static int64_t nanoseconds(void) {
    struct timespec value; clock_gettime(CLOCK_MONOTONIC, &value);
    return (int64_t)value.tv_sec * 1000000000LL + value.tv_nsec;
}
static long peak_rss(void) {
    FILE *file = fopen("/proc/self/status", "r");
    if (!file) return 0;
    char line[256]; long result = 0;
    while (fgets(line, sizeof(line), file)) if (sscanf(line, "VmHWM: %ld kB", &result) == 1) break;
    fclose(file); return result;
}
static int consume(WavpackContext *context, int32_t *output, int channels, int rounds, uint32_t *checksum, uint64_t *samples) {
    *checksum = 2166136261u; *samples = 0;
    for (int round = 0; round < rounds; ++round) {
        if (!WavpackSeekSample64(context, 0)) return 0;
        uint32_t frames;
        while ((frames = WavpackUnpackSamples(context, output, 4096)) != 0) {
            uint32_t count = frames * (uint32_t)channels;
            for (uint32_t i = 0; i < count; ++i) *checksum = (*checksum ^ (uint32_t)output[i]) * 16777619u;
            *samples += count;
        }
        if (WavpackGetNumErrors(context)) return 0;
    }
    return 1;
}
int main(int argc, char **argv) {
    if (argc != 3 || strcmp(WavpackGetLibraryVersionString(), "5.8.1")) return 1;
    int rounds = atoi(argv[2]);
    if (rounds < 1 || rounds > 100000) return 1;
    FILE *file = fopen(argv[1], "rb");
    if (!file || fseek(file, 0, SEEK_END)) return 2;
    long bytes = ftell(file);
    if (bytes < 32 || fseek(file, 0, SEEK_SET)) return 2;
    unsigned char *data = malloc((size_t)bytes);
    if (!data || fread(data, 1, (size_t)bytes, file) != (size_t)bytes || fclose(file)) return 2;
    Input input = { .data = data, .bytes = bytes, .at = 0 };
    char error[100];
    WavpackContext *context = WavpackOpenFileInputEx64(&reader, &input, NULL, error, 0, 0);
    if (!context) { fprintf(stderr, "%s\n", error); return 3; }
    int channels = WavpackGetNumChannels(context);
    if (channels < 1 || channels > 8 || WavpackGetMode(context) & MODE_HYBRID) return 3;
    int32_t *output = malloc((size_t)4096 * (size_t)channels * sizeof(*output));
    uint32_t checksum; uint64_t samples;
    if (!output || !consume(context, output, channels, 4, &checksum, &samples)) return 4;
    int64_t started = nanoseconds();
    if (!consume(context, output, channels, rounds, &checksum, &samples)) return 5;
    int64_t elapsed = nanoseconds() - started;
    uint64_t expected = (uint64_t)WavpackGetNumSamples64(context) * (uint64_t)channels * (uint64_t)rounds;
    if (samples != expected) return 6;
    printf("samples=%llu\nchecksum=%u\nwall_ns=%lld\nnative_peak_rss_kb=%ld\n",
        (unsigned long long)samples, checksum, (long long)elapsed, peak_rss());
    WavpackCloseFile(context); free(output); free(data); return 0;
}
