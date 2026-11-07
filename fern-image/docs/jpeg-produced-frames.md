# JPEG frames from block producers

The internal entropy and frame writers accept a synchronous block producer.
The producer receives source data as an argument and supplies complete borrowed blocks to a consumer.
This keeps borrowed views outside function environments and avoids a required complete coefficient array.

The caller declares an exact block count for entropy output.
The frame writer derives that count from its dimensions.
Each call owns private bit state, DC prediction, restart state and output limits.
The first codec error poisons the call, even when the producer ignores consumer errors.
Producer errors remain unchanged when no codec error occurs.

The consumer closes when the producer returns.
An escaped consumer returns a shape error after that point.
The writer removes its active output from shared state before callbacks and removes it when the call closes.
The synchronous protocol does not support asynchronous or concurrent producer callbacks.

`plane_writer.encode` connects this protocol to the component fold.
The caller supplies a borrowed plane, layout, edge policy, quantizers, Huffman tables, restart interval and limits.
The result is a complete one-component baseline JPEG frame.
The implementation does not retain a complete coefficient plane.

Tests compare complete bytes with the array writers and independent references.
They also check exact limits, malformed inputs, cancellation, consumer lifetime and caller aliases.
No public quality, color, alpha or sample-factor default applies.
Full JPEG coverage and measured performance remain open. These checks do not establish a physical memory bound.
