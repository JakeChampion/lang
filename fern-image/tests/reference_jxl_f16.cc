#include "lib/jxl/enc_bit_writer.h"
#include "lib/jxl/enc_fields.h"
#include "lib/jxl/fields.h"
#include "lib/jxl/memory_manager_internal.h"

#include <array>
#include <cstdint>
#include <cstring>
#include <iostream>
#include <string>

int main() {
  std::string command;
  uint64_t argument;
  while (std::cin >> command >> argument) {
    if (command == "read" && argument <= 65535) {
      std::array<uint8_t, 16> storage = {};
      storage[8] = static_cast<uint8_t>(argument);
      storage[9] = static_cast<uint8_t>(argument >> 8);
      const jxl::Bytes bytes(storage.data() + 8, 2);
      jxl::BitReader reader(bytes);
      float value = 0;
      const bool decoded = jxl::F16Coder::Read(&reader, &value);
      const size_t position = reader.TotalBitsConsumed();
      const bool within = reader.AllReadsWithinBounds();
      const bool closed = reader.Close();
      if (!decoded || !within || !closed) std::cout << "value\n";
      else {
        uint32_t bits;
        std::memcpy(&bits, &value, sizeof(bits));
        std::cout << "read " << bits << ' ' << position << '\n';
      }
    } else if (command == "encode" && argument <= UINT32_MAX) {
      const uint32_t bits = argument;
      float value;
      std::memcpy(&value, &bits, sizeof(bits));
      size_t size = 0;
      if (!jxl::F16Coder::CanEncode(value, &size)) {
        std::cout << "value\n";
        continue;
      }
      JxlMemoryManager memory;
      if (!jxl::MemoryManagerInit(&memory, nullptr)) return 1;
      jxl::BitWriter writer(&memory);
      const bool encoded = writer.WithMaxBits(16, static_cast<jxl::LayerType>(0), nullptr,
          [&] { return jxl::F16Coder::Write(value, &writer); });
      if (!encoded || size != 16 || writer.BitsWritten() != 16) return 2;
      const auto span = writer.GetSpan();
      if (span.size() != 2) return 3;
      std::cout << "encoded " << (static_cast<uint32_t>(span[0]) | (static_cast<uint32_t>(span[1]) << 8)) << " 16\n";
    } else {
      return 4;
    }
  }
  return std::cin.eof() ? 0 : 5;
}
