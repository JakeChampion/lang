/* Independent Vorbis IMDCT equations and whole-timeline window accumulation. */
#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

static void word(uint32_t value) {
    for (int i = 0; i < 4; ++i) putchar((value >> (8*i)) & 255);
}
static void floats(const float *values, int count) {
    for (int i = 0; i < count; ++i) { uint32_t raw; memcpy(&raw, values+i, 4); word(raw); }
}
static float coefficient(int i, int frame, int impulse) {
    if (impulse) return i == 0 ? 1.0f : 0.0f;
    return (float)((i*17 + frame*13) % 31 - 15) * .03125f;
}
static void inverse(const float *input, int count, float *output) {
    const double pi = acos(-1.0);
    for (int i = 0; i < 2*count; ++i) {
        double sum = 0;
        for (int j = 0; j < count; ++j)
            sum += input[j] * cos(pi * (2*i + 1 + count) * (2*j + 1) / (4.0 * count));
        output[i] = (float)sum;
    }
}
static float slope(int position, int length) {
    double phase = acos(-1.0) * .5 * (position + .5) / length;
    double s = sin(phase);
    return (float)sin(acos(-1.0) * .5 * s*s);
}
static float window(int i, int block, int previous, int next) {
    int ls = block > previous ? (block-previous)/4 : 0;
    int le = block > previous ? (block+previous)/4 : block/2;
    int rs = block > next ? 3*block/4-next/4 : block/2;
    int re = block > next ? 3*block/4+next/4 : block;
    if (i < ls || i >= re) return 0;
    if (i < le) return slope(i-ls, le-ls);
    if (i < rs) return 1;
    return slope(re-1-i, re-rs);
}
int main(void) {
    float input[4096], time[8192];
    word(16);
    for (int exponent = 6; exponent <= 13; ++exponent) {
        for (int mode = 0; mode < 2; ++mode) {
            int count = 1 << (exponent-1);
            for (int i = 0; i < count; ++i) input[i] = coefficient(i, 0, mode);
            inverse(input, count, time);
            word(exponent); word(mode); floats(input, count); floats(time, 2*count);
        }
    }
    word(2);
    for (int which = 0; which < 2; ++which) {
        int small = which ? 8 : 6, large = which ? 11 : 13;
        int exponents[7] = {small, small, large, large, small, large, small};
        int centers[7] = {0};
        float timeline[32768] = {0};
        for (int frame = 1; frame < 7; ++frame)
            centers[frame] = centers[frame-1] + ((1 << exponents[frame-1]) + (1 << exponents[frame]))/4;
        for (int frame = 0; frame < 7; ++frame) {
            int n = 1 << exponents[frame];
            int previous = 1 << exponents[frame ? frame-1 : frame];
            int next = 1 << exponents[frame < 6 ? frame+1 : frame];
            for (int i = 0; i < n/2; ++i) input[i] = coefficient(i, frame, 0);
            inverse(input, n/2, time);
            for (int i = 0; i < n; ++i) {
                int at = centers[frame] - n/2 + i;
                if (at >= 0) timeline[at] += time[i] * window(i, n, previous, next);
            }
        }
        word(small); word(large); word(7);
        for (int frame = 0; frame < 7; ++frame) {
            int n = 1 << exponents[frame];
            int start = centers[frame ? frame-1 : 0], count = centers[frame] - start;
            for (int i = 0; i < n/2; ++i) input[i] = coefficient(i, frame, 0);
            word(exponents[frame]); word(count); floats(input, n/2); floats(timeline+start, count);
        }
    }
    return 0;
}
