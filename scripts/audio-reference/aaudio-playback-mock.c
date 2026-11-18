/* Reuse the builder and stream lifecycle from the callback mock. */
#define AAudioStream_write callback_only_write
#include "aaudio-buffered-mock.c"
#undef AAudioStream_write
#include <assert.h>
#include <string.h>

extern const void *audio_mock_output_pointer(void);
static int direct, offset, result = -1, calls;
static int16_t received[2048];
static int16_t captured[65536];
static int capture, captured_samples;

void mock_write_plan(int borrow, int first, int accepted) {
    direct = borrow; offset = first; result = accepted; calls = 0;
}
int mock_write_calls(void) { return calls; }
int mock_written_sample(int index) { assert(index >= 0 && index < 2048); return received[index]; }
void mock_capture_begin(void) { capture = 1; captured_samples = 0; }
void mock_capture_end(void) { capture = 0; }
const void *mock_capture_data(void) { return captured; }
int mock_capture_size(void) { return captured_samples * 2; }

int32_t AAudioStream_write(Stream *stream, const void *data, int32_t frames, int64_t timeout) {
    assert(stream && !stream->closed && (stream->settings.channels == 1 || stream->settings.channels == 2) && stream->settings.format == 1);
    assert(frames > 0 && frames <= 1024 && timeout == INT64_C(7000000));
    int frame_bytes = stream->settings.channels * 2;
    const uint8_t *expected = audio_mock_output_pointer();
    if (direct) assert(data == expected + offset * frame_bytes);
    assert(memcmp(data, expected + offset * frame_bytes, (size_t)frames * frame_bytes) == 0);
    memcpy(received, data, (size_t)frames * frame_bytes);
    if (capture) {
        assert(captured_samples + frames * stream->settings.channels <= 65536);
        memcpy(captured + captured_samples, data, (size_t)frames * frame_bytes);
        captured_samples += frames * stream->settings.channels;
    }
    calls++;
    return result == -1 ? frames : result;
}
