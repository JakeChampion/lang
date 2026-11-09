// Call the pinned WavPack metadata readers and decorrelation kernels.
#include "unpack.c"
#include "decorr_utils.c"
#include "entropy_utils.c"
#include <stdio.h>

int main(void) {
    int32_t header[5];
    while (fread(header, sizeof(*header), 5, stdin) == 5) {
        int channels = header[0], frames = header[1];
        if (channels < 1 || channels > 2 || frames < 0 || frames > 8192) return 1;
        int adaptive = header[2] & 256;
        unsigned char term = (unsigned char)header[2];
        unsigned char weights[2] = {(unsigned char)header[3], (unsigned char)header[4]};
        int value = (term & 31) - 5;
        int count = value > 8 ? channels * 2 : value < 0 ? 2 : channels * value;
        if (count < 0 || count > 16) return 1;
        unsigned char history[32];
        int32_t samples[16384], residuals[8192];
        if (fread(history, 2, (size_t)count, stdin) != (size_t)count) return 2;
        if (!adaptive && fread(samples, 4, (size_t)frames * channels, stdin) != (size_t)frames * channels) return 2;
        WavpackStream stream = {0};
        stream.wphdr.flags = channels == 1 ? MONO_FLAG : 0;
        WavpackMetadata metadata = {0};
        metadata.data = &term; metadata.byte_length = 1;
        if (!read_decorr_terms(&stream, &metadata)) return 3;
        metadata.data = weights; metadata.byte_length = channels;
        if (!read_decorr_weights(&stream, &metadata)) return 3;
        metadata.data = history; metadata.byte_length = count * 2;
        if (!read_decorr_samples(&stream, &metadata)) return 3;
        if (adaptive) {
            if (channels != 1 || value != 17) return 5;
            struct decorr_pass *pass = stream.decorr_passes;
            for (int i = 0; i < frames; ++i) {
                int32_t source = 2 * pass->samples_A[0] - pass->samples_A[1];
                residuals[i] = (source < 0 ? -1 : 1) * (header[2] & 512 ? -1 : 1);
                samples[i] = residuals[i];
                decorr_mono_pass(pass, samples + i, 1);
            }
            if (pass->weight_A >= -32768 && pass->weight_A <= 32767) return 5;
            int32_t final_weight = pass->weight_A;
            if (fwrite(&final_weight, 4, 1, stdout) != 1 ||
                fwrite(residuals, 4, (size_t)frames, stdout) != (size_t)frames) return 4;
        }
        else if (channels == 1) decorr_mono_pass(stream.decorr_passes, samples, frames);
        else decorr_stereo_pass(stream.decorr_passes, samples, frames);
        if (fwrite(samples, 4, (size_t)frames * channels, stdout) != (size_t)frames * channels) return 4;
    }
    return ferror(stdin) || !feof(stdin) ? 2 : 0;
}
