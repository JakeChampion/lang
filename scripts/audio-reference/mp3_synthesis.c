#define MINIMP3_IMPLEMENTATION
#define MINIMP3_FLOAT_OUTPUT
#define MINIMP3_ONLY_MP3
#define MINIMP3_NO_SIMD
#include "minimp3.h"
#include <stdio.h>

int main(int argc, char **argv) {
    if (argc != 2) return 1;
    FILE *file = fopen(argv[1], "wb");
    if (!file) return 1;
    float history[960], input[1152], output[1152], lines[2112];
    int channels[] = {1, 2, 1, 1, 2, 1, 2, 2, 1};
    for (int i = 0; i < 960; i++) history[i] = (float)((i * 73) % 257 - 128) * 0.125f;
    for (int round = 0; round < 9; round++) {
        for (int i = 0; i < 1152; i++) input[i] = (float)((round * 53 + i * 29) % 1021 - 510) * 0.25f;
        for (int i = 0; i < 2112; i++) lines[i] = 0.0f;
        mp3d_synth_granule(history, input, 18, channels[round], output, lines);
        size_t count = 576 * channels[round];
        if (fwrite(output, sizeof(float), count, file) != count) return 2;
        if (fwrite(history, sizeof(float), 960, file) != 960) return 2;
    }
    return fclose(file) ? 2 : 0;
}
