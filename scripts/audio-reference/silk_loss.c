/* Observe the unmodified libopus 22244de5a79bd1d6d623c32e72bf1954b56235be decoder. */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "silk/API.h"
#include "silk/main.h"

static opus_int16 captured[2][320];
static int captured_channels;
static int captured_frames;

opus_int __real_silk_resampler(silk_resampler_state_struct *, opus_int16 *, const opus_int16 *, opus_int32);
opus_int __wrap_silk_resampler(silk_resampler_state_struct *state, opus_int16 *out,
                              const opus_int16 *in, opus_int32 count) {
    if (captured_channels >= 2 || count < 0 || count > 320) exit(3);
    memcpy(captured[captured_channels++], in, (size_t)count * sizeof(*in));
    captured_frames = count;
    return __real_silk_resampler(state, out, in, count);
}

static void check(int value) {
    if (value < 0) { fprintf(stderr, "libopus error %d\n", value); exit(2); }
}

int main(void) {
    const int durations[] = {20, 10, 10, 20, 10, 20, 10, 20, 20};
    uint32_t seed = 842356;
    for (int rate = 8; rate <= 16; rate += 4) {
        for (int channels = 1; channels <= 2; channels++) {
            int bytes = 0;
            check(silk_Get_Decoder_Size(&bytes));
            void *decoder = calloc(1, (size_t)bytes);
            if (!decoder) return 4;
            check(silk_InitDecoder(decoder));
            int stream = 1;
            for (int step = 0; step < 9; step++) {
                int duration = durations[step];
                int lost = step == 0 || step == 2 || step == 3 || step == 4 || step == 7;
                unsigned char packet[1024] = {0};
                int length = 0;
                if (!lost) {
                    stream = step == 5 || step == 8 ? 1 : 2;
                    length = sizeof(packet);
                    for (int i = 0; i < length; i++) {
                        seed = seed * UINT32_C(1664525) + UINT32_C(1013904223);
                        packet[i] = (unsigned char)(seed >> 24);
                    }
                }
                silk_DecControlStruct control;
                memset(&control, 0, sizeof(control));
                control.nChannelsAPI = channels;
                control.nChannelsInternal = stream;
                control.API_sampleRate = rate * 1000;
                control.internalSampleRate = rate * 1000;
                control.payloadSize_ms = duration;
                ec_dec range;
                ec_dec_init(&range, packet, (opus_uint32)length);
                opus_res pcm[640];
                opus_int32 count = 0;
                captured_channels = 0;
                captured_frames = 0;
                check(silk_Decode(decoder, &control, lost, 1, &range, pcm, &count, 0));
                if (count != rate * duration || captured_frames != count) return 5;
                printf("{\"rate\":%d,\"channels\":%d,\"duration\":%d,\"stream\":%d,\"lost\":%d,\"packet\":\"",
                       rate, channels, duration, stream, lost);
                for (int i = 0; i < length; i++) printf("%02x", packet[i]);
                printf("\",\"losses\":%d,\"pcm\":[", ((silk_decoder_state *)decoder)->lossCnt);
                for (int i = 0; i < count; i++) {
                    for (int channel = 0; channel < channels; channel++) {
                        int source = captured_channels == 1 ? 0 : channel;
                        printf("%s%d", i || channel ? "," : "", captured[source][i]);
                    }
                }
                puts("]}");
            }
            free(decoder);
        }
    }
    return 0;
}
