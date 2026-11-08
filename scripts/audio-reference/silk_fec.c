/* Observe the unmodified libopus 22244de5a79bd1d6d623c32e72bf1954b56235be decoder. */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "silk/API.h"
#include "silk/main.h"
#include "celt/float_cast.h"

static void check(int value) {
    if (value < 0) { fprintf(stderr, "libopus error %d\n", value); exit(2); }
}

int main(void) {
    const int rates[] = {8000, 12000, 16000, 24000, 48000};
    const int durations[] = {10, 20, 40, 60};
    uint32_t seed = 3989123;
    int id = 0;
    for (int rate = 8; rate <= 16; rate += 4) {
        for (int target = 0; target < 5; target++) {
            for (int channels = 1; channels <= 2; channels++, id++) {
                int bytes = 0;
                check(silk_Get_Decoder_Size(&bytes));
                void *decoder = calloc(1, (size_t)bytes);
                if (!decoder) return 4;
                check(silk_InitDecoder(decoder));
                for (int step = 0; step < 4; step++) {
                    int duration = durations[step];
                    int stream = step == 1 || step == 2 ? 2 : 1;
                    int length = duration * 16;
                    unsigned char packet[960];
                    for (int i = 0; i < length; i++) {
                        seed = seed * UINT32_C(1664525) + UINT32_C(1013904223);
                        packet[i] = (unsigned char)(seed >> 24);
                    }
                    /* Decode the previous packet from FEC, then decode this packet. */
                    for (int fec = 1; fec >= 0; fec--) {
                        silk_DecControlStruct control;
                        memset(&control, 0, sizeof(control));
                        control.nChannelsAPI = channels;
                        control.nChannelsInternal = stream;
                        control.API_sampleRate = rates[target];
                        control.internalSampleRate = rate * 1000;
                        control.payloadSize_ms = duration;
                        ec_dec range;
                        ec_dec_init(&range, packet, (opus_uint32)length);
                        opus_res pcm[5760];
                        int count = 0;
                        int frames = duration > 20 ? duration / 20 : 1;
                        for (int frame = 0; frame < frames; frame++) {
                            opus_int32 decoded = 0;
                            check(silk_Decode(decoder, &control, fec ? 2 : 0, frame == 0,
                                              &range, pcm + count * channels, &decoded, 0));
                            count += decoded;
                        }
                        if (count != rates[target] / 1000 * duration) return 5;
                        silk_decoder_state *state = decoder;
                        printf("{\"case\":%d,\"rate\":%d,\"output_rate\":%d,\"channels\":%d,\"duration\":%d,\"stream\":%d,\"fec\":%d,\"packet\":\"",
                               id, rate, rates[target], channels, duration, stream, fec);
                        for (int i = 0; i < length; i++) printf("%02x", packet[i]);
                        printf("\",\"range\":%u,\"losses\":%d,\"flags\":[", range.rng, state[0].lossCnt);
                        for (int channel = 0; channel < stream; channel++) {
                            int mask = 0;
                            for (int i = 0; i < frames; i++) mask |= state[channel].LBRR_flags[i] << i;
                            printf("%s%d", channel ? "," : "", mask);
                        }
                        printf("],\"pcm\":[");
                        for (int i = 0; i < count * channels; i++) {
                            printf("%s%d", i ? "," : "", (int)RES2INT16(pcm[i]));
                        }
                        puts("]}");
                    }
                }
                free(decoder);
            }
        }
    }
    return 0;
}
