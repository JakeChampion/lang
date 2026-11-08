// Reference-only harness for Apple's ALAC decoder. This is not a Fern dependency.
// Source pin: macosforge/alac c38887c5c5e64a4b31108733bd79ca9b2496d987.
#include "ALACDecoder.h"
#include "ALACBitUtilities.h"
#include <cstdint>
#include <cstdio>
#include <fstream>
#include <iterator>
#include <vector>

static std::vector<uint8_t> read(const char *path) {
    std::ifstream input(path, std::ios::binary);
    return std::vector<uint8_t>(std::istreambuf_iterator<char>(input), {});
}

int main(int argc, char **argv) {
    if (argc != 3) return 1;
    auto configuration = read(argv[1]);
    auto packet = read(argv[2]);
    if (configuration.size() != 24 || packet.empty()) return 2;
    uint32_t capacity = uint32_t(configuration[0]) << 24 |
        uint32_t(configuration[1]) << 16 | uint32_t(configuration[2]) << 8 | configuration[3];
    uint32_t channels = configuration[9], depth = configuration[5];
    if (!capacity || capacity > 65536 || !channels || channels > 8 ||
        (depth != 16 && depth != 20 && depth != 24 && depth != 32)) return 3;
    ALACDecoder decoder;
    if (decoder.Init(configuration.data(), configuration.size())) return 4;
    uint32_t size = packet.size();
    packet.resize(size + 64, 0);
    BitBuffer bits;
    BitBufferInit(&bits, packet.data(), size);
    std::vector<uint8_t> output(size_t(capacity) * channels * 4);
    uint32_t frames = 0;
    int32_t status = decoder.Decode(&bits, output.data(), capacity, channels, &frames);
    if (status || frames > capacity) {
        std::fprintf(stderr, "ALAC reference error %d\n", status);
        return 5;
    }
    const int maps[8][8] = {
        {0}, {0,1}, {2,0,1}, {2,0,1,3}, {2,0,1,3,4},
        {2,0,1,4,5,3}, {2,0,1,5,6,4,3}, {2,4,5,0,1,6,7,3}
    };
    uint32_t bytes = (depth + 7) / 8;
    for (uint32_t frame = 0; frame < frames; ++frame) {
        int32_t samples[8] = {};
        for (uint32_t channel = 0; channel < channels; ++channel) {
            size_t offset = (size_t(frame) * channels + channel) * bytes;
            uint32_t raw = 0;
            for (uint32_t byte = 0; byte < bytes; ++byte)
                raw |= uint32_t(output[offset + byte]) << (8 * byte);
            uint32_t shift = 32 - bytes * 8;
            int32_t value = int32_t(raw << shift) >> shift;
            if (depth == 20) value >>= 4;
            samples[maps[channels - 1][channel]] = value;
        }
        for (uint32_t channel = 0; channel < channels; ++channel) {
            uint32_t value = uint32_t(samples[channel]);
            for (uint32_t byte = 0; byte < 4; ++byte)
                if (std::putchar((value >> (8 * byte)) & 255) == EOF) return 6;
        }
    }
    return std::fflush(stdout) == 0 ? 0 : 7;
}
