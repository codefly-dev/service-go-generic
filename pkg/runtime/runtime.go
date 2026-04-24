// Package runtime implements the generic Go Runtime gRPC service.
// Specializations embed *Runtime (Go struct embedding) to inherit the
// full lifecycle and override only what their layer adds — typically
// Load (additional endpoint wiring) and Init (REST/gRPC env vars).
// Test, Lint, Build are reused as-is.
package runtime

import (
	"context"
	"os"
	"path"

	"github.com/codefly-dev/core/agents/services"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	runners "github.com/codefly-dev/core/runners/base"
	"github.com/codefly-dev/core/wool"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
	golanghelpers "github.com/codefly-dev/core/runners/golang"

	goservice "github.com/codefly-dev/service-go/pkg/service"
)

// RuntimeImage is the default runtime Docker image. Specializations can
// override by reassigning before Init if their layer needs a different base.
var RuntimeImage = &resources.DockerImage{Name: "codeflydev/go", Tag: "0.0.10"}

// Runtime is the generic Go runtime server. Embedded by specializations
// (go-grpc, …) to inherit the services.Base chain via *goservice.Service
// and the full lifecycle methods.
type Runtime struct {
	services.RuntimeServer
	*goservice.Service

	// RunnerEnvironment is exported so specializations can reach it for
	// extra env wiring or port bindings. Nil before Init.
	RunnerEnvironment *golanghelpers.GoRunnerEnvironment

	cacheLocation string
	runner        runners.Proc
	testProc      runners.Proc
}

// New builds a generic Go Runtime bound to the shared Service.
func New(svc *goservice.Service) *Runtime {
	return &Runtime{Service: svc}
}

func (s *Runtime) Load(ctx context.Context, req *runtimev0.LoadRequest) (*runtimev0.LoadResponse, error) {
	err := s.Base.Load(ctx, req.Identity, s.Settings)
	if err != nil {
		return s.Runtime.LoadErrorf(err, "loading base")
	}

	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)

	if req.DisableCatch {
		s.Wool.DisableCatch()
	}

	s.Runtime.SetEnvironment(req.Environment)

	// Prefer configured source dir (default: code/).
	// Fall back to service root if source dir has no go.mod (arbitrary Go project).
	s.Service.SourceLocation, err = s.LocalDirCreate(ctx, "%s", s.Settings.GoSourceDir())
	if err != nil {
		return s.Runtime.LoadErrorf(err, "creating source location")
	}
	if _, statErr := os.Stat(path.Join(s.Service.SourceLocation, "go.mod")); statErr != nil {
		if _, rootErr := os.Stat(path.Join(s.Location, "go.mod")); rootErr == nil {
			s.Service.SourceLocation = s.Location
		}
	}

	s.cacheLocation, err = s.LocalDirCreate(ctx, ".cache")
	if err != nil {
		return s.Runtime.LoadErrorf(err, "creating cache location")
	}

	// Optional: load endpoints if service has any (e.g. HTTP health). No gRPC required.
	s.Endpoints, _ = s.Base.Service.LoadEndpoints(ctx)
	// Leave GrpcEndpoint/RestEndpoint unset — go has no gRPC

	return s.Runtime.LoadResponse()
}

func (s *Runtime) SetRuntimeContext(_ context.Context, runtimeContext *basev0.RuntimeContext) error {
	s.Runtime.RuntimeContext = golanghelpers.SetGoRuntimeContext(runtimeContext)
	return nil
}

func (s *Runtime) CreateRunnerEnvironment(ctx context.Context) error {
	s.Wool.Trace("creating runner environment", wool.DirField(s.Identity.WorkspacePath))

	cfg := golanghelpers.RunnerConfig{
		RuntimeImage:   RuntimeImage,
		WorkspacePath:  s.Identity.WorkspacePath,
		RelativeSource: s.Identity.RelativeToWorkspace,
		UniqueName:     s.UniqueWithWorkspace(),
		CacheLocation:  s.cacheLocation,
		Settings: &golanghelpers.GoAgentSettings{
			HotReload:                 s.Settings.HotReload,
			DebugSymbols:              s.Settings.DebugSymbols,
			RaceConditionDetectionRun: s.Settings.RaceConditionDetectionRun,
			WithCGO:                   s.Settings.WithCGO,
			WithWorkspace:             s.Settings.WithWorkspace,
			SourceDir:                 s.Settings.SourceDir,
		},
	}

	env, err := golanghelpers.CreateRunner(ctx, s.Runtime.RuntimeContext, cfg)
	if err != nil {
		return err
	}

	allEnvs, err := s.EnvironmentVariables.All()
	if err != nil {
		return s.Wool.Wrapf(err, "cannot get environment variables")
	}
	env.WithEnvironmentVariables(ctx, allEnvs...)

	s.RunnerEnvironment = env
	// Expose the underlying RunnerEnvironment on the shared Service so
	// Code / Tooling / commands route spawns through the same mode —
	// without reaching into the Go-specific wrapper.
	s.Service.ActiveEnv = env.Env()
	return nil
}

func (s *Runtime) Init(ctx context.Context, req *runtimev0.InitRequest) (*runtimev0.InitResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)

	s.Runtime.LogInitRequest(req)

	err := s.SetRuntimeContext(ctx, req.RuntimeContext)
	if err != nil {
		return s.Runtime.InitErrorf(err, "cannot set runtime context")
	}

	s.Wool.Forwardf("starting execution environment in %s mode", s.Runtime.RuntimeContext.Kind)
	s.EnvironmentVariables.SetRuntimeContext(s.Runtime.RuntimeContext)
	s.NetworkMappings = req.ProposedNetworkMappings

	err = s.EnvironmentVariables.AddConfigurations(ctx, req.WorkspaceConfigurations...)
	if err != nil {
		return s.Runtime.InitError(err)
	}

	confs := resources.FilterConfigurations(req.DependenciesConfigurations, s.Runtime.RuntimeContext)
	s.Wool.Trace("adding configurations", wool.Field("configurations", resources.MakeManyConfigurationSummary(confs)))
	err = s.EnvironmentVariables.AddConfigurations(ctx, confs...)
	if err != nil {
		return s.Runtime.InitError(err)
	}

	// No endpoint env vars — go has no gRPC/REST

	if s.RunnerEnvironment == nil {
		err = s.CreateRunnerEnvironment(ctx)
		if err != nil {
			return s.Runtime.InitErrorf(err, "cannot create runner environment")
		}
	}

	err = s.RunnerEnvironment.Init(ctx)
	if err != nil {
		s.Wool.Error("cannot init the go runner", wool.ErrField(err))
		return s.Runtime.InitError(err)
	}

	s.Wool.Trace("runner init done")
	return s.Runtime.InitResponse()
}

func (s *Runtime) Start(ctx context.Context, req *runtimev0.StartRequest) (*runtimev0.StartResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)

	s.Wool.Info("Building go binary")

	if s.runner != nil {
		if err := s.runner.Stop(ctx); err != nil {
			return s.Runtime.StartError(err)
		}
	}

	err := s.RunnerEnvironment.BuildBinary(ctx)
	if err != nil {
		if !s.Settings.HotReload {
			return s.Runtime.StartError(err)
		}
		s.Wool.Info("compile error, waiting for hot-reload")
		return s.Runtime.StartResponse()
	}

	runningContext := s.Wool.Inject(context.Background())
	err = s.EnvironmentVariables.AddEndpoints(ctx, req.DependenciesNetworkMappings, resources.NetworkAccessFromRuntimeContext(s.Runtime.RuntimeContext))
	if err != nil {
		return s.Runtime.StartError(err)
	}
	s.EnvironmentVariables.SetFixture(req.Fixture)

	proc, err := s.RunnerEnvironment.Runner()
	if err != nil {
		return s.Runtime.StartErrorf(err, "getting runner")
	}
	startEnvs, err := s.EnvironmentVariables.All()
	if err != nil {
		return s.Runtime.StartErrorf(err, "getting environment variables")
	}
	proc.WithEnvironmentVariables(ctx, startEnvs...)

	s.runner = proc
	err = s.runner.Start(runningContext)
	if err != nil {
		return s.Runtime.StartErrorf(err, "starting runner")
	}
	s.Wool.Trace("runner started successfully")
	return s.Runtime.StartResponse()
}

func (s *Runtime) Build(ctx context.Context, req *runtimev0.BuildRequest) (*runtimev0.BuildResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)

	s.Infof("running go build")

	envs, err := s.EnvironmentVariables.All()
	if err != nil {
		return s.Runtime.BuildErrorf(err, "getting environment variables")
	}

	opts := golanghelpers.BuildOptions{Target: req.Target}
	output, runErr := golanghelpers.RunGoBuild(ctx, s.RunnerEnvironment, s.Service.SourceLocation, envs, opts)
	if runErr != nil {
		return s.Runtime.BuildErrorf(runErr, "build failed")
	}
	return s.Runtime.BuildResponse(output)
}

func (s *Runtime) Test(ctx context.Context, req *runtimev0.TestRequest) (*runtimev0.TestResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)

	s.Infof("running go tests")

	testEnvs, err := s.EnvironmentVariables.All()
	if err != nil {
		return s.Runtime.TestErrorf(err, "getting environment variables")
	}

	opts := golanghelpers.TestOptions{
		Target:   req.Target,
		Verbose:  req.Verbose,
		Race:     req.Race,
		Timeout:  req.Timeout,
		Coverage: req.Coverage,
		// Stream per-test events through the logger so the CLI TUI can
		// show real-time progress instead of waiting for the summary.
		OnEvent: func(ev golanghelpers.TestEvent) {
			switch ev.Action {
			case "run":
				if ev.Test != "" {
					s.Wool.Forwardf("RUN  %s", ev.Test)
				}
			case "pass":
				if ev.Test != "" {
					s.Wool.Forwardf("PASS %s (%.2fs)", ev.Test, ev.Elapsed)
				}
			case "fail":
				if ev.Test != "" {
					s.Wool.Forwardf("FAIL %s (%.2fs)", ev.Test, ev.Elapsed)
				}
			case "skip":
				if ev.Test != "" {
					s.Wool.Forwardf("SKIP %s", ev.Test)
				}
			}
		},
	}
	summary, runErr := golanghelpers.RunGoTests(ctx, s.RunnerEnvironment, s.Service.SourceLocation, testEnvs, opts)

	s.Wool.Forwardf("Tests: %s", summary.SummaryLine())
	for _, f := range summary.Failures {
		s.Wool.Forwardf("%s", f)
	}

	return s.Runtime.TestResponseWithResults(summary.Run, summary.Passed, summary.Failed, summary.Skipped, summary.Coverage, summary.Failures, runErr)
}

func (s *Runtime) Lint(ctx context.Context, req *runtimev0.LintRequest) (*runtimev0.LintResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)

	s.Infof("running go vet")

	envs, err := s.EnvironmentVariables.All()
	if err != nil {
		return s.Runtime.LintErrorf(err, "getting environment variables")
	}

	opts := golanghelpers.LintOptions{Target: req.Target}
	output, runErr := golanghelpers.RunGoLint(ctx, s.RunnerEnvironment, s.Service.SourceLocation, envs, opts)
	if runErr != nil {
		return s.Runtime.LintErrorf(runErr, "lint failed")
	}
	return s.Runtime.LintResponse(output)
}

func (s *Runtime) Information(ctx context.Context, req *runtimev0.InformationRequest) (*runtimev0.InformationResponse, error) {
	return s.Runtime.InformationResponse(ctx, req)
}

func (s *Runtime) Stop(ctx context.Context, req *runtimev0.StopRequest) (*runtimev0.StopResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)
	s.Wool.Trace("stopping service")
	if s.testProc != nil {
		_ = s.testProc.Stop(ctx)
		s.testProc = nil
	}
	if s.runner != nil {
		if err := s.runner.Stop(ctx); err != nil {
			return s.Runtime.StopError(err)
		}
	}
	// Stop the file watcher to prevent CPU spin on orphaned processes
	if s.Watcher != nil {
		s.Watcher.Pause()
	}
	if s.Events != nil {
		close(s.Events)
		s.Events = nil
	}
	return s.Runtime.StopResponse()
}

func (s *Runtime) Destroy(ctx context.Context, req *runtimev0.DestroyRequest) (*runtimev0.DestroyResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)

	s.Wool.Trace("destroying service")
	err := golanghelpers.DestroyGoRuntime(ctx, s.Runtime.RuntimeContext, RuntimeImage,
		s.cacheLocation, s.Identity.WorkspacePath,
		path.Join(s.Identity.RelativeToWorkspace, s.Settings.GoSourceDir()),
		s.UniqueWithWorkspace())
	if err != nil {
		return s.Runtime.DestroyError(err)
	}
	return s.Runtime.DestroyResponse()
}
