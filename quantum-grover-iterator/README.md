# Quantum Grover Iterator with Phase Estimation

Grover's search algorithm implemented in Q# with Python-driven phase
estimation convergence testing via QIR-based interop.

## Structure

```
quantum-grover-iterator/
├── qsharp.json                 # Q# project metadata
├── pyproject.toml              # Python project config
├── src/
│   ├── grover.qs               # Grover iterator + phase estimation ops
│   └── phase_estimation.qs     # High-level Q# test operations
├── test_phase_estimation.py    # Python convergence tests + plots
└── README.md
```

## Requirements

- Python >= 3.10
- `qsharp >= 1.0` (QIR-based interop)
- `matplotlib`, `numpy`

## Usage

```bash
pip install -e .
python test_phase_estimation.py
```

Produces three plots:

- `convergence.png` — measurement probability vs precision bits
- `probability_distribution.png` — stacked bar showing eigenvalue distributions
- `rz_vs_h_convergence.png` — Rz gate fidelity convergence
