#define MINIMP3_IMPLEMENTATION
#define MINIMP3_FLOAT_OUTPUT
#define MINIMP3_ONLY_MP3
#ifndef FERN_MP3_BENCH_SIMD
#define MINIMP3_NO_SIMD
#endif
#include "minimp3_ex.h"
#include <stdio.h>

int main(int argc, char **argv) {
    if (argc != 2) return 1;
    FILE *file = fopen(argv[1], "rb");
    if (!file || fseek(file, 0, SEEK_END)) return 1;
    long size = ftell(file);
    if (size < 4 || size > 16777216 || fseek(file, 0, SEEK_SET)) return 1;
    unsigned char *input = malloc(size);
    if (!input || fread(input, 1, size, file) != (size_t)size) return 1;
    fclose(file);
    uint32_t checksum = 0;
    for (int i = 0; i < 100; i++) {
        mp3dec_t state;
        mp3dec_init(&state);
        mp3dec_file_info_t info = {0};
        if (mp3dec_load_buf(&state, input, size, &info, NULL, NULL) || !info.samples) return 2;
        size_t positions[3] = {0, info.samples / 2, info.samples - 1};
        for (int j = 0; j < 3; j++) checksum += (uint32_t)(int32_t)(info.buffer[positions[j]] * 1024.0f);
        free(info.buffer);
    }
    printf("%u\n", checksum);
    free(input);
    return 0;
}
