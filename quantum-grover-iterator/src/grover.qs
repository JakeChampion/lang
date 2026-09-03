namespace GroverPhaseEstimation {

    open Microsoft.Quantum.Canon;
    open Microsoft.Quantum.Intrinsic;
    open Microsoft.Quantum.Measurement;
    open Microsoft.Quantum.Math;
    open Microsoft.Quantum.Convert;
    open Microsoft.Quantum.Arrays;

    // =====================================================================
    // Grover iterator components
    // =====================================================================

    /// # Summary
    /// Oracle that flips the phase of the target state |11...1>.
    operation MarkState(qubits : Qubit[]) : Unit is Adj {
        Controlled Z(Most(qubits), Tail(qubits));
    }

    /// # Summary
    /// Diffusion operator: inversion about the mean.
    /// D = 2|s><s| - I  where |s> = H^n|0...0>
    operation DiffusionOperator(qubits : Qubit[]) : Unit is Adj {
        within {
            ApplyToEachA(H, qubits);
            ApplyToEachA(X, qubits);
        } apply {
            Controlled Z(Most(qubits), Tail(qubits));
        }
    }

    /// # Summary
    /// Single Grover iteration: oracle followed by diffusion.
    operation GroverIteration(qubits : Qubit[], oracle : (Qubit[]) => Unit is Adj) : Unit is Adj {
        oracle(qubits);
        DiffusionOperator(qubits);
    }

    /// # Summary
    /// Repeat GroverIteration a given number of times.
    operation GroverSearch(
        qubits : Qubit[],
        oracle : (Qubit[]) => Unit is Adj,
        iterations : Int
    ) : Unit is Adj {
        for _ in 1 .. iterations {
            GroverIteration(qubits, oracle);
        }
    }

    /// # Summary
    /// Oracle that marks the state |00...0>.
    operation MarkZeroState(qubits : Qubit[]) : Unit is Adj {
        within {
            ApplyToEachA(X, qubits);
        } apply {
            Controlled Z(Most(qubits), Tail(qubits));
        }
    }

    // =====================================================================
    // Phase oracles for eigenvalue estimation
    // =====================================================================

    /// # Summary
    /// 2-qubit phase oracle: Rz(theta) on qubit 0, identity on qubit 1.
    /// Eigenvalues: e^{-i*theta/2} (|0>) and e^{+i*theta/2} (|1>),
    /// each with multiplicity 2 (from the identity on qubit 1).
    operation TwoQubitRzPhaseOracle(theta : Double, qubits : Qubit[]) : Unit is Adj {
        Rz(theta, qubits[0]);
    }

    /// # Summary
    /// Controlled version: applies TwoQubitRzPhaseOracle on target
    /// controlled by a single control qubit.
    operation CtrlTwoQubitRz(
        ctrl : Qubit,
        theta : Double,
        targets : Qubit[]
    ) : Unit is Adj {
        Controlled Rz([ctrl], (theta, targets[0]));
    }

    /// # Summary
    /// 2-qubit Hadamard oracle: H on qubit 0, identity on qubit 1.
    /// Eigenvalues: +1 (|+> eigenstate) and -1 (|-> eigenstate),
    /// each with multiplicity 2.
    operation TwoQubitHPhaseOracle(qubits : Qubit[]) : Unit is Adj {
        H(qubits[0]);
    }

    /// # Summary
    /// Controlled Hadamard oracle: H on target controlled by a single control.
    operation CtrlTwoQubitH(ctrl : Qubit, targets : Qubit[]) : Unit is Adj {
        Controlled H([ctrl], targets[0]);
    }

    // =====================================================================
    // Phase estimation
    // =====================================================================

    /// # Summary
    /// Phase estimation for the 2-qubit Rz oracle.
    /// Estimates eigenvalues of Rz(theta) on the first qubit of a 2-qubit register.
    ///
    /// # Input
    /// - `numPrecisionBits` : Number of counting qubits (precision).
    /// - `theta` : Rotation angle for Rz.
    ///
    /// # Output
    /// Tuple (count0, count1) over 100 shots, where count0 counts
    /// measurements interpreting as eigenvalue ~0 and count1 as ~1.
    operation EstimateTwoQubitRz(
        numPrecisionBits : Int,
        theta : Double
    ) : (Int, Int) {
        mutable count0 = 0;
        mutable count1 = 0;

        for _ in 1 .. 100 {
            use counting = Qubit[numPrecisionBits];
            use eigenstate = Qubit[2];

            let n = Length(counting);

            // Initialize eigenstate to uniform superposition (equal weight
            // on |0> and |1> eigenstates of Rz)
            ApplyToEach(H, eigenstate);

            // Hadamard on counting register
            ApplyToEach(H, counting);

            // Controlled U^{2^k} applications
            for k in 0 .. n - 1 {
                let power = 1 <<< k;
                for _ in 1 .. power {
                    CtrlTwoQubitRz(counting[k], 2.0 * theta, eigenstate);
                }
            }

            // Inverse QFT on counting register
            ApplyToEachA(H, counting);
            for i in 0 .. n - 2 {
                let angle = PI() / IntAsDouble(1 <<< (i + 1));
                for j in 0 .. i {
                    Controlled Rz(
                        [counting[i - j]],
                        (-2.0 * angle, counting[j])
                    );
                }
                H(counting[i]);
            }

            let measured = MeasureEachZ(counting);
            mutable allZero = true;
            for r in measured {
                if r == One {
                    set allZero = false;
                }
            }

            if allZero {
                set count0 += 1;
            } else {
                set count1 += 1;
            }

            ResetAll(counting);
            ResetAll(eigenstate);
        }

        return (count0, count1);
    }

    /// # Summary
    /// Phase estimation for the 2-qubit Hadamard oracle.
    /// Estimates eigenvalues of H on the first qubit.
    ///
    /// # Input
    /// - `numPrecisionBits` : Number of counting qubits (precision).
    ///
    /// # Output
    /// Tuple (countPlus, countMinus) over 100 shots, where countPlus
    /// counts measurements of eigenvalue +1 and countMinus eigenvalue -1.
    operation EstimateTwoQubitH(
        numPrecisionBits : Int
    ) : (Int, Int) {
        mutable countPlus = 0;
        mutable countMinus = 0;

        for _ in 1 .. 100 {
            use counting = Qubit[numPrecisionBits];
            use eigenstate = Qubit[2];

            let n = Length(counting);

            // Initialize eigenstate to |00>
            // (we want to estimate eigenvalues of H, not start in superposition)

            // Hadamard on counting register
            ApplyToEach(H, counting);

            // Controlled H^{2^k} applications
            for k in 0 .. n - 1 {
                let power = 1 <<< k;
                for _ in 1 .. power {
                    CtrlTwoQubitH(counting[k], eigenstate);
                }
            }

            // Inverse QFT on counting register
            ApplyToEachA(H, counting);
            for i in 0 .. n - 2 {
                let angle = PI() / IntAsDouble(1 <<< (i + 1));
                for j in 0 .. i {
                    Controlled Rz(
                        [counting[i - j]],
                        (-2.0 * angle, counting[j])
                    );
                }
                H(counting[i]);
            }

            let measured = MeasureEachZ(counting);
            mutable allZero = true;
            for r in measured {
                if r == One {
                    set allZero = false;
                }
            }

            if allZero {
                set countPlus += 1;
            } else {
                set countMinus += 1;
            }

            ResetAll(counting);
            ResetAll(eigenstate);
        }

        return (countPlus, countMinus);
    }
}
