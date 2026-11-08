#include "lib/jxl/image_metadata.h"
#include "lib/jxl/fields.h"

#include <cstdint>
#include <fstream>
#include <iomanip>
#include <iostream>
#include <iterator>
#include <sstream>
#include <string>
#include <vector>

struct Span {
  const std::vector<uint8_t>& bytes;
  size_t length;
  const uint8_t* data() const { return bytes.data(); }
  size_t size() const { return length; }
};

class Output final : public jxl::fields_internal::VisitorBase {
 public:
  jxl::Status Bits(size_t width, uint32_t, uint32_t* value) override {
    if (width > 32 || static_cast<uint64_t>(*value) >= (1ULL << width)) return false;
    for (size_t i = 0; i < width; ++i) {
      if (bits_ % 8 == 0) bytes_.push_back(0);
      bytes_.back() |= ((*value >> i) & 1) << (bits_ % 8);
      ++bits_;
    }
    return true;
  }
  jxl::Status U32(jxl::U32Enc distribution, uint32_t, uint32_t* value) override {
    uint32_t selector = 0;
    size_t length = 0;
    if (!jxl::U32Coder::ChooseSelector(distribution, *value, &selector, &length)) return false;
    if (!Bits(2, 0, &selector)) return false;
    const auto selected = distribution.GetDistr(selector);
    if (selected.IsDirect()) return true;
    uint32_t payload = *value - selected.Offset();
    return Bits(length - 2, 0, &payload);
  }
  jxl::Status U64(uint64_t, uint64_t*) override { return false; }
  jxl::Status F16(float, float*) override { return false; }
  void Print() const {
    std::ostringstream hex;
    for (uint8_t byte : bytes_) hex << std::hex << std::setw(2) << std::setfill('0') << static_cast<unsigned>(byte);
    std::cout << "encoded " << bits_ << ' ' << hex.str() << '\n';
  }
 private:
  size_t bits_ = 0;
  std::vector<uint8_t> bytes_;
};

int main(int argc, char** argv) {
  if (argc != 2) return 1;
  std::ifstream file(argv[1], std::ios::binary | std::ios::ate);
  if (!file || file.tellg() < 0 || file.tellg() > 65536) return 2;
  file.seekg(0);
  std::vector<uint8_t> bytes((std::istreambuf_iterator<char>(file)), {});
  const size_t length = bytes.size();
  if (bytes.empty()) bytes.push_back(0);
  std::string command;
  uint64_t argument;
  while (std::cin >> command >> argument) {
    jxl::BitDepth depth;
    if (command == "encode") {
      uint64_t bits, exponent;
      if (!(std::cin >> bits >> exponent)) return 3;
      if (argument > 1 || bits > UINT32_MAX || exponent > UINT32_MAX) {
        std::cout << "value\n";
        continue;
      }
      depth.floating_point_sample = argument != 0;
      depth.bits_per_sample = bits;
      depth.exponent_bits_per_sample = exponent;
      Output output;
      if (output.Visit(&depth)) output.Print();
      else std::cout << "value\n";
      continue;
    }
    if ((command != "read" && command != "rewrite") || argument > length * 8) return 4;
    jxl::BitReader reader(Span{bytes, length});
    reader.SkipBits(static_cast<size_t>(argument));
    const bool decoded = jxl::Bundle::Read(&reader, &depth);
    const size_t position = reader.TotalBitsConsumed();
    const bool within = reader.AllReadsWithinBounds();
    const bool closed = reader.Close();
    if (!decoded || !within || !closed) std::cout << "invalid\n";
    else if (command == "rewrite") {
      Output output;
      if (output.Visit(&depth)) output.Print();
      else std::cout << "value\n";
    } else {
      std::cout << "depth " << depth.floating_point_sample << ' ' << depth.bits_per_sample << ' '
                << depth.exponent_bits_per_sample << ' ' << position << '\n';
    }
  }
  return std::cin.eof() ? 0 : 5;
}
