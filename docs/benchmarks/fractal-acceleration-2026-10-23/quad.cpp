// Independent 113-bit scalar escape oracle. GCC supplies __float128 arithmetic.
// Input: real imaginary limit. Output: first escape count, or -1 at the limit.
#include <iostream>
int main() {
    double real, imaginary;
    int limit;
    while (std::cin >> real >> imaginary >> limit) {
        __float128 zr = 0, zi = 0;
        const __float128 cr = real, ci = imaginary;
        int result = -1;
        for (int n = 0; n < limit; ++n) {
            const __float128 rr = zr * zr, ii = zi * zi;
            if (rr + ii > 65536) { result = n; break; }
            zi = (zr + zr) * zi + ci;
            zr = rr - ii + cr;
        }
        std::cout << result << '\n';
    }
}
