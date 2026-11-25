// Reference-only benchmark harness for Apple's ALAC codec. This is not a Fern dependency.
// Source pin: macosforge/alac c38887c5c5e64a4b31108733bd79ca9b2496d987.
//
// encode CHANNELS BITS RATE INPUT OUTPUT: packs interleaved little-endian PCM into ALAC packets.
// decode INPUT ROUNDS: times ROUNDS sequential decodes of every packet, then prints the checksum.
#include "ALACBitUtilities.h"
#include "ALACDecoder.h"
#include "ALACEncoder.h"
#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <ctime>
#include <fstream>
#include <iterator>
#include <string>
#include <vector>

static std::vector<uint8_t> read(const char *path) {
    std::ifstream input(path, std::ios::binary);
    return std::vector<uint8_t>(std::istreambuf_iterator<char>(input), {});
}

static void put32(std::vector<uint8_t> &out, uint32_t value) {
    for (int i = 0; i < 4; ++i) out.push_back((value >> (8 * i)) & 255);
}

static uint32_t get32(const std::vector<uint8_t> &in, size_t at) {
    return uint32_t(in[at]) | uint32_t(in[at + 1]) << 8 | uint32_t(in[at + 2]) << 16 | uint32_t(in[at + 3]) << 24;
}

static int encode(int channels, int depth, int rate, const char *in, const char *out) {
    uint32_t flag = depth == 16 ? 1 : depth == 20 ? 2 : depth == 24 ? 3 : depth == 32 ? 4 : 0;
    if (channels < 1 || channels > 8 || !flag) return 3;
    auto pcm = read(in);
    uint32_t width = (depth + 7) / 8;
    uint32_t stride = width * channels;
    AudioFormatDescription input = {double(rate), kALACFormatLinearPCM,
        kALACFormatFlagIsSignedInteger | kALACFormatFlagIsPacked, stride, 1, stride, uint32_t(channels), uint32_t(depth), 0};
    AudioFormatDescription output = {double(rate), kALACFormatAppleLossless, flag, 0,
        kALACDefaultFrameSize, 0, uint32_t(channels), 0, 0};
    ALACEncoder encoder;
    encoder.SetFrameSize(kALACDefaultFrameSize);
    if (encoder.InitializeEncoder(output)) return 4;
    ALACSpecificConfig config;
    encoder.GetConfig(config);
    std::vector<uint8_t> file = {'F', 'A', 'L', 'B'};
    // GetConfig stores the multi-byte fields big-endian already.
    std::vector<uint8_t> cookie;
    auto raw = [&cookie](const void *field, size_t size) {
        cookie.insert(cookie.end(), (const uint8_t *)field, (const uint8_t *)field + size);
    };
    raw(&config.frameLength, 4);
    cookie.push_back(config.compatibleVersion);
    cookie.push_back(config.bitDepth);
    cookie.push_back(config.pb);
    cookie.push_back(config.mb);
    cookie.push_back(config.kb);
    cookie.push_back(config.numChannels);
    raw(&config.maxRun, 2);
    raw(&config.maxFrameBytes, 4);
    raw(&config.avgBitRate, 4);
    raw(&config.sampleRate, 4);
    put32(file, cookie.size());
    file.insert(file.end(), cookie.begin(), cookie.end());
    uint32_t frames = pcm.size() / stride;
    uint32_t packets = (frames + kALACDefaultFrameSize - 1) / kALACDefaultFrameSize;
    put32(file, frames);
    put32(file, packets);
    std::vector<uint8_t> buffer(kALACDefaultFrameSize * stride + kALACMaxEscapeHeaderBytes + 64);
    for (uint32_t at = 0; at < frames; at += kALACDefaultFrameSize) {
        uint32_t count = frames - at < kALACDefaultFrameSize ? frames - at : kALACDefaultFrameSize;
        int32_t bytes = count * stride;
        if (encoder.Encode(input, output, pcm.data() + size_t(at) * stride, buffer.data(), &bytes)) return 5;
        put32(file, bytes);
        file.insert(file.end(), buffer.begin(), buffer.begin() + bytes);
    }
    std::ofstream(out, std::ios::binary).write(reinterpret_cast<const char *>(file.data()), file.size());
    return 0;
}

struct Packets {
    std::vector<uint8_t> cookie;
    uint32_t frames;
    std::vector<std::vector<uint8_t>> packets;
};

static bool load(const char *path, Packets &out) {
    auto file = read(path);
    if (file.size() < 8 || std::memcmp(file.data(), "FALB", 4)) return false;
    size_t at = 4;
    uint32_t size = get32(file, at);
    at += 4;
    out.cookie.assign(file.begin() + at, file.begin() + at + size);
    at += size;
    out.frames = get32(file, at);
    uint32_t count = get32(file, at + 4);
    at += 8;
    for (uint32_t i = 0; i < count; ++i) {
        uint32_t bytes = get32(file, at);
        at += 4;
        std::vector<uint8_t> packet(file.begin() + at, file.begin() + at + bytes);
        packet.resize(bytes + 64, 0);
        out.packets.push_back(packet);
        at += bytes;
    }
    return at == file.size();
}

static int64_t now() {
    timespec t;
    clock_gettime(CLOCK_MONOTONIC, &t);
    return int64_t(t.tv_sec) * 1000000000 + t.tv_nsec;
}

static long peak_rss() {
    std::ifstream status("/proc/self/status");
    std::string line;
    while (std::getline(status, line))
        if (line.rfind("VmHWM:", 0) == 0) return std::strtol(line.c_str() + 6, nullptr, 10);
    return 0;
}

static int decode(const char *path, int rounds) {
    Packets in;
    if (!load(path, in) || rounds < 1) return 2;
    ALACDecoder decoder;
    if (decoder.Init(in.cookie.data(), in.cookie.size())) return 3;
    uint32_t channels = decoder.mConfig.numChannels, depth = decoder.mConfig.bitDepth, capacity = decoder.mConfig.frameLength;
    uint32_t width = (depth + 7) / 8;
    std::vector<uint8_t> output(size_t(capacity) * channels * 4);
    uint32_t observed = 0;
    uint64_t frames = 0;
    auto pass = [&](bool hash, uint32_t &checksum) -> bool {
        const int maps[8][8] = {
            {0}, {0, 1}, {2, 0, 1}, {2, 0, 1, 3}, {2, 0, 1, 3, 4},
            {2, 0, 1, 4, 5, 3}, {2, 0, 1, 5, 6, 4, 3}, {2, 4, 5, 0, 1, 6, 7, 3}};
        for (auto &packet : in.packets) {
            BitBuffer bits;
            BitBufferInit(&bits, packet.data(), packet.size() - 64);
            uint32_t count = 0;
            if (decoder.Decode(&bits, output.data(), capacity, channels, &count)) return false;
            frames += count;
            observed ^= output[0];
            if (!hash) continue;
            for (uint32_t frame = 0; frame < count; ++frame) {
                int32_t samples[8] = {};
                for (uint32_t channel = 0; channel < channels; ++channel) {
                    size_t offset = (size_t(frame) * channels + channel) * width;
                    uint32_t raw = 0;
                    for (uint32_t byte = 0; byte < width; ++byte) raw |= uint32_t(output[offset + byte]) << (8 * byte);
                    uint32_t shift = 32 - width * 8;
                    int32_t value = int32_t(raw << shift) >> shift;
                    if (depth == 20) value >>= 4;
                    samples[maps[channels - 1][channel]] = value;
                }
                for (uint32_t channel = 0; channel < channels; ++channel)
                    checksum = (checksum ^ uint32_t(samples[channel])) * 16777619u;
            }
        }
        return true;
    };
    uint32_t checksum = 2166136261u;
    for (int i = 0; i < 4; ++i)
        if (!pass(false, checksum)) return 4;
    frames = 0;
    int64_t started = now();
    for (int i = 0; i < rounds; ++i)
        if (!pass(false, checksum)) return 4;
    int64_t elapsed = now() - started;
    if (frames != uint64_t(in.frames) * rounds) return 5;
    uint64_t samples = frames * channels;
    if (!pass(true, checksum)) return 4;
    std::printf("samples=%llu\nchecksum=%u\nwall_ns=%lld\npeak_rss_kb=%ld\nobserved=%u\n",
        (unsigned long long)samples, checksum, (long long)elapsed, peak_rss(), observed);
    return 0;
}

int main(int argc, char **argv) {
    if (argc == 7 && !std::strcmp(argv[1], "encode"))
        return encode(std::atoi(argv[2]), std::atoi(argv[3]), std::atoi(argv[4]), argv[5], argv[6]);
    if (argc == 4 && !std::strcmp(argv[1], "decode")) return decode(argv[2], std::atoi(argv[3]));
    return 1;
}
