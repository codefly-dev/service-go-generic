// Package builder implements the generic Go Builder gRPC service.
// Specializations embed *Builder to inherit Load / Init / Sync / Create /
// Build / Deploy. Because //go:embed cannot reach outside the .go file's
// directory, the caller (binary main.go) provides the three template FS
// trees (factory, builder, deployment) at construction time.
package builder

import (
	"context"
	"embed"

	"github.com/codefly-dev/core/agents/communicate"
	"github.com/codefly-dev/core/agents/services"
	"github.com/codefly-dev/core/builders"
	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	golanghelpers "github.com/codefly-dev/core/runners/golang"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/templates"

	goservice "github.com/codefly-dev/service-go/pkg/service"
)

// Setting names for communicate prompts.
const (
	HotReload                 = golanghelpers.SettingHotReload
	DebugSymbols              = golanghelpers.SettingDebugSymbols
	RaceConditionDetectionRun = golanghelpers.SettingRaceConditionDetectionRun
)

// BuildConfig provides the embedded template trees plus the file
// requirements descriptor. Specializations construct this struct with
// their own //go:embed directives in their main.go.
type BuildConfig struct {
	FactoryFS    embed.FS // templates/factory — service scaffolding
	BuilderFS    embed.FS // templates/builder — Dockerfile generation
	DeploymentFS embed.FS // templates/deployment — k8s manifests
	Requirements *builders.Dependencies
	GoVersion    string
	AlpineVersion string
}

// Builder is the generic Go builder server. Embedded by specializations.
type Builder struct {
	services.BuilderServer
	*goservice.Service

	cfg           BuildConfig
	cacheLocation string
	answers       map[string]*agentv0.Answer
}

// New builds a generic Go Builder. Caller provides template FS + deps.
func New(svc *goservice.Service, cfg BuildConfig) *Builder {
	return &Builder{Service: svc, cfg: cfg}
}

func (s *Builder) Load(ctx context.Context, req *builderv0.LoadRequest) (*builderv0.LoadResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)

	if err := s.Builder.Load(ctx, req.Identity, s.Settings); err != nil {
		return nil, err
	}

	s.Service.SourceLocation = s.Local("%s", s.Settings.GoSourceDir())
	s.cacheLocation = s.Local(".cache")
	if s.cfg.Requirements != nil {
		s.cfg.Requirements.Localize(s.Location)
	}

	if req.CreationMode != nil {
		s.Builder.CreationMode = req.CreationMode
		gs, err := templates.ApplyTemplateFrom(ctx, shared.Embed(s.cfg.FactoryFS), "templates/factory/GETTING_STARTED.md", s.Information)
		if err != nil {
			return s.Builder.LoadError(err)
		}
		s.Builder.GettingStarted = gs
		return s.Builder.LoadResponse()
	}

	s.Endpoints, _ = s.Base.Service.LoadEndpoints(ctx)
	return s.Builder.LoadResponse()
}

func (s *Builder) Init(ctx context.Context, req *builderv0.InitRequest) (*builderv0.InitResponse, error) {
	defer s.Wool.Catch()
	s.Builder.LogInitRequest(req)
	ctx = s.Wool.Inject(ctx)
	s.DependencyEndpoints = req.DependenciesEndpoints
	return s.Builder.InitResponse()
}

func (s *Builder) Update(ctx context.Context, _ *builderv0.UpdateRequest) (*builderv0.UpdateResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)
	return &builderv0.UpdateResponse{}, nil
}

// Sync is a no-op on the generic layer — go has no protos to regenerate.
// Specializations (go-grpc) override.
func (s *Builder) Sync(ctx context.Context, _ *builderv0.SyncRequest) (*builderv0.SyncResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)
	return s.Builder.SyncResponse()
}

// Build produces a Docker image via the shared go builder helper.
func (s *Builder) Build(ctx context.Context, req *builderv0.BuildRequest) (*builderv0.BuildResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)
	return golanghelpers.BuildGoDocker(ctx, s.Base.Builder, req, s.Location,
		s.cfg.Requirements, s.cfg.BuilderFS, s.cfg.GoVersion, s.cfg.AlpineVersion)
}

// Deploy renders k8s manifests and applies them.
func (s *Builder) Deploy(ctx context.Context, req *builderv0.DeploymentRequest) (*builderv0.DeploymentResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)
	return golanghelpers.DeployGoKubernetes(ctx, s.Base.Builder, req, s.EnvironmentVariables, s.cfg.DeploymentFS)
}

// Options are the default communicate questions for `codefly add service`.
func (s *Builder) Options() []*agentv0.Question {
	return []*agentv0.Question{
		communicate.NewConfirm(&agentv0.Message{Name: HotReload, Message: "Code hot-reload?", Description: "Restart service when code changes"}, true),
		communicate.NewConfirm(&agentv0.Message{Name: DebugSymbols, Message: "Start with debug symbols?", Description: "Build with debug symbols for stack debugging"}, false),
		communicate.NewConfirm(&agentv0.Message{Name: RaceConditionDetectionRun, Message: "Start with race condition detection?", Description: "Build with -race"}, false),
	}
}

// CreateConfiguration is the template context passed to factory templates.
type CreateConfiguration struct {
	*services.Information
	Envs []string
}

func (s *Builder) Create(ctx context.Context, req *builderv0.CreateRequest) (*builderv0.CreateResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)

	if s.Builder.CreationMode != nil && s.Builder.CreationMode.Communicate && s.answers != nil {
		var err error
		s.Settings.HotReload, err = communicate.Confirm(s.answers, HotReload)
		if err != nil {
			return s.Builder.CreateError(err)
		}
		s.Settings.DebugSymbols, err = communicate.Confirm(s.answers, DebugSymbols)
		if err != nil {
			return s.Builder.CreateError(err)
		}
		s.Settings.RaceConditionDetectionRun, err = communicate.Confirm(s.answers, RaceConditionDetectionRun)
		if err != nil {
			return s.Builder.CreateError(err)
		}
	} else {
		options := s.Options()
		var err error
		s.Settings.HotReload, err = communicate.GetDefaultConfirm(options, HotReload)
		if err != nil {
			return s.Builder.CreateError(err)
		}
		s.Settings.DebugSymbols, err = communicate.GetDefaultConfirm(options, DebugSymbols)
		if err != nil {
			return s.Builder.CreateError(err)
		}
		s.Settings.RaceConditionDetectionRun, err = communicate.GetDefaultConfirm(options, RaceConditionDetectionRun)
		if err != nil {
			return s.Builder.CreateError(err)
		}
	}

	create := CreateConfiguration{Information: s.Information, Envs: []string{}}
	ignore := shared.NewIgnore("go.work*", "service.generation.codefly.yaml")

	if err := s.Templates(ctx, create, services.WithFactory(s.cfg.FactoryFS).WithPathSelect(ignore)); err != nil {
		return s.Builder.CreateError(err)
	}
	return s.Builder.CreateResponse(ctx, s.Settings)
}

func (s *Builder) Communicate(stream builderv0.Builder_CommunicateServer) error {
	asker := communicate.NewQuestionAsker(stream)
	answers, err := asker.RunSequence(s.Options())
	if err != nil {
		return err
	}
	s.answers = answers
	return nil
}
