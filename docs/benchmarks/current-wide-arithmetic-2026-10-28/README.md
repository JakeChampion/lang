# Current wide arithmetic qualification

Source: `a3296b1a5694a7d456bb3d8d526d7bf381a09b8d`.
Compiler build source: `06b5da6e8320a971897df1ada5d1418dc9cbdf0c`.
Both sources contain identical compiler and standard-library files.
The build used the current `9193717cfa8ddce4927761cc5ff5d47faf29b473` bootstrap pin.

PR #13729 merged externally while the current compiler build completed.
Its normal merge parents are `4df99ff7df078bde72844adaaf810c7ff885bab7` and `06b5da6e8320a971897df1ada5d1418dc9cbdf0c`.
The earlier `5383d709b477ff52737c8915b93a54e4790699d2` evidence remains a separate historical campaign.
The seven arithmetic files retain their original bytes and exact integer criterion.

The build completed with exit zero, a version check, and the standard smoke checks.
The fresh Go oracle used source `a3296b1a5694a7d456bb3d8d526d7bf381a09b8d`.
All three direct workers completed all 4474 reference rows.
Their allocation and free counts matched, with zero live bytes.

The first auxiliary check found a proof packaging defect.
Three raw diagnostic files had Go source suffixes outside their original harness package.
Go package discovery treated the artifact directory as executable source and reported undefined harness symbols.
The gzip archives preserve each original byte and digest without a package validation exclusion.

The first source check did not write its runtime executable because the new checkout lacked the ignored output directory.
A parallel harness later created that directory.
The resumed source check uses a distinct record.
The original failed record remains intact.

The arithmetic helpers do not establish retained-machine recovery.
No decoder or machine integration accompanies this evidence.
All actual M5 and epic gates remain open.
Current CI results remain separate from local qualification.

The stock arithmetic gate passed four Go test nodes with zero failures and zero skips.
The current four controlled defects each compiled and then failed with the required reason.
The final control corrupts only the expected value of reference row 4473.
These intentional failures do not establish a persistent compiler leak.

The corrected archive tree passed Go build, vet, the 219-node source audit, and the remaining auxiliary checks.
The separate compiler, standard-library, and driver checks passed.
The complete source gate remains failed because its feature census counts 5202 manual increments against the unchanged 5200 ceiling.
Issue #13733 tracks the repair without a threshold increase.
