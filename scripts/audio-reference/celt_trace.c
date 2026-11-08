/* Trace the unmodified CELT decoder in pinned libopus. */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "opus.h"
#include "celt/celt.h"

static uint32_t seed;
static unsigned random_value(void) {
    seed = seed * UINT32_C(1664525) + UINT32_C(1013904223);
    return seed >> 8;
}

static void check(int value) {
    if (value < 0) { fprintf(stderr, "libopus error %d\n", value); exit(2); }
}

int main(int argc, char **argv) {
    if (argc != 4) return 2;
    seed = (uint32_t)strtoul(argv[1], NULL, 10);
    const int scripts = atoi(argv[2]), frames = atoi(argv[3]);
    const int rates[] = {48000, 24000, 16000, 12000, 8000};
    for (int script = 0; script < scripts; script++) {
        int rate = rates[random_value() % 5];
        int channels = 1 + random_value() % 2;
        CELTDecoder *decoder = calloc(1, (size_t)celt_decoder_get_size(channels));
        if (!decoder) return 3;
        check(celt_decoder_init(decoder, rate, channels));
        printf("s %d %d\n", rate, channels);
        for (int frame = 0; frame < frames; frame++) {
            int lm = random_value() % 4;
            int stream = 1 + random_value() % 2;
            int start = random_value() % 8 == 0 ? 17 : 0;
            int end = 21;
            if (random_value() % 4 == 0) end = 13 + random_value() % 9;
            if (end <= start) end = 21;
            int length;
            if (random_value() % 4 == 0) length = 2 + random_value() % 1274;
            else length = 2 + random_value() % 120;
            unsigned char packet[1275];
            for (int i = 0; i < length; i++) packet[i] = (unsigned char)random_value();
            if (random_value() % 16 == 0) packet[0] = packet[1] = 255;
            if (random_value() % 5 == 0) { length = 0; start = 0; }
            int size = (120 << lm) / (48000 / rate);
            check(celt_decoder_ctl(decoder, CELT_SET_CHANNELS(stream)));
            check(celt_decoder_ctl(decoder, CELT_SET_START_BAND(start)));
            check(celt_decoder_ctl(decoder, CELT_SET_END_BAND(end)));
            float samples[1920];
            int result = celt_decode_with_ec(decoder, length ? packet : NULL, length, samples, size, NULL, 0);
            opus_uint32 range = 0;
            check(celt_decoder_ctl(decoder, OPUS_GET_FINAL_RANGE(&range)));
            printf("d %d %d %d %d %d %d %u |", lm, stream, start, end, length, result < 0 ? -1 : result, range);
            if (result < 0) printf(" internal CELT error");
            else for (int i = 0; i < result * channels; i++) {
                int32_t bits;
                memcpy(&bits, &samples[i], sizeof(bits));
                printf(" %d", bits);
            }
            puts("");
        }
        free(decoder);
    }
    return 0;
}
