/* Generate packets and PCM with unmodified, pinned libopus. */
#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "opus.h"
#include "src/opus_private.h"

static void check(int value) {
    if (value < 0) { fprintf(stderr, "libopus error %d\n", value); exit(2); }
}

static void emit(OpusDecoder *decoder, int id, int rate, int channels,
                 int action, const unsigned char *packet, int length, int frames) {
    if (action == 3) check(opus_decoder_ctl(decoder, OPUS_RESET_STATE));
    float pcm[11520];
    int count = opus_decode_float(decoder, packet, length, pcm, frames, action == 2);
    check(count);
    if (count != frames) exit(3);
    opus_uint32 range = 0;
    check(opus_decoder_ctl(decoder, OPUS_GET_FINAL_RANGE(&range)));
    printf("{\"case\":%d,\"rate\":%d,\"channels\":%d,\"action\":%d,\"frames\":%d,\"range\":%u,\"packet\":\"",
           id, rate, channels, action, frames, range);
    for (int i = 0; i < length; i++) printf("%02x", packet[i]);
    printf("\",\"pcm\":[");
    for (int i = 0; i < count * channels; i++) {
        uint32_t bits;
        memcpy(&bits, &pcm[i], sizeof(bits));
        printf("%s%u", i ? "," : "", bits);
    }
    puts("]}");
}

int main(void) {
    const int rates[] = {8000, 12000, 16000, 24000, 48000};
    const int modes[] = {1000, 1000, 1000, 1000, 1001, 1001, 1002, 1002,
                         1002, 1002, 1001, 1000, 1002, 1000, 1001, 1002};
    const int sizes[] = {480, 960, 1920, 2880, 480, 960, 120, 240,
                         480, 960, 960, 960, 960, 480, 480, 120};
    const int losses[] = {1, 3, 5, 12, 48};
    int id = 0;
    for (int target = 0; target < 5; target++) {
        for (int channels = 1; channels <= 2; channels++, id++) {
            int error = 0;
            OpusEncoder *encoder = opus_encoder_create(48000, 2, OPUS_APPLICATION_AUDIO, &error);
            check(error);
            OpusDecoder *decoder = opus_decoder_create(rates[target], channels, &error);
            check(error);
            check(opus_encoder_ctl(encoder, OPUS_SET_INBAND_FEC(1)));
            check(opus_encoder_ctl(encoder, OPUS_SET_PACKET_LOSS_PERC(30)));
            check(opus_encoder_ctl(encoder, OPUS_SET_COMPLEXITY(10)));
            uint32_t seed = 179231;
            int time = 0;
            emit(decoder, id, rates[target], channels, 1, NULL, 0, rates[target] / 400);
            for (int step = 0; step < 16; step++) {
                int mode = modes[step];
                int bandwidth = mode == 1001 ? (step == 4 ? OPUS_BANDWIDTH_SUPERWIDEBAND : OPUS_BANDWIDTH_FULLBAND) :
                                mode == 1002 ? OPUS_BANDWIDTH_FULLBAND :
                                step == 0 ? OPUS_BANDWIDTH_NARROWBAND : step == 1 ? OPUS_BANDWIDTH_MEDIUMBAND : OPUS_BANDWIDTH_WIDEBAND;
                check(opus_encoder_ctl(encoder, OPUS_SET_FORCE_MODE(mode)));
                check(opus_encoder_ctl(encoder, OPUS_SET_BANDWIDTH(bandwidth)));
                check(opus_encoder_ctl(encoder, OPUS_SET_BITRATE(mode == 1000 ? 24000 : mode == 1001 ? 64000 : 96000)));
                check(opus_encoder_ctl(encoder, OPUS_SET_FORCE_CHANNELS(step % 4 == 0 ? 1 : 2)));
                float input[5760];
                for (int i = 0; i < sizes[step]; i++, time++) {
                    seed = seed * UINT32_C(1664525) + UINT32_C(1013904223);
                    float noise = ((int)(seed >> 16) - 32768) * (0.01f / 32768.0f);
                    input[2*i] = 0.25f * sinf((float)time * 0.041f) + noise;
                    input[2*i+1] = 0.2f * sinf((float)time * 0.029f) - noise;
                }
                unsigned char packet[1276];
                int length = opus_encode_float(encoder, input, sizes[step], packet, sizeof(packet));
                check(length);
                int frames = sizes[step] / (48000 / rates[target]);
                if (step == 2 || step == 5 || step == 8) {
                    int requested = frames + (step == 5 ? rates[target] / 50 : 0);
                    emit(decoder, id, rates[target], channels, 2, packet, length, requested);
                }
                emit(decoder, id, rates[target], channels, 0, packet, length, frames);
                if (step % 3 == 2) {
                    emit(decoder, id, rates[target], channels, 1, NULL, 0, losses[step / 3] * rates[target] / 400);
                }
            }
            const unsigned char silence_long[] = {88};
            const unsigned char silence_hybrid[] = {96, 0};
            emit(decoder, id, rates[target], channels, 0, silence_long, 1, rates[target]*60/1000);
            emit(decoder, id, rates[target], channels, 0, silence_hybrid, 2, rates[target]/100);
            emit(decoder, id, rates[target], channels, 2, NULL, 0, rates[target]/50);
            emit(decoder, id, rates[target], channels, 3, NULL, 0, rates[target]/50);
            opus_encoder_destroy(encoder);
            opus_decoder_destroy(decoder);
        }
    }
    return 0;
}
