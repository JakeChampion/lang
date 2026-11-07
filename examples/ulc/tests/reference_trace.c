/* Test driver for the unmodified upstream encoder. */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include "ulcEncoder.h"
#include "ulcEncoder_Internals.h"

int main(int argc, char **argv) {
    if (argc != 3) return 1;
    FILE *in = fopen(argv[1], "rb"), *out = fopen(argv[2], "wb");
    uint32_t header[4];
    if (!in || !out || fread(header, sizeof(header), 1, in) != 1) return 2;
    struct ULC_EncoderState_t state = { .BlockSize = header[0], .nChan = header[1], .RateHz = header[2] };
    if (ULC_EncoderState_Init(&state) < 0) return 3;
    size_t count = (size_t)header[0] * header[1];
    float *samples = malloc(count * sizeof(float));
    if (!samples) return 4;
    for (uint32_t block = 0; block < header[3]; block++) {
        if (fread(samples, sizeof(float), count, in) != count) return 5;
        int nonzero = ULCi_TransformBlock(&state, samples);
        int fields[3] = {state.WindowCtrl, state.NextWindowCtrl, nonzero};
        fwrite(fields, sizeof(fields), 1, out);
        fwrite(&state.BlockComplexity, sizeof(float), 1, out);
        fwrite(state.TransformBuffer, sizeof(float), count, out);
        fwrite(state.TransformNoise, sizeof(float), count, out);
    }
    free(samples);
    ULC_EncoderState_Destroy(&state);
    fclose(in);
    return fclose(out) != 0;
}
