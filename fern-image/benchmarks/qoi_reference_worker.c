/* Offline QOI reference benchmark. No Fern runtime links to this file. */
#define QOI_IMPLEMENTATION
#define QOI_NO_STDIO
#include "qoi.h"
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <time.h>

static unsigned char *read_all(const char *path, size_t *length) {
    FILE *file = fopen(path, "rb");
    if (!file || fseek(file, 0, SEEK_END) || ftell(file) < 0) exit(2);
    long size = ftell(file);
    if (fseek(file, 0, SEEK_SET)) exit(2);
    unsigned char *data = malloc((size_t)size);
    if (!data || fread(data, 1, (size_t)size, file) != (size_t)size || fclose(file)) exit(2);
    *length = (size_t)size;
    return data;
}

static void write_all(const char *prefix, const char *suffix, const void *data, size_t length) {
    char path[4096];
    if (snprintf(path, sizeof(path), "%s%s", prefix, suffix) >= (int)sizeof(path)) exit(2);
    FILE *file = fopen(path, "wb");
    if (!file || fwrite(data, 1, length, file) != length || fclose(file)) exit(2);
}

static uint64_t nanos(void) {
    uint64_t ticks, frequency;
    /* Match the Fern arm64-darwin counter and integer conversion. */
    __asm__ volatile("mrs %0, cntvct_el0" : "=r"(ticks));
    __asm__ volatile("mrs %0, cntfrq_el0" : "=r"(frequency));
    return ticks / frequency * UINT64_C(1000000000)
         + ticks % frequency * UINT64_C(1000000000) / frequency;
}

static uint64_t repeat(int encode, const unsigned char *input, size_t input_size,
                       const void *pixels, const qoi_desc *description, int count, int *bytes) {
    uint64_t checksum = 0;
    for (int i = 0; i < count; ++i) {
        qoi_desc decoded = {0};
        void *output;
        if (encode) {
            output = qoi_encode(pixels, description, bytes);
        } else {
            output = qoi_decode(input, (int)input_size, &decoded, 0);
            *bytes = (int)(decoded.width * decoded.height * decoded.channels);
        }
        if (!output || *bytes <= 0) exit(6);
        const unsigned char *data = output;
        checksum += (uint64_t)*bytes + data[0] + data[*bytes - 1];
        free(output);
    }
    return checksum;
}

int main(int argc, char **argv) {
    if (argc != 6 || (strcmp(argv[1], "encode") && strcmp(argv[1], "decode"))) return 1;
    char *end;
    long count = strtol(argv[5], &end, 10);
    if (*end || count <= 0 || count > 1000000) return 1;
    size_t input_size, raw_size;
    unsigned char *input = read_all(argv[2], &input_size);
    unsigned char *raw = read_all(argv[3], &raw_size);
    qoi_desc description;
    unsigned char *pixels = qoi_decode(input, (int)input_size, &description, 0);
    if (!pixels || raw_size != description.width * description.height * description.channels || memcmp(raw, pixels, raw_size)) return 4;
    int encoded_size;
    void *encoded = qoi_encode(pixels, &description, &encoded_size);
    if (!encoded) return 5;
    write_all(argv[4], ".qoi", encoded, (size_t)encoded_size);
    write_all(argv[4], ".raw", pixels, raw_size);
    free(encoded);
    int bytes;
    int encode = !strcmp(argv[1], "encode");
    uint64_t warmup = repeat(encode, input, input_size, pixels, &description, 2, &bytes);
    uint64_t start = nanos();
    uint64_t checksum = repeat(encode, input, input_size, pixels, &description, (int)count, &bytes);
    uint64_t elapsed = nanos() - start;
    printf("%llu %llu %d %llu\n", (unsigned long long)elapsed, (unsigned long long)checksum, bytes, (unsigned long long)warmup);
    free(input); free(raw); free(pixels);
    return 0;
}
