/* A lifecycle and ABI test double. This file does not decode video. */
#include <assert.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <string.h>
#include <sys/types.h>

static int mode, live_formats, live_codecs, output_step, queued, released;
static unsigned char input_data[512];
static unsigned char output_data[8] = {99, 99, 1, 2, 3, 99, 99, 99};
static int active_encoder, named_hardware, surfaces, parameter_calls, fail_queue;
static int live_muxers, muxer_started, muxer_writes, muxer_token;
static int live_extractors, extractor_token, sample_position, sample_reads;
static int format_token, codec_token;

void mock_mode(int m) {
    assert(live_formats == 0 && live_codecs == 0);
    mode = m;
    assert(live_muxers == 0 && surfaces == 0 && live_extractors == 0);
    output_step = queued = released = active_encoder = named_hardware = parameter_calls = fail_queue = muxer_writes = 0;
}
int mock_live(void) { return live_formats + live_codecs + live_muxers + surfaces + live_extractors; }
int mock_queued(void) { return queued; }
int mock_released(void) { return released; }
int mock_parameters(void) { return parameter_calls; }
int mock_muxer_writes(void) { return muxer_writes; }
void mock_fail_next_queue(void) { fail_queue = 1; }
int mock_sample_reads(void) { return sample_reads; }
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
    else if (strcmp(key, "max-input-size") == 0) assert(value == 64 || value == 512);
    else if (strcmp(key, "bitrate") == 0 || strcmp(key, "video-bitrate") == 0) assert(value == 100000 || value == 200000);
    else if (strcmp(key, "frame-rate") == 0) assert(value == 30);
    else if (strcmp(key, "i-frame-interval") == 0) assert(value == 2);
    else if (strcmp(key, "color-format") == 0) assert(value == 19 || value == 2130708361);
    else if (strcmp(key, "bitrate-mode") == 0) assert(value == 1);
    else { assert(strcmp(key, "request-sync") == 0); assert(value == 0); }
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
    active_encoder = named_hardware = 0;
    live_codecs++;
    return &codec_token;
}
void *AMediaCodec_createCodecByName(const char *name) {
    assert(strstr(name, "avc") != NULL);
    if (mode == 2) return NULL;
    active_encoder = strstr(name, "encoder") != NULL;
    named_hardware = strstr(name, "vendor") != NULL;
    live_codecs++;
    return &codec_token;
}
int AMediaCodec_configure(void *c, void *f, void *w, void *crypto, uint32_t flags) {
    assert(c == &codec_token && f == &format_token && !crypto && live_codecs == 1);
    assert(active_encoder ? (!w && flags == 1) : (w == (void *)123 && flags == 0));
    return mode == 3 || (named_hardware && mode != 10) ? -10000 : 0;
}
int AMediaCodec_start(void *c) { assert(c == &codec_token); return mode == 4 ? -10000 : 0; }
int AMediaCodec_stop(void *c) { assert(c == &codec_token); return mode == 5 ? -10000 : 0; }
int AMediaCodec_delete(void *c) { assert(c == &codec_token); live_codecs--; return 0; }
int AMediaCodec_flush(void *c) { assert(c == &codec_token); output_step = 0; return 0; }
ssize_t AMediaCodec_dequeueInputBuffer(void *c, int64_t timeout) {
    assert(c == &codec_token && timeout >= 0);
    return timeout == 0 && mode != 16 ? -1 : 3;
}
uint8_t *AMediaCodec_getInputBuffer(void *c, size_t index, size_t *capacity) {
    assert(c == &codec_token && index == 3);
    *capacity = active_encoder ? sizeof(input_data) : 8;
    return input_data;
}
int AMediaCodec_queueInputBuffer(void *c, size_t index, size_t offset, size_t size, uint64_t time, uint32_t flags) {
    assert(c == &codec_token && index == 3 && offset == 0);
    if (flags == 4) assert(size == 0);
    else if (flags == 2) { assert(size == 5 && time == 0); assert(memcmp(input_data,"\0\0\0\1\147",5) == 0); }
    else if (active_encoder) { assert(size == 192 && time == 1000 && flags == 0); assert(input_data[0] == 16 && input_data[191] == 128); }
    else { assert(size == 3 && time == 123456 && flags == 1); assert(memcmp(input_data, "\1\2\3", 3) == 0); }
    if (fail_queue) { fail_queue = 0; return -10000; }
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
    default: info->offset = active_encoder ? 2 : 7; info->size = active_encoder ? 3 : 99; info->time = 123456; info->flags = active_encoder ? 1 : 4; return 2;
    }
}
void *AMediaCodec_getOutputFormat(void *c) { assert(c == &codec_token); live_formats++; return &format_token; }
int AMediaCodec_releaseOutputBuffer(void *c, size_t index, bool render) {
    assert(c == &codec_token && index == 2 && !render); released++; return 0;
}
int AMediaCodec_releaseOutputBufferAtTime(void *c, size_t index, int64_t time) {
    assert(c == &codec_token && index == 2 && time == 9876543210LL); released++; return 0;
}
uint8_t *AMediaCodec_getOutputBuffer(void *c, size_t index, size_t *capacity) {
    assert(c == &codec_token && index == 2); *capacity = sizeof(output_data); return output_data;
}
bool AMediaFormat_getBuffer(void *f, const char *key, void **data, size_t *size) {
    static uint8_t csd[] = {0, 0, 0, 1, 103};
    assert(f == &format_token);
    if (strcmp(key, "csd-0") != 0) return false;
    *data = csd; *size = sizeof(csd); return true;
}
bool AMediaFormat_getString(void *f, const char *key, const char **value) {
    assert(f == &format_token); if (strcmp(key,"mime")) return false; *value = "video/avc"; return true;
}
int AMediaCodec_createInputSurface(void *c, void **surface) {
    assert(c == &codec_token && active_encoder);
    if (mode == 7) return -10000;
    surfaces++; *surface = (void *)555; return 0;
}
void ANativeWindow_release(void *surface) { assert(surface == (void *)555); surfaces--; }
int AMediaCodec_signalEndOfInputStream(void *c) { assert(c == &codec_token && active_encoder); return 0; }
int AMediaCodec_setParameters(void *c, void *f) { assert(c == &codec_token && f == &format_token); parameter_calls++; return 0; }
void *AMediaMuxer_new(int fd, int format) { assert(fd == 77 && format == 0); live_muxers++; muxer_started = 0; return &muxer_token; }
int AMediaMuxer_delete(void *m) { assert(m == &muxer_token); live_muxers--; return 0; }
ssize_t AMediaMuxer_addTrack(void *m, void *f) { assert(m == &muxer_token && f == &format_token && !muxer_started); return 0; }
int AMediaMuxer_start(void *m) { assert(m == &muxer_token && !muxer_started); muxer_started = 1; return 0; }
int AMediaMuxer_stop(void *m) { assert(m == &muxer_token && muxer_started); muxer_started = 0; return 0; }
int AMediaMuxer_setOrientationHint(void *m, int degrees) { assert(m == &muxer_token && degrees == 90 && !muxer_started); return 0; }
int AMediaMuxer_writeSampleData(void *m, size_t track, const uint8_t *data, struct buffer_info *info) {
    assert(m == &muxer_token && muxer_started && track == 0);
    assert(info->offset == 0 && info->size == 3 && info->time == 123456 && info->flags == 1);
    assert(data == output_data + 2 && memcmp(data, "\1\2\3", 3) == 0);
    muxer_writes++; return 0;
}
void *AMediaExtractor_new(void) { live_extractors++; sample_position = sample_reads = 0; return &extractor_token; }
int AMediaExtractor_delete(void *e) { assert(e == &extractor_token); live_extractors--; return 0; }
int AMediaExtractor_setDataSourceFd(void *e, int fd, int64_t offset, int64_t length) { assert(e == &extractor_token && fd == 77 && offset == 0 && length == 100); return mode == 11 ? -10000 : 0; }
size_t AMediaExtractor_getTrackCount(void *e) { assert(e == &extractor_token); return mode == 12 ? 257 : 1; }
void *AMediaExtractor_getTrackFormat(void *e, size_t track) { assert(e == &extractor_token && track == 0); live_formats++; return &format_token; }
int AMediaExtractor_selectTrack(void *e, size_t track) { assert(e == &extractor_token && track == 0); return 0; }
ssize_t AMediaExtractor_getSampleSize(void *e) { assert(e == &extractor_token); return sample_position >= 2 ? -1 : mode == 13 ? 513 : 3; }
int64_t AMediaExtractor_getSampleTime(void *e) { assert(e == &extractor_token); return 123456; }
uint32_t AMediaExtractor_getSampleFlags(void *e) { assert(e == &extractor_token); return mode == 14 ? 2 : 1; }
int AMediaExtractor_getSampleTrackIndex(void *e) { assert(e == &extractor_token); return 0; }
ssize_t AMediaExtractor_readSampleData(void *e, uint8_t *data, size_t capacity) {
    assert(e == &extractor_token && capacity >= 3); memcpy(data,"\1\2\3",3); sample_reads++; return mode == 15 ? 2 : 3;
}
bool AMediaExtractor_advance(void *e) { assert(e == &extractor_token); return ++sample_position < 2; }
int AMediaExtractor_seekTo(void *e, int64_t time, int seek_mode) { assert(e == &extractor_token && (time == 200000 || time == 0) && seek_mode == 0); sample_position = 0; return 0; }
