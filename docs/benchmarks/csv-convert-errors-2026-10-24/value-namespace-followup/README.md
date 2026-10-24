# Value namespace and default constants

The source commit is `d457d861824ad9aad5a7a36a61b7de44ec627d13`.
Type parameter qualifiers keep their type bindings.
Bare constant and function values retain their separate namespace.
The two new imported cases cover both free functions and trait default methods.

Issue #13126 affects the published Go oracle.
Its constant fold removed declarations before it visited trait default bodies.
The fold now visits those bodies with the declaration module and parameter scope.
The published self-hosted checker already accepts this case.
Three unit cases check constant substitution, parameter shadowing and local shadowing.

All 2945 Go frontend and constant-fold cases complete without failures.
Seven walker cases intentionally exclude synthetic AST nodes.
The optional whole-corpus formatter type-check sweep remains excluded.
All 26 generic supertrait cases pass with no failures or skips.
Both compilers check and execute the new value-namespace cases.
The imported cases compile for four targets and run on three.

The fresh Go oracle passes all 54 matrix commands.
Go vet and Go format pass.
Compiler and standard library bytes match the earlier native checkpoint.
The native bootstrap, allocation and size evidence stays valid for that exact source hash.
The manifest records the source commits and oracle hash.
