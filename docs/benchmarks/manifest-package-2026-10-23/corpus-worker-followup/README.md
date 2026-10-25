# Package checks after the corpus worker update

Source: `edcff9889c447be3ffad71709246c5056d5f4134`.
Public dependency: `b3cc53ec3d92831daf321eda59f07e534bd5abcb`.

The package branch includes the ownership analysis and corpus worker updates.
All 2,453 Go checks pass across four packages without skips.
The fresh Go oracle records the clean source commit.

All 195 matrix commands pass against the new oracle and the prior native image.
All 30 runtime allocation counts balance.
The matrices cover both frontends, format round trips, package boundaries, and public re-exports.
Darwin images are built but are not executed locally.

Every Fern source file, the stage0 pin, and the size limits retain their exact bytes.
The source certificate verifies the compared paths and native image hash.
The earlier bootstrap, native tests, private decimal table, and size gates retain their recorded scope.
Their complete proof is in the adjacent `visibility-final-integration` capsule.
