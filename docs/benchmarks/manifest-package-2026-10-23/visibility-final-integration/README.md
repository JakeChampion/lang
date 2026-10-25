# Package visibility checks

Source: `489278fceebe29602589bddd221b4c8ae260ed2c`.
Dependency: `1a85a71b03e5d800e155d4b43ac4fd54084678cf`.

A package uses the nearest manifest root as its identity.
Without a manifest, the module directory supplies the identity.
A nested manifest starts a separate package, even with the same name.
All modules under the configured standard library root share one package.
Enum constructors and patterns follow the visibility of their enum.
Invalid public re-exports report E087 before name resolution.

The library has 75 package declarations across seven modules.
Their executable code retains the public source bytes after visibility normalization.
The identity record verifies the complete document bytes, with the same chunk labels.
The generated JSON calls retain access to the library helpers.

The bootstrap record identifies the official producer and all stages.
Stages 2 and 3 have identical bytes.
The Go source certificate identifies the earlier test run and its exact unchanged inputs.
Each test log includes every package result.

The three matrices cover 195 commands and 30 balanced allocation counts.
They include both frontends, format round trips, and three target runtimes.
Darwin images are built but are not executed locally.
The installed compiler adds five checks and a complete allocation trace.
The private decimal table probe covers all 619 exponent rows.
The compiler complexity and all 11 driver sizes retain their limits.
