package dev.fern.dexer;

import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

// Each method prints the results of one kind of bytecode. The JVM output is
// expected.txt, and ART must print the same lines for the DEX file of fern -dex.
public class Main {
    static final int ANSWER = 42;
    static final long BIG = 1L << 40;
    static final float THIRD = 1f / 3f;
    static final double E = 2.718281828459045;
    static final String NAME = "fern";
    static final boolean YES = true;
    static final char LETTER = 'x';
    static final byte SMALL = -7;
    static final short MEDIUM = 1234;
    static int counter;
    static int[] squares = new int[5];

    static {
        for (int i = 0; i < squares.length; i++) {
            squares[i] = i * i;
        }
    }

    interface Shape {
        double area();

        String name();
    }

    static abstract class Base implements Shape {
        protected final String label;

        Base(String label) {
            this.label = label;
        }

        public String name() {
            return label;
        }

        public String toString() {
            return name() + "=" + area();
        }
    }

    static class Square extends Base {
        private final double side;

        Square(double side) {
            super("square");
            this.side = side;
        }

        public double area() {
            return side * side;
        }
    }

    static class Circle extends Base {
        private final double r;

        Circle(double r) {
            super("circle");
            this.r = r;
        }

        public double area() {
            return Math.PI * r * r;
        }

        public String name() {
            return "round " + super.name();
        }
    }

    static class Failure extends Exception {
        final int code;

        Failure(String message, int code) {
            super(message);
            this.code = code;
        }
    }

    static void ints() {
        int a = 17, b = -5;
        System.out.println("int " + (a + b) + " " + (a - b) + " " + (a * b) + " " + (a / b) + " " + (a % b));
        System.out.println("int bits " + (a & b) + " " + (a | b) + " " + (a ^ b) + " " + (~a) + " " + (-a));
        System.out.println("int shifts " + (b << 3) + " " + (b >> 1) + " " + (b >>> 28) + " " + (a << 33));
        System.out.println("int lit " + (a + 1000) + " " + (a * 300) + " " + (a & 0xff) + " " + (a + 100000));
        System.out.println("int overflow " + (Integer.MAX_VALUE + 1) + " " + (Integer.MIN_VALUE / -1));
        int x = 0;
        x += 5;
        x -= 200;
        x++;
        System.out.println("iinc " + x);
        byte by = (byte) 300;
        short sh = (short) 70000;
        char ch = (char) 65601;
        System.out.println("narrow " + by + " " + sh + " " + (int) ch);
    }

    static void longs() {
        long a = 123456789012L, b = -987654321L;
        System.out.println("long " + (a + b) + " " + (a - b) + " " + (a * b) + " " + (a / b) + " " + (a % b));
        System.out.println("long bits " + (a & b) + " " + (a | b) + " " + (a ^ b) + " " + (~a) + " " + (-a));
        System.out.println("long shifts " + (a << 7) + " " + (b >> 3) + " " + (b >>> 60));
        System.out.println("long cmp " + (a > b) + " " + (a == b) + " " + Long.compare(a, b) + " " + BIG);
        int i = (int) a;
        long back = i;
        System.out.println("long conv " + i + " " + back + " " + (float) a + " " + (double) b);
    }

    static void floats() {
        float f = 1.5f, g = -0.25f;
        double d = 3.75, e = 1e-3;
        System.out.println("float " + (f + g) + " " + (f - g) + " " + (f * g) + " " + (f / g) + " " + (f % g) + " " + (-f));
        System.out.println("double " + (d + e) + " " + (d - e) + " " + (d * e) + " " + (d / e) + " " + (d % e) + " " + (-d));
        float nan = Float.NaN;
        double dnan = Double.NaN;
        System.out.println("nan " + (nan < 1f) + " " + (nan > 1f) + " " + (dnan == dnan) + " " + (dnan != dnan));
        System.out.println("conv " + (int) 3.99f + " " + (long) -2.5 + " " + (int) 1e20 + " " + (long) dnan + " " + (float) d + " " + (double) f);
        System.out.println("const " + THIRD + " " + E + " " + 0.0f + " " + 1.0 + " " + 2.0f);
    }

    static void arrays() {
        boolean[] z = {true, false, true};
        byte[] b = {1, -2, 3};
        char[] c = {'f', 'e', 'r', 'n'};
        short[] s = new short[3];
        s[1] = -300;
        long[] l = {1L, BIG, -3L};
        float[] f = new float[2];
        f[0] = 0.5f;
        double[] d = {E, -1.0};
        String[] t = {"a", "b", null};
        int[][] grid = new int[3][4];
        grid[2][3] = 9;
        long[][][] cube = new long[2][3][4];
        cube[1][2][3] = 7L;
        int sum = 0;
        for (int v : squares) {
            sum += v;
        }
        System.out.println("arrays " + z[2] + " " + b[1] + " " + new String(c) + " " + s[1] + " " + l[1] + " " + f[0] + " " + d[0] + " " + t[2] + " " + t.length);
        System.out.println("multi " + grid.length + " " + grid[0].length + " " + grid[2][3] + " " + cube[1][2][3] + " " + cube[1].length + " " + sum);
        Object o = grid;
        System.out.println("instanceof " + (o instanceof int[][]) + " " + (o instanceof Object[]) + " " + (o instanceof long[]));
    }

    static String day(int n) {
        switch (n) {
            case 0: return "sun";
            case 1: return "mon";
            case 2: return "tue";
            case 3: return "wed";
            case 4: return "thu";
            case 5: return "fri";
            case 6: return "sat";
            default: return "none";
        }
    }

    static int sparse(int n) {
        switch (n) {
            case -1000: return 1;
            case 7: return 2;
            case 99999: return 3;
            case Integer.MIN_VALUE: return 4;
            default: return 0;
        }
    }

    static int strings(String s) {
        switch (s) {
            case "alpha": return 1;
            case "beta": return 2;
            default: return -1;
        }
    }

    static void switches() {
        StringBuilder sb = new StringBuilder();
        for (int i = -1; i < 8; i++) {
            sb.append(day(i)).append(' ');
        }
        System.out.println("tableswitch " + sb.toString().trim());
        System.out.println("lookupswitch " + sparse(-1000) + sparse(7) + sparse(99999) + sparse(Integer.MIN_VALUE) + sparse(3));
        System.out.println("string switch " + strings("alpha") + " " + strings("beta") + " " + strings("gamma"));
    }

    static int check(int n) throws Failure {
        if (n < 0) {
            throw new Failure("negative " + n, n);
        }
        return n * 2;
    }

    static String guarded(int n) {
        StringBuilder sb = new StringBuilder();
        try {
            sb.append("try ").append(check(n));
        } catch (Failure f) {
            sb.append("caught ").append(f.getMessage()).append(" code ").append(f.code);
        } finally {
            sb.append(" finally");
        }
        return sb.toString();
    }

    static String nested(Object o) {
        try {
            try {
                String s = (String) o;
                return "length " + s.length();
            } catch (ClassCastException e) {
                return "not a string";
            }
        } catch (NullPointerException e) {
            return "null";
        }
    }

    static void exceptions() {
        System.out.println(guarded(4));
        System.out.println(guarded(-3));
        System.out.println(nested("abc") + ", " + nested(Integer.valueOf(1)) + ", " + nested(null));
        try {
            int[] a = new int[2];
            a[5] = 1;
        } catch (ArrayIndexOutOfBoundsException e) {
            System.out.println("bounds");
        }
        try {
            System.out.println(10 / (counter - counter));
        } catch (ArithmeticException e) {
            System.out.println("divide");
        }
        try {
            throw new IllegalStateException("multi");
        } catch (IllegalArgumentException | IllegalStateException e) {
            System.out.println("multi-catch " + e.getMessage());
        }
    }

    private final Object lock = new Object();
    private int total;

    synchronized void add(int n) {
        total += n;
    }

    int locked(int n) {
        synchronized (lock) {
            if (n > 10) {
                return -1;
            }
            total += n;
        }
        return total;
    }

    static synchronized int bump() {
        return ++counter;
    }

    static void monitors() {
        Main m = new Main();
        m.add(3);
        m.add(4);
        System.out.println("monitors " + m.locked(5) + " " + m.locked(50) + " " + bump() + " " + bump());
    }

    static void objects() {
        List<Shape> shapes = new ArrayList<Shape>();
        shapes.add(new Square(3));
        shapes.add(new Circle(1));
        for (Shape s : shapes) {
            System.out.println(s.name() + " " + s.area() + " " + s);
        }
        Map<String, Integer> counts = new HashMap<String, Integer>();
        for (String w : "a b a c b a".split(" ")) {
            Integer old = counts.get(w);
            counts.put(w, old == null ? 1 : old + 1);
        }
        System.out.println("map " + counts.get("a") + " " + counts.get("b") + " " + counts.get("c") + " " + counts.get("d"));
        Object o = shapes.get(0);
        Base b = (Base) o;
        System.out.println("cast " + b.label + " " + (o instanceof Circle) + " " + (o instanceof Shape));
        System.out.println("constants " + ANSWER + " " + NAME + " " + YES + " " + LETTER + " " + SMALL + " " + MEDIUM);
        Integer boxed = 1000;
        long unboxed = boxed;
        Character c = 'q';
        System.out.println("boxes " + boxed.equals(1000) + " " + unboxed + " " + c + " " + Double.valueOf(0.5).hashCode());
    }

    static long fib(int n) {
        long a = 0, b = 1;
        while (n-- > 0) {
            long t = a + b;
            a = b;
            b = t;
        }
        return a;
    }

    static int many(int a, long b, float c, double d, Object e, int f, long g, char h, boolean i, byte j, short k) {
        return a + (int) b + (int) c + (int) d + (e == null ? 0 : 1) + f + (int) g + h + (i ? 1 : 0) + j + k;
    }

    static void calls() {
        System.out.println("fib " + fib(10) + " " + fib(90));
        System.out.println("many " + many(1, 2L, 3f, 4.0, "x", 6, 7L, 'a', true, (byte) 9, (short) 10));
        String s = null;
        System.out.println("ternary " + (s == null ? "none" : s) + " " + (fib(5) > 3 ? 1 : 2));
        Runnable r = new Runnable() {
            public void run() {
                System.out.println("anonymous " + counter);
            }
        };
        r.run();
    }

    public static void main(String[] args) {
        ints();
        longs();
        floats();
        arrays();
        switches();
        exceptions();
        monitors();
        objects();
        calls();
    }
}
