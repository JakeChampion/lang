namespace GroverPhaseEstimation {

    open Microsoft.Quantum.Canon;
    open Microsoft.Quantum.Intrinsic;
    open Microsoft.Quantum.Measurement;
    open Microsoft.Quantum.Math;
    open Microsoft.Quantum.Convert;
    open Microsoft.Quantum.Arrays;

    // =====================================================================
    // Grover search demo
    // =====================================================================

    /// # Summary
    /// Run Grover search and measure the marked state.
    operation RunGroverSearch(numQubits : Int) : Result[] {
        mutable results = [Zero, size = 0];
        use qubits = Qubit[numQubits];

        let optimalIter = Round(PI() / 4.0 * Sqrt(IntAsDouble(1 <<< numQubits)));
        let oracle = MarkState;

        GroverSearch(qubits, oracle, optimalIter);
        set results = MeasureEachZ(qubits);
        ResetAll(qubits);
        return results;
    }
}
