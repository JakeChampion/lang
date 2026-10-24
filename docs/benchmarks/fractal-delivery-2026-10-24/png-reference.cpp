// libpng reference: encode the same RGB8 tiles with fixed speed/filter presets.
#include <png.h>
#include <chrono>
#include <algorithm>
#include <cstdio>
#include <filesystem>
#include <fstream>
#include <iterator>
#include <string>
#include <vector>
static void output(png_structp png, png_bytep data, png_size_t length) {
    auto &bytes = *static_cast<std::vector<unsigned char> *>(png_get_io_ptr(png));
    bytes.insert(bytes.end(), data, data + length);
}
int main(int argc, char **argv) {
    if (argc != 3) return 1;
    const std::string mode = argv[1];
    std::vector<std::filesystem::path> paths;
    for (const auto &entry : std::filesystem::directory_iterator(argv[2]))
        if (entry.path().extension() == ".rgb") paths.push_back(entry.path());
    std::sort(paths.begin(), paths.end());
    for (const auto &path : paths) {
        std::ifstream file(path, std::ios::binary);
        std::vector<unsigned char> pixels(std::istreambuf_iterator<char>(file), {});
        if (pixels.size() != 256 * 256 * 3) return 2;
        png_bytep rows[256];
        for (int y = 0; y < 256; ++y) rows[y] = pixels.data() + y * 256 * 3;
        const auto start = std::chrono::steady_clock::now();
        std::vector<unsigned char> bytes;
        auto png = png_create_write_struct(PNG_LIBPNG_VER_STRING, nullptr, nullptr, nullptr);
        auto info = png_create_info_struct(png);
        if (!png || !info || setjmp(png_jmpbuf(png))) return 3;
        png_set_write_fn(png, &bytes, output, nullptr);
        png_set_IHDR(png, info, 256, 256, 8, PNG_COLOR_TYPE_RGB, PNG_INTERLACE_NONE, PNG_COMPRESSION_TYPE_DEFAULT, PNG_FILTER_TYPE_DEFAULT);
        png_set_compression_level(png, mode.ends_with("fast") ? 1 : 6);
        int filter = PNG_ALL_FILTERS;
        if (mode.starts_with("none")) filter = PNG_FILTER_NONE;
        if (mode.starts_with("sub")) filter = PNG_FILTER_SUB;
        png_set_filter(png, PNG_FILTER_TYPE_BASE, filter);
        png_write_info(png, info);
        png_write_image(png, rows);
        png_write_end(png, info);
        png_destroy_write_struct(&png, &info);
        const auto ns = std::chrono::duration_cast<std::chrono::nanoseconds>(std::chrono::steady_clock::now() - start).count();
        std::printf("%s,%lld,%zu\n", path.stem().c_str(), (long long)ns, bytes.size());
    }
}
