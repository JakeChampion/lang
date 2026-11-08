/* Independent FDK AAC check of the correlated PNS stereo relation. */
#include <cstdio>
#include <vector>
#include "aacdecoder_lib.h"

int main() {
    const UCHAR packet[] = {255,241,80,128,2,95,252,33,0,85,166,134,0,45,52,48,1,192};
    std::vector<UCHAR> input;
    for (int i = 0; i < 5; ++i) input.insert(input.end(), packet, packet + sizeof(packet));
    HANDLE_AACDECODER decoder = aacDecoder_Open(TT_MP4_ADTS, 1);
    if (!decoder) return 1;
    UCHAR *data = input.data();
    UINT bytes = (UINT)input.size(), valid = bytes;
    if (aacDecoder_Fill(decoder, &data, &bytes, &valid) != AAC_DEC_OK) return 2;
    INT_PCM pcm[8192];
    bool nonzero = false;
    for (int frame = 0; frame < 3; ++frame) {
        if (aacDecoder_DecodeFrame(decoder, pcm, 8192, 0) != AAC_DEC_OK) return 3;
        const CStreamInfo *info = aacDecoder_GetStreamInfo(decoder);
        if (info->sampleRate != 44100 || info->numChannels != 2 || info->frameSize != 1024) return 4;
        for (int i = 0; i < info->frameSize; ++i) {
            if (pcm[2*i] != pcm[2*i+1]) return 5;
            nonzero |= pcm[2*i] != 0;
        }
    }
    aacDecoder_Close(decoder);
    if (!nonzero) return 6;
    std::puts("FDK AAC: correlated PNS produces equal, nonzero channels");
    return 0;
}
