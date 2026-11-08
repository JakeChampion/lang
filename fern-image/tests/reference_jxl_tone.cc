#include "lib/jxl/enc_bit_writer.h"
#include "lib/jxl/fields.h"
#include "lib/jxl/image_metadata.h"
#include "lib/jxl/memory_manager_internal.h"

#include <array>
#include <cstdint>
#include <cstring>
#include <iomanip>
#include <iostream>
#include <string>
#include <vector>

static float value(uint32_t bits) {
  float result;
  std::memcpy(&result, &bits, sizeof(result));
  return result;
}

static uint32_t word(float value) {
  uint32_t result;
  std::memcpy(&result, &value, sizeof(result));
  return result;
}

static void decode(jxl::Bytes bytes, size_t offset = 0) {
  jxl::BitReader reader(bytes);
  reader.SkipBits(offset);
  jxl::ToneMapping tone;
  const bool decoded = jxl::Bundle::Read(&reader, &tone);
  const size_t position = reader.TotalBitsConsumed();
  const bool within = reader.AllReadsWithinBounds();
  const bool closed = reader.Close();
  if (!decoded || !within || !closed) std::cout << "invalid\n";
  else std::cout << "tone " << word(tone.intensity_target) << ' ' << word(tone.min_nits) << ' '
      << tone.relative_to_max_display << ' ' << word(tone.linear_below) << ' ' << position << '\n';
}

int main() {
  std::string command;
  std::vector<uint8_t> input(16, 0);
  size_t input_size = 0;
  while (std::cin >> command) {
    if (command == "encode") {
      uint32_t intensity, minimum, relative, linear;
      if (!(std::cin >> intensity >> minimum >> relative >> linear) || relative > 1) return 1;
      jxl::ToneMapping tone;
      tone.intensity_target = value(intensity);
      tone.min_nits = value(minimum);
      tone.relative_to_max_display = relative;
      tone.linear_below = value(linear);
      size_t extensions = 0, size = 0;
      if (!jxl::Bundle::CanEncode(tone, &extensions, &size)) {
        std::cout << "value\n";
        continue;
      }
      JxlMemoryManager memory;
      if (!jxl::MemoryManagerInit(&memory, nullptr)) return 2;
      jxl::BitWriter writer(&memory);
      if (!jxl::Bundle::Write(tone, &writer, static_cast<jxl::LayerType>(0), nullptr)) return 3;
      if (writer.BitsWritten() != size) return 4;
      writer.ZeroPadToByte();
      const auto bytes = writer.GetSpan();
      std::cout << "encoded " << size << ' ';
      std::array<uint8_t, 80> storage = {};
      if (bytes.size() > 64) return 5;
      for (size_t i = 0; i < bytes.size(); ++i) {
        storage[8 + i] = bytes[i];
        std::cout << std::hex << std::setw(2) << std::setfill('0') << static_cast<uint32_t>(bytes[i]);
      }
      std::cout << std::dec << '\n';
      decode(jxl::Bytes(storage.data() + 8, bytes.size()));
    } else if (command == "input") {
      std::string hex;
      if (!(std::cin >> hex)) return 6;
      if (hex == "-") hex.clear();
      if (hex.size() % 2 || hex.size() > 131072) return 6;
      input_size = hex.size() / 2;
      input.assign(input_size + 16, 0);
      for (size_t i = 0; i < hex.size(); i += 2) {
        const std::string pair = hex.substr(i, 2);
        if (pair.find_first_not_of("0123456789abcdef") != std::string::npos) return 7;
        input[8 + i / 2] = static_cast<uint8_t>(std::stoul(pair, nullptr, 16));
      }
    } else if (command == "read") {
      size_t offset;
      if (!(std::cin >> offset) || offset > input_size * 8) return 6;
      decode(jxl::Bytes(input.data() + 8, input_size), offset);
    } else {
      return 8;
    }
  }
  return std::cin.eof() ? 0 : 9;
}
