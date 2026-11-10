/* Use FFmpeg's independent coded-bitstream reader for one SPS NAL. */
#include <stdio.h>
#include <libavcodec/bsf.h>
#include <libavcodec/packet.h>
#include <libavutil/log.h>

int main(int argc, char **argv) {
    if (argc != 2) return 1;
    FILE *file = fopen(argv[1], "rb");
    if (!file) return 2;
    AVBSFContext *context = NULL;
    AVPacket *packet = av_packet_alloc();
    int result = 3;
    const AVBitStreamFilter *filter = av_bsf_get_by_name("trace_headers");
    if (!packet || !filter || av_bsf_alloc(filter, &context) < 0) goto done;
    context->par_in->codec_id = AV_CODEC_ID_H264;
    context->par_in->codec_type = AVMEDIA_TYPE_VIDEO;
    context->time_base_in = (AVRational){1, 25};
    av_log_set_level(AV_LOG_TRACE);
    if (av_bsf_init(context) < 0 || fseek(file, 0, SEEK_END) != 0) goto done;
    long size = ftell(file);
    if (size < 1 || size > 65536 || fseek(file, 0, SEEK_SET) != 0) goto done;
    if (av_new_packet(packet, (int)size + 4) < 0) goto done;
    packet->data[0] = packet->data[1] = packet->data[2] = 0;
    packet->data[3] = 1;
    if (fread(packet->data + 4, 1, (size_t)size, file) != (size_t)size) goto done;
    if (av_bsf_send_packet(context, packet) < 0 || av_bsf_receive_packet(context, packet) < 0) goto done;
    result = 0;
done:
    fclose(file);
    av_packet_free(&packet);
    av_bsf_free(&context);
    return result;
}
