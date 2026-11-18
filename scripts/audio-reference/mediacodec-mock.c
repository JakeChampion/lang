/* An independent NDK contract test. This mock does not decode compressed audio. */
#include <assert.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

typedef struct { int rate, channels, maximum, profile, encoding, mask, csd; char mime[64]; } Format;
typedef struct { int hardware, stage; } Codec;
typedef struct { int32_t offset, size; int64_t time; uint32_t flags; } Info;
static int mode, live, released, queued;
static unsigned char input[16], output[16] = {99, 99, 1, 2, 3, 4, 99};

void audio_mock_mode(int value) { assert(live == 0); mode = value; released = queued = 0; }
int audio_mock_live(void) { return live; }
int audio_mock_released(void) { return released; }
int audio_mock_queued(void) { return queued; }
void *AMediaFormat_new(void) { if (mode == 1) return NULL; live++; return calloc(1, sizeof(Format)); }
int32_t AMediaFormat_delete(Format *f) { assert(f); live--; free(f); return 0; }
void AMediaFormat_setString(Format *f, const char *key, const char *value) {
    assert(!strcmp(key, "mime") && !strcmp(value, "audio/mp4a-latm"));
    strcpy(f->mime, value);
}
void AMediaFormat_setInt32(Format *f, const char *key, int32_t value) {
    if (!strcmp(key, "sample-rate")) f->rate = value;
    else if (!strcmp(key, "channel-count")) f->channels = value;
    else if (!strcmp(key, "max-input-size")) f->maximum = value;
    else { assert(!strcmp(key, "aac-profile")); f->profile = value; }
}
void AMediaFormat_setBuffer(Format *f, const char *key, const void *data, size_t size) {
    assert(size == 2 && ((const unsigned char *)data)[0] == 18 && ((const unsigned char *)data)[1] == 16);
    if (!strcmp(key, "csd-0")) f->csd |= 1;
    else if (!strcmp(key, "csd-1")) f->csd |= 2;
    else { assert(!strcmp(key, "csd-2")); f->csd |= 4; }
}
void *AMediaCodec_createCodecByName(const char *name) {
    assert(!strcmp(name, "c2.vendor.avc.decoder") || !strcmp(name, "c2.android.avc.decoder"));
    if (mode == 2) return NULL;
    Codec *c = calloc(1, sizeof(*c));
    c->hardware = !strcmp(name, "c2.vendor.avc.decoder"); live++; return c;
}
int32_t AMediaCodec_configure(Codec *c, Format *f, void *window, void *crypto, uint32_t flags) {
    assert(c && !window && !crypto && !flags);
    assert(f->rate == 48000 && f->channels == 2 && f->maximum == 8 && f->profile == 2);
    assert(!strcmp(f->mime, "audio/mp4a-latm") && f->csd == 7);
    return mode == 3 || (mode == 4 && c->hardware) ? -1 : 0;
}
int32_t AMediaCodec_start(Codec *c) { assert(c); return mode == 5 ? -1 : 0; }
int32_t AMediaCodec_stop(Codec *c) { assert(c); return mode == 6 ? -1 : 0; }
int32_t AMediaCodec_delete(Codec *c) { assert(c); live--; free(c); return 0; }
int32_t AMediaCodec_flush(Codec *c) { assert(c); c->stage = 0; return mode == 7 ? -1 : 0; }
int64_t AMediaCodec_dequeueInputBuffer(Codec *c, int64_t timeout) {
    assert(c && timeout == 0); return mode == 8 ? -1 : 0;
}
void *AMediaCodec_getInputBuffer(Codec *c, size_t index, size_t *capacity) {
    assert(c && index == 0); *capacity = sizeof(input); return input;
}
int32_t AMediaCodec_queueInputBuffer(Codec *c, size_t index, size_t offset, size_t size, uint64_t time, uint32_t flags) {
    assert(c && index == 0 && offset == 0 && time == 123);
    assert((size == 2 && input[0] == 42 && input[1] == 43 && !flags) || (size == 0 && flags == 4));
    queued++; return mode == 9 ? -1 : 0;
}
int64_t AMediaCodec_dequeueOutputBuffer(Codec *c, Info *info, int64_t timeout) {
    assert(c && timeout == 0);
    switch (c->stage++) {
    case 0: return -2;
    case 1: return -3;
    case 2: *info = (Info){2, mode == 10 ? 20 : mode == 11 ? -1 : 4, 123, 0}; return 0;
    case 3: *info = (Info){0, 0, 456, 4}; return 1;
    default: return -1;
    }
}
void *AMediaCodec_getOutputFormat(Codec *c) {
    assert(c); Format *f = AMediaFormat_new();
    f->rate = mode == 12 ? 96000 : mode == 13 ? 0 : 48000;
    f->channels = mode == 14 ? 9 : 2;
    f->encoding = mode == 15 ? 3 : mode == 16 ? 4 : 2;
    f->mask = 12; return f;
}
uint8_t AMediaFormat_getInt32(Format *f, const char *key, int32_t *value) {
    if (!strcmp(key, "sample-rate")) *value = f->rate;
    else if (!strcmp(key, "channel-count")) *value = f->channels;
    else if (!strcmp(key, "channel-mask")) *value = f->mask;
    else { assert(!strcmp(key, "pcm-encoding")); if (mode == 17) return 0; *value = f->encoding; }
    return 1;
}
void *AMediaCodec_getOutputBuffer(Codec *c, size_t index, size_t *capacity) {
    assert(c && index == 0); *capacity = mode == 18 ? 3 : sizeof(output); return mode == 19 ? NULL : output;
}
int32_t AMediaCodec_releaseOutputBuffer(Codec *c, size_t index, uint8_t render) {
    assert(c && index < 2 && render == 0); released++; return 0;
}
