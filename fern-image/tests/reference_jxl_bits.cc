// Offline reference only. Production image code has no libjxl dependency.
#include "lib/jxl/dec_bit_reader.h"

#include <cstdint>
#include <fstream>
#include <iostream>
#include <iterator>
#include <vector>

int main(int argc, char** argv) {
  if (argc != 2) return 1;
  std::ifstream file(argv[1], std::ios::binary | std::ios::ate);
  if (!file || file.tellg() < 0 || file.tellg() > 1048576) return 2;
  file.seekg(0);
  std::vector<uint8_t> bytes((std::istreambuf_iterator<char>(file)), {});
  const size_t length = bytes.size();
  if (bytes.empty()) bytes.push_back(0);
  struct Span {
    const std::vector<uint8_t>& bytes;
    size_t length;
    const uint8_t* data() const { return bytes.data(); }
    size_t size() const { return length; }
  };
  int64_t position;
  int width;
  while (std::cin >> position >> width) {
    if (position < 0 || static_cast<uint64_t>(position) > length * 8 ||
        width < 0 || width > 56) return 3;
    jxl::BitReader reader(Span{bytes, length});
    reader.SkipBits(static_cast<size_t>(position));
    const uint64_t value = reader.ReadBits(static_cast<size_t>(width));
    const size_t next = reader.TotalBitsConsumed();
    const bool valid = reader.AllReadsWithinBounds();
    const bool closed = !!reader.Close();
    if (valid && closed) {
      std::cout << "ok " << value << " " << next << "\n";
    } else {
      std::cout << "truncated\n";
    }
  }
  return std::cin.eof() ? 0 : 4;
}
