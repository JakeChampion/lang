#define QOA_IMPLEMENTATION
#include "qoa.h"

int main(int argc, char **argv) {
    if (argc != 2) return 1;
    FILE *file = fopen(argv[1], "rb");
    if (!file || fseek(file, 0, SEEK_END)) return 1;
    long size = ftell(file);
    if (size < 16 || size > 16777216 || fseek(file, 0, SEEK_SET)) return 1;
    unsigned char *input = malloc(size);
    if (!input || fread(input, 1, size, file) != (size_t)size) return 1;
    fclose(file);
    int total = 0;
    for (int i = 0; i < 1000; i++) {
        qoa_desc desc = {0};
        short *samples = qoa_decode(input, size, &desc);
        if (!samples) return 2;
        total += samples[0] + samples[desc.samples * desc.channels - 1];
        free(samples);
    }
    printf("%d\n", total);
    free(input);
    return 0;
}
