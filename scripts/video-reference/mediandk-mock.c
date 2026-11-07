/* A lifecycle and ABI test double. This file does not decode video. */
#include <assert.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <string.h>
#include <sys/types.h>

static int mode, live_formats, live_codecs, output_step, queued, released;
static unsigned char input_data[8];
static int format_token, codec_token;

void mock_mode(int m) {
    assert(live_formats == 0 && live_codecs == 0);
    mode = m;
    output_step = queued = released = 0;
}
int mock_live(void) { return live_formats + live_codecs; }
int mock_queued(void) { return queued; }
int mock_released(void) { return released; }
void *AMediaFormat_new(void) {
    if (mode == 1) return NULL;
    live_formats++;
    return &format_token;
}
int AMediaFormat_delete(void *f) { assert(f == &format_token); live_formats--; return 0; }
void AMediaFormat_setString(void *f, const char *key, const char *value) {
    assert(f == &format_token && strcmp(key, "mime") == 0 && strcmp(value, "video/avc") == 0);
}
void AMediaFormat_setInt32(void *f, const char *key, int32_t value) {
    assert(f == &format_token);
    if (strcmp(key, "width") == 0) assert(value == 16);
    else if (strcmp(key, "height") == 0) assert(value == 8);
    else { assert(strcmp(key, "max-input-size") == 0); assert(value == 64); }
}
void AMediaFormat_setBuffer(void *f, const char *key, const void *data, size_t size) {
    assert(f == &format_token && size == 5);
    assert(strcmp(key, "csd-0") == 0 || strcmp(key, "csd-1") == 0);
    assert(memcmp(data, "\0\0\0\1\147", 5) == 0);
}
bool AMediaFormat_getInt32(void *f, const char *key, int32_t *out) {
    assert(f == &format_token);
    if (strcmp(key, "width") == 0) *out = 16;
    else if (strcmp(key, "height") == 0) *out = 8;
    else if (strcmp(key, "crop-left") == 0) *out = 2;
    else if (strcmp(key, "crop-right") == 0) *out = 13;
    else if (strcmp(key, "color-standard") == 0) *out = 1;
    else return false;
    return true;
}
void *AMediaCodec_createDecoderByType(const char *mime) {
    assert(strcmp(mime, "video/avc") == 0);
    if (mode == 2) return NULL;
    live_codecs++;
    return &codec_token;
}
int AMediaCodec_configure(void *c, void *f, void *w, void *crypto, uint32_t flags) {
    assert(c == &codec_token && f == &format_token && w == (void *)123 && !crypto && flags == 0);
    return mode == 3 ? -10000 : 0;
}
int AMediaCodec_start(void *c) { assert(c == &codec_token); return mode == 4 ? -10000 : 0; }
int AMediaCodec_stop(void *c) { assert(c == &codec_token); return mode == 5 ? -10000 : 0; }
int AMediaCodec_delete(void *c) { assert(c == &codec_token); live_codecs--; return 0; }
int AMediaCodec_flush(void *c) { assert(c == &codec_token); output_step = 0; return 0; }
ssize_t AMediaCodec_dequeueInputBuffer(void *c, int64_t timeout) {
    assert(c == &codec_token && timeout >= 0);
    return timeout == 0 ? -1 : 3;
}
uint8_t *AMediaCodec_getInputBuffer(void *c, size_t index, size_t *capacity) {
    assert(c == &codec_token && index == 3);
    *capacity = sizeof(input_data);
    return input_data;
}
int AMediaCodec_queueInputBuffer(void *c, size_t index, size_t offset, size_t size, uint64_t time, uint32_t flags) {
    assert(c == &codec_token && index == 3 && offset == 0);
    if (flags == 4) assert(size == 0);
    else { assert(size == 3 && time == 123456 && flags == 1); assert(memcmp(input_data, "\1\2\3", 3) == 0); }
    queued++;
    return 0;
}
struct buffer_info { int32_t offset, size; int64_t time; uint32_t flags; };
ssize_t AMediaCodec_dequeueOutputBuffer(void *c, struct buffer_info *info, int64_t timeout) {
    assert(c == &codec_token && timeout >= 0);
    switch (output_step++) {
    case 0: return -1;
    case 1: return -2;
    case 2: return -3;
    default: info->offset = 7; info->size = 99; info->time = 123456; info->flags = 4; return 2;
    }
}
void *AMediaCodec_getOutputFormat(void *c) { assert(c == &codec_token); live_formats++; return &format_token; }
int AMediaCodec_releaseOutputBuffer(void *c, size_t index, bool render) {
    assert(c == &codec_token && index == 2 && !render); released++; return 0;
}
int AMediaCodec_releaseOutputBufferAtTime(void *c, size_t index, int64_t time) {
    assert(c == &codec_token && index == 2 && time == 9876543210LL); released++; return 0;
}
