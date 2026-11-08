#define MINIMP3_IMPLEMENTATION
#define MINIMP3_FLOAT_OUTPUT
#define MINIMP3_ONLY_MP3
#define MINIMP3_NO_SIMD
#include "minimp3_ex.h"
#include <stdio.h>

static int write_samples(FILE *file, const float *samples, size_t count) {
    for (size_t i = 0; i < count; i++) {
        uint32_t bits;
        memcpy(&bits, samples + i, sizeof(bits));
        unsigned char bytes[4] = {bits & 255, (bits >> 8) & 255, (bits >> 16) & 255, (bits >> 24) & 255};
        if (fwrite(bytes, 1, 4, file) != 4) return 1;
    }
    return 0;
}

int main(int argc, char **argv) {
    if (argc < 3) return 1;
    FILE *file = fopen(argv[1], "rb");
    if (!file || fseek(file, 0, SEEK_END)) return 1;
    long size = ftell(file);
    if (size < 4 || size > 16777216 || fseek(file, 0, SEEK_SET)) return 1;
    unsigned char *input = malloc(size);
    if (!input || fread(input, 1, size, file) != (size_t)size) return 1;
    fclose(file);
    file = fopen(argv[2], "wb");
    if (!file) return 1;
    mp3dec_t state;
    mp3dec_init(&state);
    if (argc > 3 && !strcmp(argv[3], "gapless")) {
        mp3dec_file_info_t info = {0};
        if (mp3dec_load_buf(&state, input, size, &info, NULL, NULL)) return 2;
        if (write_samples(file, info.buffer, info.samples)) return 3;
        free(info.buffer);
    } else {
        float samples[2304];
        int at = 0;
        while (at < size) {
            mp3dec_frame_info_t info = {0};
            int count = mp3dec_decode_frame(&state, input + at, size - at, samples, &info);
            if (!info.frame_bytes) break;
            at += info.frame_bytes;
            if (write_samples(file, samples, count * info.channels)) return 3;
        }
    }
    fclose(file);
    free(input);
    return 0;
}
