// Code generated for the #4398 part-3 e2e package split; edit freely.
//
// Aliases binding this package's historical helper names to the shared
// harness (internal/e2eharness), so the ~900 test files keep their bare
// identifiers. internal/e2eselfhost carries the same alias file.
package e2e

import (
	e2eharness "github.com/jakechampion/lang/internal/e2eharness"
)

var arm64Tooling = e2eharness.Arm64Tooling
var buildBin = e2eharness.BuildBin
var buildLangBinForInterp = e2eharness.BuildLangBinForInterp
var buildModloadArm64DriverX86 = e2eharness.BuildModloadArm64DriverX86
var buildModloadDriverX86 = e2eharness.BuildModloadDriverX86
var buildSelfHostBin = e2eharness.BuildSelfHostBin
var cachedDriverBin = e2eharness.CachedDriverBin
var cachedLink = e2eharness.CachedLink
var compileAndRunArm64 = e2eharness.CompileAndRunArm64
var compileArm64Bin = e2eharness.CompileArm64Bin
var compileAndRunWasmbinMain = e2eharness.CompileAndRunWasmbinMain
var compileAndRunX86_64 = e2eharness.CompileAndRunX86_64
var compileFilesModload = e2eharness.CompileFilesModload
var compileSourceModload = e2eharness.CompileSourceModload
var compileStdProgModload = e2eharness.CompileStdProgModload
var componentCoreSection = e2eharness.ComponentCoreSection
var contains = e2eharness.Contains
var copySelfHostFiles = e2eharness.CopySelfHostFiles
var eligBits = e2eharness.EligBits
var extractComponentType = e2eharness.ExtractComponentType
var hashSelfHostSources = e2eharness.HashSelfHostSources
var interpExit = e2eharness.InterpExit
var langSrcAbs = e2eharness.LangSrcAbs
var loadCheckMono = e2eharness.LoadCheckMono
var mustWrite = e2eharness.MustWrite
var runArm64Bin = e2eharness.RunArm64Bin
var runBin = e2eharness.RunBin
var runCapture = e2eharness.RunCapture
var runDriverFile = e2eharness.RunDriverFile
var runDriverStdinExits = e2eharness.RunDriverStdinExits
var runFixtureInterp = e2eharness.RunFixtureInterp
var runInterpExit = e2eharness.RunInterpExit
var selfHostImportClosure = e2eharness.SelfHostImportClosure

const uuidV4Program = e2eharness.UuidV4Program

var writeSelfHostAsmProject = e2eharness.WriteSelfHostAsmProject
var writeSelfHostModloadProject = e2eharness.WriteSelfHostModloadProject
var x86_64Tooling = e2eharness.X86_64Tooling
