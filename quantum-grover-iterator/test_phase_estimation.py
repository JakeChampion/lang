"""
Phase estimation convergence test using Q#/Python QIR interop.

Tests:
1. Convergence of phase estimation with 1-10 precision bits (2-qubit Rz)
2. Comparison of Rz vs H gate eigenvalue convergence
3. Measurement probability distribution
"""

import qsharp
import numpy as np
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
from pathlib import Path


def init_qsharp():
    """Initialize Q# with the source directory containing .qs files."""
    qsharp.init(project_root=str(Path(__file__).parent))


def run_rz_estimation(num_precision_bits: int, theta: float = np.pi / 3) -> dict:
    """
    Run phase estimation for the 2-qubit Rz oracle.

    Returns counts of eigenvalue measurements over 100 shots.
    """
    result = qsharp.eval(
        f'GroverPhaseEstimation.EstimateTwoQubitRz({num_precision_bits}, {theta})'
    )
    return {"count0": result[0], "count1": result[1]}


def run_h_estimation(num_precision_bits: int) -> dict:
    """
    Run phase estimation for the 2-qubit Hadamard oracle.

    Returns counts of eigenvalue measurements over 100 shots.
    """
    result = qsharp.eval(
        f'GroverPhaseEstimation.EstimateTwoQubitH({num_precision_bits})'
    )
    return {"countPlus": result[0], "countMinus": result[1]}


def test_convergence_range():
    """
    Test phase estimation convergence from 1 to 10 precision bits.
    Uses the 2-qubit Rz oracle with theta = pi/3.
    """
    theta = np.pi / 3
    bit_range = range(1, 11)
    counts = []

    print("=== Phase Estimation Convergence (2-qubit Rz, theta = pi/3) ===")
    print(f"{'Bits':>4} | {'p(0)':>6} | {'p(1)':>6} | {'Fidelity':>8}")
    print("-" * 40)

    for n in bit_range:
        result = run_rz_estimation(n, theta)
        total = result["count0"] + result["count1"]
        p0 = result["count0"] / total if total > 0 else 0
        p1 = result["count1"] / total if total > 0 else 0
        fidelity = max(p0, p1)
        counts.append(result)
        print(f"{n:>4} | {p0:>6.2f} | {p1:>6.2f} | {fidelity:>8.2%}")

    return counts


def test_rz_vs_h_convergence():
    """
    Compare convergence of Rz(pi/3) vs H gate eigenvalues.

    Rz(pi/3): eigenvalues e^{-i*pi/6} and e^{+i*pi/6}
    H gate:   eigenvalues +1 and -1
    """
    bits_range = range(1, 11)

    print("\n=== Rz vs H Gate Eigenvalue Convergence ===")
    print(
        f"{'Bits':>4} | {'Rz p(0)':>8} | {'Rz p(1)':>8}"
        f" | {'H p(+)':>8} | {'H p(-)':>8}"
    )
    print("-" * 56)

    rz_counts = []
    h_counts = []

    theta = np.pi / 3

    for n in bits_range:
        # Rz test
        rz_result = run_rz_estimation(n, theta)
        rz_total = rz_result["count0"] + rz_result["count1"]
        rz_p0 = rz_result["count0"] / rz_total if rz_total > 0 else 0
        rz_p1 = rz_result["count1"] / rz_total if rz_total > 0 else 0
        rz_counts.append((rz_p0, rz_p1))

        # H gate test
        h_result = run_h_estimation(n)
        h_total = h_result["countPlus"] + h_result["countMinus"]
        h_p_plus = h_result["countPlus"] / h_total if h_total > 0 else 0
        h_p_minus = h_result["countMinus"] / h_total if h_total > 0 else 0
        h_counts.append((h_p_plus, h_p_minus))

        print(
            f"{n:>4} | {rz_p0:>8.2f} | {rz_p1:>8.2f}"
            f" | {h_p_plus:>8.2f} | {h_p_minus:>8.2f}"
        )

    return rz_counts, h_counts


def plot_convergence(counts: list, save_path: str = "convergence.png"):
    """Plot convergence of phase estimation measurement probabilities."""
    fig, axes = plt.subplots(1, 2, figsize=(14, 5))

    bits = range(1, len(counts) + 1)
    p0_vals = [c["count0"] / (c["count0"] + c["count1"]) for c in counts]
    p1_vals = [c["count1"] / (c["count0"] + c["count1"]) for c in counts]

    # Subplot 1: measurement probability distribution
    ax1 = axes[0]
    ax1.bar(
        [b - 0.15 for b in bits],
        p0_vals,
        width=0.3,
        label="p(eigenvalue ~ 0)",
        color="#2196F3",
        alpha=0.8,
    )
    ax1.bar(
        [b + 0.15 for b in bits],
        p1_vals,
        width=0.3,
        label="p(eigenvalue ~ 1)",
        color="#FF9800",
        alpha=0.8,
    )
    ax1.set_xlabel("Precision bits")
    ax1.set_ylabel("Measurement probability")
    ax1.set_title("Phase Estimation: Measurement Distribution")
    ax1.set_xticks(list(bits))
    ax1.set_ylim(0, 1.1)
    ax1.legend()
    ax1.grid(True, alpha=0.3)

    # Subplot 2: fidelity vs precision
    ax2 = axes[1]
    fidelities = [
        max(c["count0"], c["count1"]) / (c["count0"] + c["count1"]) for c in counts
    ]
    ax2.plot(
        list(bits),
        fidelities,
        "o-",
        color="#4CAF50",
        linewidth=2,
        markersize=6,
        label="Phase estimation fidelity",
    )
    ax2.axhline(y=1.0, color="gray", linestyle="--", alpha=0.5, label="Perfect fidelity")
    ax2.set_xlabel("Precision bits")
    ax2.set_ylabel("Fidelity")
    ax2.set_title("Fidelity vs Precision Bits")
    ax2.set_xticks(list(bits))
    ax2.set_ylim(0.4, 1.05)
    ax2.legend()
    ax2.grid(True, alpha=0.3)

    plt.tight_layout()
    plt.savefig(save_path, dpi=150, bbox_inches="tight")
    plt.close()
    print(f"\nSaved convergence plot to {save_path}")


def plot_probability_distribution(
    counts: list, save_path: str = "probability_distribution.png"
):
    """Plot stacked probability distribution of eigenvalue measurements."""
    fig, ax = plt.subplots(figsize=(10, 6))

    bits = range(1, len(counts) + 1)
    p0_vals = [c["count0"] / (c["count0"] + c["count1"]) for c in counts]
    p1_vals = [c["count1"] / (c["count0"] + c["count1"]) for c in counts]

    ax.bar(list(bits), p0_vals, label="p(eigenvalue ~ 0)", color="#2196F3")
    ax.bar(
        list(bits),
        p1_vals,
        bottom=p0_vals,
        label="p(eigenvalue ~ 1)",
        color="#FF9800",
    )

    ax.set_xlabel("Number of precision bits")
    ax.set_ylabel("Probability")
    ax.set_title(
        "Phase Estimation Probability Distribution\n(2-qubit Rz oracle, theta = pi/3)"
    )
    ax.set_xticks(list(bits))
    ax.set_ylim(0, 1.1)
    ax.legend(loc="upper right")
    ax.grid(True, alpha=0.3, axis="y")

    # Annotate transition region
    for b, p0 in zip(bits, p0_vals):
        if abs(p0 - 0.5) < 0.1:
            ax.annotate(
                "transition", xy=(b, 0.5), fontsize=8, ha="center", va="bottom", color="red"
            )

    plt.tight_layout()
    plt.savefig(save_path, dpi=150, bbox_inches="tight")
    plt.close()
    print(f"Saved probability distribution plot to {save_path}")


def plot_rz_vs_h(
    rz_counts: list,
    h_counts: list,
    save_path: str = "rz_vs_h_convergence.png",
):
    """Plot Rz vs H gate fidelity convergence comparison."""
    fig, axes = plt.subplots(1, 2, figsize=(14, 5))

    bits = list(range(1, len(rz_counts) + 1))

    # Left: fidelity comparison
    ax1 = axes[0]
    rz_fidelity = [max(p0, p1) for p0, p1 in rz_counts]
    h_fidelity = [max(p0, p1) for p0, p1 in h_counts]

    ax1.plot(
        bits, rz_fidelity, "o-", color="#2196F3", linewidth=2, markersize=6,
        label="Rz(pi/3) fidelity",
    )
    ax1.plot(
        bits, h_fidelity, "s-", color="#FF9800", linewidth=2, markersize=6,
        label="H gate fidelity",
    )
    ax1.axhline(y=1.0, color="gray", linestyle="--", alpha=0.5)
    ax1.set_xlabel("Precision bits")
    ax1.set_ylabel("Fidelity")
    ax1.set_title("Rz vs H Gate: Fidelity Convergence")
    ax1.set_xticks(bits)
    ax1.set_ylim(0.4, 1.05)
    ax1.legend()
    ax1.grid(True, alpha=0.3)

    # Right: probability split comparison
    ax2 = axes[1]
    rz_p0 = [p0 for p0, _ in rz_counts]
    rz_p1 = [p1 for _, p1 in rz_counts]
    h_p0 = [p0 for p0, _ in h_counts]
    h_p1 = [p1 for _, p1 in h_counts]

    ax2.plot(
        bits, rz_p0, "o--", color="#2196F3", alpha=0.6, label="Rz p(0)",
    )
    ax2.plot(
        bits, rz_p1, "o-", color="#2196F3", linewidth=2, label="Rz p(1)",
    )
    ax2.plot(
        bits, h_p0, "s--", color="#FF9800", alpha=0.6, label="H p(+)",
    )
    ax2.plot(
        bits, h_p1, "s-", color="#FF9800", linewidth=2, label="H p(-)",
    )
    ax2.set_xlabel("Precision bits")
    ax2.set_ylabel("Measurement probability")
    ax2.set_title("Rz vs H Gate: Probability Split")
    ax2.set_xticks(bits)
    ax2.set_ylim(0, 1.05)
    ax2.legend()
    ax2.grid(True, alpha=0.3)

    plt.tight_layout()
    plt.savefig(save_path, dpi=150, bbox_inches="tight")
    plt.close()
    print(f"Saved Rz vs H comparison plot to {save_path}")


def main():
    """Run all phase estimation convergence tests."""
    print("Initializing Q# runtime...")
    init_qsharp()

    # --- Test 1: Convergence from 1-10 precision bits ---
    print("\n" + "=" * 60)
    print("TEST 1: Phase Estimation Convergence (2-qubit Rz)")
    print("=" * 60)
    counts = test_convergence_range()

    # --- Test 2: Rz vs H gate comparison ---
    print("\n" + "=" * 60)
    print("TEST 2: Rz vs H Gate Eigenvalue Convergence")
    print("=" * 60)
    rz_counts, h_counts = test_rz_vs_h_convergence()

    # --- Plot results ---
    print("\n" + "=" * 60)
    print("GENERATING PLOTS")
    print("=" * 60)

    plot_convergence(counts, save_path="convergence.png")
    plot_probability_distribution(counts, save_path="probability_distribution.png")
    plot_rz_vs_h(rz_counts, h_counts, save_path="rz_vs_h_convergence.png")

    # --- Summary ---
    print("\n" + "=" * 60)
    print("SUMMARY")
    print("=" * 60)
    best = max(
        max(c["count0"], c["count1"]) / (c["count0"] + c["count1"]) for c in counts
    )
    print(f"  Convergence test: {len(counts)} precision levels tested")
    print(f"  Best fidelity:    {best:.2%}")
    print(
        "  Plots saved: convergence.png, probability_distribution.png,"
        " rz_vs_h_convergence.png"
    )
    print("=" * 60)


if __name__ == "__main__":
    main()
