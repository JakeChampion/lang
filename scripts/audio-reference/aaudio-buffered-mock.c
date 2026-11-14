// A stand-in for libaaudio.so for the Buffered stream of std/android/audio. It
// keeps the settings of a builder and its data callback. mock_pull calls the
// callback on a thread of its own, as the audio thread of a device does, and
// keeps the samples that it gave.
#include <pthread.h>
#include <stdint.h>
#include <stdlib.h>

typedef int32_t (*data_callback)(void *stream, void *user, void *data, int32_t frames);

typedef struct {
    int32_t rate, channels, format, direction;
    data_callback callback;
    void *user;
} Builder;

typedef struct {
    Builder settings;
    int32_t started, flushes, closed;
} Stream;

static Stream *last;
static int16_t pulled[8192];
static int32_t pulled_frames;

int32_t AAudio_createStreamBuilder(Builder **out) {
    *out = calloc(1, sizeof(Builder));
    return 0;
}
void AAudioStreamBuilder_setDirection(Builder *b, int32_t v) { b->direction = v; }
void AAudioStreamBuilder_setSampleRate(Builder *b, int32_t v) { b->rate = v; }
void AAudioStreamBuilder_setChannelCount(Builder *b, int32_t v) { b->channels = v; }
void AAudioStreamBuilder_setFormat(Builder *b, int32_t v) { b->format = v; }
void AAudioStreamBuilder_setPerformanceMode(Builder *b, int32_t v) { (void)b; (void)v; }
void AAudioStreamBuilder_setBufferCapacityInFrames(Builder *b, int32_t v) { (void)b; (void)v; }
void AAudioStreamBuilder_setUsage(Builder *b, int32_t v) { (void)b; (void)v; }
void AAudioStreamBuilder_setContentType(Builder *b, int32_t v) { (void)b; (void)v; }
void AAudioStreamBuilder_setInputPreset(Builder *b, int32_t v) { (void)b; (void)v; }
void AAudioStreamBuilder_setDataCallback(Builder *b, data_callback cb, void *user) {
    b->callback = cb;
    b->user = user;
}
int32_t AAudioStreamBuilder_openStream(Builder *b, Stream **out) {
    Stream *s = calloc(1, sizeof(Stream));
    s->settings = *b;
    last = s;
    *out = s;
    return 0;
}
int32_t AAudioStreamBuilder_delete(Builder *b) { free(b); return 0; }
int32_t AAudioStream_requestStart(Stream *s) { s->started = 1; return 0; }
int32_t AAudioStream_requestPause(Stream *s) { s->started = 0; return 0; }
int32_t AAudioStream_requestFlush(Stream *s) { s->flushes++; return 0; }
int32_t AAudioStream_requestStop(Stream *s) { s->started = 0; return 0; }
int32_t AAudioStream_waitForStateChange(Stream *s, int32_t state, int32_t *next, int64_t ns) {
    (void)s; (void)state; (void)ns;
    *next = 0;
    return 0;
}
int32_t AAudioStream_close(Stream *s) { s->closed = 1; return 0; }
int32_t AAudioStream_getSampleRate(Stream *s) { return s->settings.rate; }
int32_t AAudioStream_getBufferCapacityInFrames(Stream *s) { (void)s; return 96; }
int32_t AAudioStream_setBufferSizeInFrames(Stream *s, int32_t frames) { (void)s; return frames; }
int32_t AAudioStream_getBufferSizeInFrames(Stream *s) { (void)s; return 96; }
// A stream with a data callback takes no write.
int32_t AAudioStream_write(Stream *s, void *b, int32_t f, int64_t ns) { (void)s; (void)b; (void)f; (void)ns; abort(); }
int32_t AAudioStream_read(Stream *s, void *b, int32_t f, int64_t ns) { (void)s; (void)b; (void)f; (void)ns; abort(); }
int32_t AAudioStream_getTimestamp(Stream *s, int32_t c, int64_t *p, int64_t *t) { (void)s; (void)c; (void)p; (void)t; return -895; }

static void *pull_thread(void *arg) {
    Stream *s = arg;
    int32_t result = s->settings.callback(s, s->settings.user, pulled, pulled_frames);
    return (void *)(intptr_t)result;
}

// Calls the data callback of the last stream for `frames` frames on a new
// thread, and gives its result.
int32_t mock_pull(int32_t frames) {
    if (last == NULL || last->settings.callback == NULL || frames * last->settings.channels > 8192) {
        return -1;
    }
    pulled_frames = frames;
    pthread_t t;
    void *result;
    pthread_create(&t, NULL, pull_thread, last);
    pthread_join(t, &result);
    return (int32_t)(intptr_t)result;
}

int32_t mock_sample(int32_t i) { return pulled[i]; }
int32_t mock_flushes(void) { return last == NULL ? -1 : last->flushes; }
int32_t mock_closed(void) { return last == NULL ? -1 : last->closed; }
int32_t mock_format(void) { return last == NULL ? -1 : last->settings.format; }
