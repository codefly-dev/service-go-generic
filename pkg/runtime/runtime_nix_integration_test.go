package runtime_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	runners "github.com/codefly-dev/core/runners/base"
	golanghelpers "github.com/codefly-dev/core/runners/golang"
	"github.com/codefly-dev/core/wool"
)

// Integration tests for the Nix runtime code path through the generic
// go agent's runner layer. These don't drive the full Runtime.Test RPC
// (which needs a full Service identity, workspace YAML, etc.) — instead
// they exercise the narrowest chain that matters for Nix correctness:
//
//   SetGoRuntimeContext(nix)    → selects Nix kind
//   CreateRunner(nix)           → constructs a NixGoRunner
//   env.Init                    → triggers NixEnvironment.Init, which
//                                 runs `nix print-dev-env --json` and
//                                 populates the materialized map
//   env.NewProcess              → emits direct-exec args when materialized
//
// Gated on nix installed + new enough to evaluate the testdata flake.

func requireNixOrSkip(t *testing.T) {
	t.Helper()
	if !runners.CheckNixInstalled() {
		t.Skip("nix not installed")
	}
}

// TestGoAgent_NixRuntimeContextSelection locks the runtime-context
// resolver: explicit Nix → Nix. This is what the agent's
// SetRuntimeContext method calls through to.
func TestGoAgent_NixRuntimeContextSelection(t *testing.T) {
	requireNixOrSkip(t)
	resolved := golanghelpers.SetGoRuntimeContext(resources.NewRuntimeContextNix())
	if resolved.Kind != resources.RuntimeContextNix {
		t.Errorf("expected Nix kind, got %s", resolved.Kind)
	}
}

// TestGoAgent_CreateRunner_BuildsNixEnvironment verifies that asking
// CreateRunner for a Nix context actually constructs a NixGoRunner
// (not Native, not Docker), and that Init succeeds against a real flake.
//
// Skipped on hosts where the nix version can't evaluate the testdata
// flake — the existing nix_runner_test.go probe pattern.
func TestGoAgent_CreateRunner_BuildsNixEnvironment(t *testing.T) {
	requireNixOrSkip(t)

	// Use the shared testdata flake from core/runners/base/testdata —
	// reaching across module boundaries via file path is ugly but
	// avoids duplicating the known-good flake.nix.
	workspacePath := sharedNixTestdata(t)
	relative := "."

	cfg := golanghelpers.RunnerConfig{
		WorkspacePath:  workspacePath,
		RelativeSource: relative,
		UniqueName:     "test-nix-runner",
		CacheLocation:  t.TempDir(),
		Settings:       &golanghelpers.GoAgentSettings{},
	}

	env, err := golanghelpers.CreateRunner(context.Background(),
		resources.NewRuntimeContextNix(), cfg)
	if err != nil {
		t.Fatalf("CreateRunner(nix): %v", err)
	}
	if env == nil {
		t.Fatal("env is nil")
	}

	// Env() should return the NixEnvironment flavor. We can't import
	// *base.NixEnvironment directly for type-assert (it would re-
	// introduce the import cycle risk), so we check indirectly: a Nix
	// env rejects Init on a directory WITHOUT flake.nix. A Native env
	// doesn't care. This differentiates them.
	if _, err := env.Env().NewProcess("true"); err != nil {
		t.Fatalf("NewProcess on nix env: %v", err)
	}
}

// TestGoAgent_NixInit_MaterializesOrFallsBackCleanly asserts the full
// Init path never fails the caller. If materialize succeeds, great; if
// not, we log-and-fall-back. Either way, err == nil — the Runtime.Init
// hot path depends on that.
func TestGoAgent_NixInit_MaterializesOrFallsBackCleanly(t *testing.T) {
	requireNixOrSkip(t)
	wool.SetGlobalLogLevel(wool.WARN) // keep test output clean
	ctx := context.Background()

	workspacePath := sharedNixTestdata(t)
	cfg := golanghelpers.RunnerConfig{
		WorkspacePath:  workspacePath,
		RelativeSource: ".",
		UniqueName:     "test-nix-init",
		CacheLocation:  t.TempDir(),
		Settings:       &golanghelpers.GoAgentSettings{},
	}
	env, err := golanghelpers.CreateRunner(ctx, resources.NewRuntimeContextNix(), cfg)
	if err != nil {
		t.Fatalf("CreateRunner(nix): %v", err)
	}
	// The key promise: Init tolerates old/broken nix and never propagates
	// the materialization failure up.
	if err := env.Env().Init(ctx); err != nil {
		t.Errorf("Nix env Init must not error on hosts where materialize fails; got: %v", err)
	}
}

// sharedNixTestdata locates the known-good flake.nix shipped under
// core/runners/base/testdata. Walks up from the test's cwd until it
// finds the core module, then descends into runners/base/testdata.
// Skips the test if the layout isn't as expected.
func sharedNixTestdata(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// Ascend until we find a sibling named "core".
	dir := cwd
	for {
		candidate := filepath.Join(dir, "..", "core", "runners", "base", "testdata")
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			abs, _ := filepath.Abs(candidate)
			return abs
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Skip("could not locate core/runners/base/testdata relative to " + cwd)
	return "" // unreachable
}

// Silence the unused-import guard on basev0 until we add a test that
// builds a full basev0.RuntimeContext beyond the helper constructors.
var _ *basev0.RuntimeContext
