#include "lib/jxl/fields.h"

#include <cstdint>
#include <fstream>
#include <iostream>
#include <iterator>
#include <string>
#include <vector>

struct Span {
  const std::vector<uint8_t>& bytes;
  size_t length;
  const uint8_t* data() const { return bytes.data(); }
  size_t size() const { return length; }
};

int main(int argc, char** argv) {
  if (argc != 2) return 1;
  std::ifstream file(argv[1], std::ios::binary | std::ios::ate);
  if (!file || file.tellg() < 0 || file.tellg() > 65536) return 2;
  file.seekg(0);
  std::vector<uint8_t> bytes((std::istreambuf_iterator<char>(file)), {});
  const size_t length = bytes.size();
  if (bytes.empty()) bytes.push_back(0);
  std::string kind;
  uint64_t argument;
  while (std::cin >> kind >> argument) {
    if (kind == "size64") {
      size_t size = 0;
      if (!jxl::U64Coder::CanEncode(argument, &size)) return 3;
      std::cout << "size " << size << '\n';
      continue;
    }
    uint32_t codes[4] = {};
    if (kind == "u32" || kind == "choose32") {
      for (auto& code : codes) {
        int width;
        uint64_t offset;
        if (!(std::cin >> width >> offset) || width < 0 || width > 32 ||
            offset > (width == 0 ? 2147483647ULL : 67108863ULL)) return 4;
        code = width == 0 ? jxl::Val(static_cast<uint32_t>(offset)).d
                          : jxl::BitsOffset(width, static_cast<uint32_t>(offset)).d;
      }
    } else if (kind != "u64") {
      return 5;
    }
    const jxl::U32Enc distribution{jxl::U32Distr(codes[0]), jxl::U32Distr(codes[1]),
                                   jxl::U32Distr(codes[2]), jxl::U32Distr(codes[3])};
    if (kind == "choose32") {
      if (argument > UINT32_MAX) return 6;
      uint32_t selector = 0;
      size_t size = 0;
      if (jxl::U32Coder::ChooseSelector(distribution, static_cast<uint32_t>(argument), &selector, &size)) {
        std::cout << "choice " << selector << ' ' << size << '\n';
      } else {
        std::cout << "value\n";
      }
      continue;
    }
    if (argument > length * 8) return 7;
    jxl::BitReader reader(Span{bytes, length});
    reader.SkipBits(static_cast<size_t>(argument));
    const uint64_t value = kind == "u64" ? jxl::U64Coder::Read(&reader)
                                         : jxl::U32Coder::Read(distribution, &reader);
    const size_t next = reader.TotalBitsConsumed();
    const bool valid = reader.AllReadsWithinBounds();
    const bool closed = reader.Close();
    if (valid && closed) std::cout << "ok " << value << ' ' << next << '\n';
    else std::cout << "truncated\n";
  }
  return std::cin.eof() ? 0 : 8;
}
