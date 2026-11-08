#include "lib/jxl/headers.h"
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
  int mode;
  uint64_t argument;
  while (std::cin >> command >> mode >> argument) {
    if (mode < 0 || mode > 2) return 3;
    jxl::SizeHeader dimensions;
    jxl::PreviewHeader preview;
    jxl::AnimationHeader animation;
    jxl::Fields* fields = mode == 0 ? static_cast<jxl::Fields*>(&dimensions)
                                    : mode == 1 ? static_cast<jxl::Fields*>(&preview)
                                                : static_cast<jxl::Fields*>(&animation);
    if (command == "encode") {
      uint64_t b, c, d;
      if (!(std::cin >> b >> c >> d)) return 4;
      if (argument > UINT32_MAX || b > UINT32_MAX || c > UINT32_MAX || d > 1) {
        std::cout << "value\n";
        continue;
      }
      bool valid = true;
      if (mode == 0) valid = dimensions.Set(argument, b);
      else if (mode == 1) valid = preview.Set(argument, b);
      else {
        animation.tps_numerator = argument;
        animation.tps_denominator = b;
        animation.num_loops = c;
        animation.have_timecodes = d != 0;
      }
      Output output;
      if (valid && output.Visit(fields)) output.Print();
      else std::cout << "value\n";
      continue;
    }
    if ((command != "read" && command != "rewrite") || argument > length * 8) return 5;
    jxl::BitReader reader(Span{bytes, length});
    reader.SkipBits(static_cast<size_t>(argument));
    const bool decoded = jxl::Bundle::Read(&reader, fields);
    const size_t position = reader.TotalBitsConsumed();
    const bool within = reader.AllReadsWithinBounds();
    const bool closed = reader.Close();
    if (!decoded || !within || !closed) {
      std::cout << "truncated\n";
      continue;
    }
    if (command == "rewrite") {
      Output output;
      if (output.Visit(fields)) output.Print();
      else std::cout << "value\n";
    } else if (mode < 2) {
      std::cout << "dimensions " << (mode == 0 ? dimensions.xsize() : preview.xsize()) << ' '
                << (mode == 0 ? dimensions.ysize() : preview.ysize()) << ' ' << position << '\n';
    } else {
      std::cout << "animation " << animation.tps_numerator << ' ' << animation.tps_denominator << ' '
                << animation.num_loops << ' ' << animation.have_timecodes << ' ' << position << '\n';
    }
  }
  return std::cin.eof() ? 0 : 6;
}
