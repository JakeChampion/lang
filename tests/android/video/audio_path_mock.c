// AAudio transport controls check whole stereo frames and bounded writes.
#include <assert.h>
#include <stdint.h>
#include <stddef.h>
#include <stdlib.h>
#include <stdio.h>
#include <unistd.h>

struct stream { int closed; int frames; int base; };
static struct stream streams[16];
static int count, mode, starts, pauses, writes, closes, base;
static int16_t expected[48000];
static size_t expected_count;

void av_audio_reset(void) {
    for (int i = 0; i < count; ++i) assert(streams[i].closed);
    count = mode = starts = pauses = writes = closes = base = 0;
    expected_count = 0;
    const char *path = getenv("AV_AUDIO_REFERENCE");
    if (path) {
        FILE *file = fopen(path, "rb");
        assert(file);
        expected_count = fread(expected, sizeof *expected, 48000, file);
        assert(expected_count == 38400 && fgetc(file) == EOF);
        assert(fclose(file) == 0);
    }
}
int av_audio_truncate(void) {
    const char *path = getenv("AV_AUDIO_FILE");
    assert(path);
    return truncate(path, 0);
}
void av_audio_mode(int value) { mode = value; }
void av_audio_base(int value) { base = value; }
int av_audio_count(int what) {
    switch (what) { case 0: return starts; case 1: return pauses;
    case 2: return writes; case 3: return closes; default: abort(); }
}
static struct stream *checked(void *ptr) {
    struct stream *s = ptr;
    assert(s >= streams && s < streams + count && !s->closed);
    return s;
}
int32_t AAudio_createStreamBuilder(void **builder) { *builder = (void *)(uintptr_t)123; return 0; }
void AAudioStreamBuilder_setDirection(void *b, int32_t n) { assert(b == (void *)(uintptr_t)123 && n == 0); }
void AAudioStreamBuilder_setSampleRate(void *b, int32_t n) { assert(b == (void *)(uintptr_t)123 && n == 48000); }
void AAudioStreamBuilder_setChannelCount(void *b, int32_t n) { assert(b == (void *)(uintptr_t)123 && n == 2); }
void AAudioStreamBuilder_setFormat(void *b, int32_t n) { assert(b == (void *)(uintptr_t)123 && n == 1); }
void AAudioStreamBuilder_setPerformanceMode(void *b, int32_t n) { assert(b == (void *)(uintptr_t)123 && n == 10); }
void AAudioStreamBuilder_setUsage(void *b, int32_t n) { assert(b == (void *)(uintptr_t)123 && n == 1); }
void AAudioStreamBuilder_setContentType(void *b, int32_t n) { assert(b == (void *)(uintptr_t)123 && n == 2); }
void AAudioStreamBuilder_setInputPreset(void) { abort(); }
void AAudioStream_getTimestamp(void) { abort(); }
void AAudioStreamBuilder_setDataCallback(void) { abort(); }
void AAudioStream_getBufferSizeInFrames(void) { abort(); }
void AAudioStreamBuilder_setBufferCapacityInFrames(void *b, int32_t n) { assert(b == (void *)(uintptr_t)123 && n == 12000); }
int32_t AAudioStreamBuilder_openStream(void *b, void **out) {
    assert(b == (void *)(uintptr_t)123 && count < 16);
    streams[count] = (struct stream){ .base = base };
    *out = streams + count++;
    return 0;
}
int32_t AAudioStreamBuilder_delete(void *b) { assert(b == (void *)(uintptr_t)123); return 0; }
int32_t AAudioStream_getSampleRate(void *p) { checked(p); return mode == 3 ? 44100 : 48000; }
int32_t AAudioStream_getBufferCapacityInFrames(void *p) { checked(p); return 12000; }
int32_t AAudioStream_setBufferSizeInFrames(void *p, int32_t n) { checked(p); assert(n == 12000); return n; }
int32_t AAudioStream_requestStart(void *p) { checked(p); starts++; return mode == 4 ? -899 : 0; }
int32_t AAudioStream_requestPause(void *p) { checked(p); pauses++; return 0; }
int32_t AAudioStream_close(void *p) { checked(p)->closed = 1; closes++; return 0; }
int32_t AAudioStream_write(void *p, const void *buffer, int32_t frames, int64_t timeout_ns) {
    struct stream *s = checked(p);
    const int16_t *samples = buffer;
    assert(frames > 0 && frames <= 1024 && timeout_ns == 0);
    for (int i = 0; i < frames * 2; ++i) {
        int at = s->base + s->frames * 2 + i;
        if (expected_count) {
            assert(at >= 0 && (size_t)at < expected_count);
            assert(abs((int)samples[i] - (int)expected[at]) <= 1);
        } else {
            assert(samples[i] == at % 30000);
        }
    }
    writes++;
    int accepted = mode == 1 ? (frames < 3 ? frames : 3) : mode == 2 ? 0 : frames;
    if (mode == 5) return frames + 1;
    s->frames += accepted;
    return accepted;
}
