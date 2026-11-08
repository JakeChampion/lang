/* Independent AAC inverse-transform/window oracle using the defining equations. */
#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

static void word(uint32_t x) {
    for (unsigned i = 0; i != 4; ++i) putchar((int)((x >> (8 * i)) & 255));
}
static void floats(const float *p, int count) {
    for (int i = 0; i != count; ++i) { uint32_t u; memcpy(&u, p + i, 4); word(u); }
}
static float coefficient(int i, int mode, int frame) {
    if (mode == 1) return i == 0 ? 1.0f : 0.0f;
    return (float)((i * 17 + frame * 13) % 31 - 15) * 0.03125f;
}
static void inverse(const float *spectrum, int n, float *output) {
    const double pi = acos(-1.0);
    for (int i = 0; i != 2 * n; ++i) {
        double total = 0;
        for (int j = 0; j != n; ++j)
            total += (double)spectrum[j] * cos(pi * ((2 * i + 1 + n) * (2 * j + 1)) / (4.0 * n));
        output[i] = (float)(total / (2.0 * n));
    }
}
static double bessel(double x) {
    double term = 1, sum = 1;
    for (int k = 1; k < 128; ++k) {
        term *= x / ((double)k * k);
        sum += term;
        if (term < sum * 1e-18) break;
    }
    return sum;
}
static void windows(int n, double alpha, float *sine, float *kbd) {
    double cumulative[1024], total = 0;
    const double pi = acos(-1.0);
    for (int i = 0; i != n; ++i) {
        sine[i] = (float)sin(pi * (i + .5) / (2.0 * n));
        total += bessel(((double)i * (n - i)) * (alpha * pi / n) * (alpha * pi / n));
        cumulative[i] = total;
    }
    for (int i = 0; i != n; ++i) kbd[i] = (float)sqrt(cumulative[i] / (total + 1.0));
}
int main(void) {
    float input[1024], time[2048];
    word(4);
    for (int n = 128; n <= 1024; n *= 8) {
        for (int mode = 1; mode <= 2; ++mode) {
            for (int i = 0; i != n; ++i) input[i] = coefficient(i, mode, 0);
            inverse(input, n, time);
            word((uint32_t)n); floats(input, n); floats(time, 2 * n);
        }
    }
    float long_sine[1024], long_kbd[1024], short_sine[128], short_kbd[128];
    windows(1024, 4.0, long_sine, long_kbd);
    windows(128, 6.0, short_sine, short_kbd);
    float delay[1024] = {0}, accumulation[2048], pcm[1024];
    const int sequence[] = {0, 1, 2, 2, 3, 0};
    const int shape[] = {0, 1, 0, 1, 0, 1};
    int previous = 0;
    word(6);
    for (int frame = 0; frame != 6; ++frame) {
        for (int i = 0; i != 1024; ++i) input[i] = coefficient(i, 2, frame + 1);
        memset(accumulation, 0, sizeof accumulation);
        const float *old_long = previous ? long_kbd : long_sine;
        const float *old_short = previous ? short_kbd : short_sine;
        const float *current_long = shape[frame] ? long_kbd : long_sine;
        const float *current_short = shape[frame] ? short_kbd : short_sine;
        if (sequence[frame] == 2) {
            for (int block = 0; block != 8; ++block) {
                inverse(input + 128 * block, 128, time);
                for (int i = 0; i != 128; ++i) {
                    const float *left = block == 0 ? old_short : current_short;
                    accumulation[448 + 128 * block + i] += time[i] * left[i];
                    accumulation[448 + 128 * block + 128 + i] += time[128 + i] * current_short[127 - i];
                }
            }
        } else {
            inverse(input, 1024, time);
            for (int i = 0; i != 1024; ++i) {
                float left = old_long[i], right = current_long[1023 - i];
                if (sequence[frame] == 3) left = i < 448 ? 0 : i < 576 ? old_short[i - 448] : 1;
                if (sequence[frame] == 1) right = i < 448 ? 1 : i < 576 ? current_short[575 - i] : 0;
                accumulation[i] = time[i] * left;
                accumulation[i + 1024] = time[i + 1024] * right;
            }
        }
        for (int i = 0; i != 1024; ++i) { pcm[i] = delay[i] + accumulation[i]; delay[i] = accumulation[i + 1024]; }
        previous = shape[frame];
        word((uint32_t)sequence[frame]); word((uint32_t)shape[frame]); floats(input, 1024); floats(pcm, 1024);
    }
    return ferror(stdout) ? 1 : 0;
}
