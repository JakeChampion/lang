#!/usr/bin/env python3
"""Write tests/stdlib/classfile_vectors.fern: the class files of
classfile_test.fern. Also write tools/jnibind/testdata/sample.jar: the same
class files in a JAR, for the test of tools/jnibind.

javac compiles the Java source below for Java 8, which is the version of
the classes of android.jar. Needs javac. Run it from the root of the
repository.
"""
import io
import subprocess
import tempfile
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / 'tests/stdlib/classfile_vectors.fern'
JAR = ROOT / 'tools/jnibind/testdata/sample.jar'

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
    'dev/fern/Point.java': '''package dev.fern;

public class Point implements Shape {
    public static final String ORIGIN = "0,0";
    public static long made;
    public int x;
    public String label;
    public Shape outline;
    public double[] weights;

    public Point() {
    }

    public Point(int x, String label) {
        this.x = x;
        this.label = label;
    }

    public double area() {
        return 0;
    }

    public void set(int x) {
        this.x = x;
    }

    public void set(String label, long stamp) {
        this.label = label;
    }

    public boolean match(Point other, float slack) {
        return true;
    }

    public byte low() {
        return (byte) x;
    }

    public short middle() {
        return (short) x;
    }

    public char letter() {
        return 'p';
    }

    public float half() {
        return 0.5f;
    }

    public Shape[] parts(Object key) {
        return null;
    }

    public void release() {
    }

    public static Point parse(String text) throws Exception {
        return new Point();
    }
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
                            ('point', 'A class with public fields, overloaded methods and each kind of result.'),
                            ('shape', 'An interface with one constant and one method.')]:
            data = (out / 'dev/fern' / (fn.capitalize() + '.class')).read_bytes()
            parts.append(f'// {comment}\npub function {fn}(): u8[] {{\n  return [\n{rows(data)}\n  ];\n}}\n')
        jar = io.BytesIO()
        with zipfile.ZipFile(jar, 'w', zipfile.ZIP_DEFLATED) as z:
            for name in sorted(n[:-5] + '.class' for n in SOURCES):
                z.writestr(zipfile.ZipInfo(name, (1980, 1, 1, 0, 0, 0)), (out / name).read_bytes(), zipfile.ZIP_DEFLATED)
        JAR.write_bytes(jar.getvalue())
    OUT.write_text('\n'.join(parts))


if __name__ == '__main__':
    main()
