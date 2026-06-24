#!/usr/bin/env sh
set -eu

go_cmd="${GO:-go}"
testflags="${TESTFLAGS:--count=1 -shuffle=on}"

run_go_test() {
	pkg="$1"
	pattern="$2"
	printf 'generated-output governance: %s %s\n' "$pkg" "$pattern"
	"$go_cmd" test $testflags "$pkg" -run "$pattern"
}

run_go_test ./cmd/gofly/internal/generator 'TestGeneratedFile(SafeTargetValidation|SafeRelativeTargetValidation|RootReadWriteAndCopy|CopyRejectsSymlinkTargets)_BitsUT'
run_go_test ./cmd/gofly/internal/generator 'TestBuildServiceScaffoldIR.*Profile|TestGenerationProfile'
run_go_test ./cmd/gofly/internal/command 'TestExecuteAPINew(WithGoZeroCompatibleProfile|UsesConfigProfileDefault|RejectsUnknownProfile)$'
run_go_test ./cmd/gofly/internal/command 'TestExecuteAPINewAcceptsGoctlReservedFlags|TestIDLGenerateCommandsEmitJSONEnvelope_BitsUT'
run_go_test ./cmd/gofly/internal/generator 'Test(PluginResponseWriteFilesRejectsEscapingPaths|PluginResponseRejectsSymlinkParentTraversal|PluginResponseRejectsSymlinkLeaf|PluginSymlinkParentBoundaries)'
run_go_test ./cmd/gofly/internal/generator 'Test(ApplyTemplateExtensionRejectsSymlinkTemplate|CopyDirRejectsSymlinkSourceEntry)'
run_go_test ./cmd/gofly/internal/generator 'Test(GenerateModelFromDDLGORMStyle|GenerateModelFromDDLGoZeroStyleDoesNotRequireGORM|GenerateModelFromDDLGORMStyleFindsParentGoMod|GenerateMongoModelDriverStyle)$'
run_go_test ./cmd/gofly/internal/command 'Test(AINewGeneratedArtifactsAreDeterministicAndIdempotent|AINewGeneratedProjectVerificationMatrix|NewServiceGeneratedProjectSmokeMatrix)_BitsUT'

printf 'generated-output governance ok\n'
