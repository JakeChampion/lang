/* Trace the unmodified MDCT and FFT in pinned libopus. */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "opus.h"
#include "celt/modes.h"
#include "celt/mdct.h"
#include "celt/kiss_fft.h"

static uint32_t seed;
static unsigned random_value(void) {
    seed = seed * UINT32_C(1664525) + UINT32_C(1013904223);
    return seed >> 8;
}
static void fill(float *values, int count, float low, float high) {
    for (int i = 0; i < count; i++) values[i] = low + (high-low) * (float)(random_value()%65536) / 65536.0f;
}
static void bits(const float *values, int count) {
    for (int i = 0; i < count; i++) {
        int32_t value;
        memcpy(&value, &values[i], sizeof(value));
        printf(" %d", value);
    }
    puts("");
}
int main(int argc, char **argv) {
    if (argc != 3) return 2;
    seed = (uint32_t)strtoul(argv[1], NULL, 10);
    int cases = atoi(argv[2]), error;
    const CELTMode *mode = opus_custom_mode_create(48000, 960, &error);
    if (error || !mode) return 3;
    for (int c = 0; c < cases; c++) {
        int shift = random_value()%4;
        int stride = 1 << (random_value()%4);
        int ia = random_value()%3;
        int oa = random_value()%stride;
        int sa = random_value()%3;
        int n = 960 >> shift;
        float input[1082], output[7680] = {0}, synthesis[1082];
        fill(input, ia+n+120, -1000.0f, 1000.0f);
        fill(synthesis, sa+n+120, -1000.0f, 1000.0f);
        clt_mdct_forward(&mode->mdct, input+ia, output+oa, mode->window, 120, shift, stride, 0);
        printf("f %d %d %d %d %d |", shift, stride, ia, oa, sa);
        bits(output, stride*n);
        clt_mdct_backward(&mode->mdct, output+oa, synthesis+sa, mode->window, 120, shift, stride, 0);
        printf("b |");
        bits(synthesis, sa+n+120);
        int size = random_value()%4;
        int length = 480 >> size;
        kiss_fft_cpx fft_input[480], fft_output[480];
        fill((float *)fft_input, 2*length, -1.0f, 1.0f);
        opus_fft(mode->mdct.kfft[size], fft_input, fft_output, 0);
        printf("t %d |", size);
        bits((const float *)fft_output, 2*length);
    }
    return 0;
}
