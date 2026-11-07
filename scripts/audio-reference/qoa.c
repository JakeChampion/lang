#define QOA_IMPLEMENTATION
#include "qoa.h"
#include <stdio.h>

/* The fixture generator supplies canonical 44-byte PCM WAVE headers. */
int main(int argc, char **argv) {
    if (argc < 3) return 1;
    FILE *file = fopen(argv[1], "rb");
    if (!file) return 1;
    unsigned char header[44];
    if (fread(header, 1, sizeof(header), file) != sizeof(header)) return 1;
    qoa_desc desc = {0};
    desc.channels = header[22];
    desc.samplerate = 48000;
    if (!desc.channels || desc.channels > QOA_MAX_CHANNELS) return 1;
    if (fseek(file, 0, SEEK_END)) return 1;
    long size = ftell(file) - 44;
    if (size <= 0 || size > 16777216 || size % (2 * desc.channels)) return 1;
    if (fseek(file, 44, SEEK_SET)) return 1;
    short *pcm = malloc(size);
    if (!pcm || fread(pcm, 1, size, file) != (size_t)size) return 1;
    fclose(file);
    desc.samples = size / (2 * desc.channels);
    unsigned int encoded_size;
    unsigned char *encoded = qoa_encode(pcm, &desc, &encoded_size);
    if (!encoded) return 2;
    file = fopen(argv[2], "wb");
    if (!file || fwrite(encoded, 1, encoded_size, file) != encoded_size) return 2;
    fclose(file);
    if (argc > 3) {
        qoa_desc decoded_desc = {0};
        short *decoded = qoa_decode(encoded, encoded_size, &decoded_desc);
        if (!decoded) return 3;
        file = fopen(argv[3], "wb");
        if (!file) return 3;
        for (unsigned int i = 0; i < decoded_desc.samples * decoded_desc.channels; i++) {
            unsigned int value = (unsigned int)(int)decoded[i];
            unsigned char bytes[4] = {value & 255, (value >> 8) & 255, (value >> 16) & 255, (value >> 24) & 255};
            if (fwrite(bytes, 1, 4, file) != 4) return 3;
        }
        fclose(file);
        free(decoded);
    }
    free(encoded);
    free(pcm);
    return 0;
}
