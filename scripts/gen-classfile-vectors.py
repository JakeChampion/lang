#!/usr/bin/env python3
"""Write tests/stdlib/classfile_vectors.fern: the class files of
classfile_test.fern.

javac compiles the Java source below for Java 8, which is the version of
the classes of android.jar. Needs javac. Run it from the root of the
repository.
"""
import subprocess
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / 'tests/stdlib/classfile_vectors.fern'

SOURCES = {
    'dev/fern/Sample.java': '''package dev.fern;

public class Sample extends java.util.AbstractList<String> implements Runnable, Comparable<Sample> {
    public static final int COUNT = -7;
    public static final long BIG = 5000000000L;
    public static final float HALF = 0.5f;
    public static final double THIRD = 0.25;
    public static final String NAME = "fern \\u00e9 \\ud83c\\udf3f \\u0000 end";
    public static final boolean ON = true;
    public static final char LETTER = 'A';
    protected int[] counts;
    private String label;

    public Sample(String label, int size) {
        this.label = label;
        this.counts = new int[size];
    }

    public void run() {
    }

    public int compareTo(Sample other) {
        return 0;
    }

    public String get(int index) {
        return label;
    }

    public int size() {
        return counts.length;
    }

    public static long total(long[] values, double scale, Object... rest) {
        return 0;
    }

    protected native byte[][] rows(short a, char b, byte c, boolean d, float e);
}
''',
    'dev/fern/Shape.java': '''package dev.fern;

public interface Shape {
    int SIDES = 4;

    double area();
}
''',
}


def rows(data):
    out = []
    for i in range(0, len(data), 32):
        out.append('    ' + ', '.join(f'{b}u8' for b in data[i:i + 32]) + ',')
    return '\n'.join(out)


def main():
    parts = ['// The class files of classfile_test.fern. scripts/gen-classfile-vectors.py\n// writes this file. Do not edit it.\n']
    with tempfile.TemporaryDirectory() as tmp:
        src = Path(tmp) / 'src'
        for name, text in SOURCES.items():
            path = src / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(text)
        out = Path(tmp) / 'out'
        subprocess.run(['javac', '--release', '8', '-Xlint:-options', '-d', str(out)] + [str(src / n) for n in SOURCES],
                       check=True, env={'PATH': '/usr/bin:/bin'})
        for fn, comment in [('sample', 'A class with constants, fields, a constructor and methods.'),
                            ('shape', 'An interface with one constant and one method.')]:
            data = (out / 'dev/fern' / (fn.capitalize() + '.class')).read_bytes()
            parts.append(f'// {comment}\npub function {fn}(): u8[] {{\n  return [\n{rows(data)}\n  ];\n}}\n')
    OUT.write_text('\n'.join(parts))


if __name__ == '__main__':
    main()
