#!/usr/bin/env sh
set -eu

go_cmd="${GO:-go}"
testflags="${TESTFLAGS:--count=1 -shuffle=on}"
tmp_root="$(mktemp -d "${TMPDIR:-/tmp}/gofly-generated-output-go-XXXXXX")"
trap 'rm -rf "$tmp_root"' EXIT
mkdir -p "$tmp_root/gocache" "$tmp_root/gotmp"

run_go_test() {
	pkg="$1"
	pattern="$2"
	printf 'generated-output governance: %s %s\n' "$pkg" "$pattern"
	GOCACHE="${GOCACHE:-$tmp_root/gocache}" GOTMPDIR="${GOTMPDIR:-$tmp_root/gotmp}" "$go_cmd" test $testflags "$pkg" -run "$pattern"
}

run_go_test ./cmd/gofly/internal/generator 'TestGeneratedFile(SafeTargetValidation|SafeRelativeTargetValidation|RootReadWriteAndCopy|CopyRejectsSymlinkTargets)'
run_go_test ./cmd/gofly/internal/generator 'TestBuildServiceScaffoldIR.*Profile|TestGenerationProfile'
run_go_test ./cmd/gofly/internal/command 'TestExecuteAPINew(WithGoZeroCompatibleProfile|UsesConfigProfileDefault|RejectsUnknownProfile)$'
run_go_test ./cmd/gofly/internal/command 'TestExecuteAPINewAcceptsGoctlReservedFlags|TestIDLGenerateCommandsEmitJSONEnvelope'
run_go_test ./cmd/gofly/internal/generator 'Test(PluginResponseWriteFilesRejectsEscapingPaths|PluginResponseRejectsSymlinkParentTraversal|PluginResponseRejectsSymlinkLeaf|PluginSymlinkParentBoundaries)'
run_go_test ./cmd/gofly/internal/generator 'Test(ApplyTemplateExtensionRejectsSymlinkTemplate|CopyDirRejectsSymlinkSourceEntry)'
run_go_test ./cmd/gofly/internal/generator 'Test(GenerateModelFromDDLGORMStyle|GenerateModelFromDDLGoZeroStyleDoesNotRequireGORM|GenerateModelFromDDLGORMStyleFindsParentGoMod|GenerateMongoModelDriverStyle)$'
run_go_test ./cmd/gofly/internal/command 'Test(AINewGeneratedArtifactsAreDeterministicAndIdempotent|AINewGeneratedProjectVerificationMatrix|NewServiceGeneratedProjectSmokeMatrix)'

printf 'generated-output governance ok\n'
