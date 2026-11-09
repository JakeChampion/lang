#define MINIMP3_IMPLEMENTATION
#define MINIMP3_FLOAT_OUTPUT
#ifndef FERN_MP2_SIMD
#define MINIMP3_NO_SIMD
#endif
#include "minimp3.h"
#include <stdio.h>

int main(int argc, char **argv) {
    if (argc != 3) return 1;
    FILE *in = fopen(argv[1], "rb");
    if (!in || fseek(in, 0, SEEK_END)) return 1;
    long size = ftell(in);
    if (size < 4 || size > 16777216 || fseek(in, 0, SEEK_SET)) return 1;
    unsigned char *bytes = malloc((size_t)size);
    if (!bytes || fread(bytes, 1, (size_t)size, in) != (size_t)size) return 1;
    fclose(in);
    FILE *out = fopen(argv[2], "wb");
    if (!out) return 1;
    mp3dec_t state;
    mp3dec_init(&state);
    int at = 0;
    while (at < size) {
        float pcm[2304];
        mp3dec_frame_info_t info = {0};
        int frames = mp3dec_decode_frame(&state, bytes + at, (int)size - at, pcm, &info);
        if (info.layer != 2 || frames != 1152 || info.frame_bytes < 4 || info.frame_bytes > size - at) return 2;
        for (int i = 0; i < frames * info.channels; i++) {
            uint32_t bits;
            memcpy(&bits, pcm + i, sizeof(bits));
            unsigned char value[4] = {bits & 255, (bits >> 8) & 255, (bits >> 16) & 255, bits >> 24};
            if (fwrite(value, 1, 4, out) != 4) return 3;
        }
        at += info.frame_bytes;
    }
    free(bytes);
    return fclose(out) ? 1 : 0;
}
