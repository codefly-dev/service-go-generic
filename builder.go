package main

import (
	"context"
	"embed"
	"fmt"

	"github.com/codefly-dev/core/agents/communicate"
	dockerhelpers "github.com/codefly-dev/core/agents/helpers/docker"
	"github.com/codefly-dev/core/agents/services"
	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/templates"
	"github.com/codefly-dev/core/wool"
)

type Builder struct {
	services.BuilderServer
	*Service
	cacheLocation string
	answers       map[string]*agentv0.Answer
}

func NewBuilder(svc *Service) *Builder {
	return &Builder{
		Service: svc,
	}
}

func (s *Builder) Load(ctx context.Context, req *builderv0.LoadRequest) (*builderv0.LoadResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)

	err := s.Builder.Load(ctx, req.Identity, s.Settings)
	if err != nil {
		return nil, err
	}

	s.sourceLocation = s.Local("%s", s.Settings.GoSourceDir())
	s.cacheLocation = s.Local(".cache")
	requirements.Localize(s.Location)

	if req.CreationMode != nil {
		s.Builder.CreationMode = req.CreationMode
		s.Builder.GettingStarted, err = templates.ApplyTemplateFrom(ctx, shared.Embed(factoryFS), "templates/factory/GETTING_STARTED.md", s.Information)
		if err != nil {
			return s.Builder.LoadError(err)
		}
		return s.Builder.LoadResponse()
	}

	// Optional: load endpoints if service has any. No gRPC required.
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

func (s *Builder) Update(ctx context.Context, req *builderv0.UpdateRequest) (*builderv0.UpdateResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)
	return &builderv0.UpdateResponse{}, nil
}

func (s *Builder) Sync(ctx context.Context, req *builderv0.SyncRequest) (*builderv0.SyncResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)
	// No proto generation — go-generic has no gRPC
	return s.Builder.SyncResponse()
}

type DockerTemplating struct {
	Components    []string
	Envs          []struct{ Key, Value string }
	GoVersion     string
	AlpineVersion string
}

func (s *Builder) Build(ctx context.Context, req *builderv0.BuildRequest) (*builderv0.BuildResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)

	dockerRequest, err := s.Builder.DockerBuildRequest(ctx, req)
	if err != nil {
		return nil, s.Wool.Wrapf(err, "docker build request")
	}

	image := s.DockerImage(dockerRequest)
	s.Wool.Debug("building docker image", wool.Field("image", image.FullName()))
	if !dockerhelpers.IsValidDockerImageName(image.Name) {
		return s.Builder.BuildError(fmt.Errorf("invalid docker image name: %s", image.Name))
	}

	docker := DockerTemplating{
		Components:    requirements.All(),
		GoVersion:    GoVersion,
		AlpineVersion: AlpineVersion,
	}

	_ = shared.DeleteFile(ctx, s.Local("builder/Dockerfile"))
	err = s.Templates(ctx, docker, services.WithBuilder(builderFS))
	if err != nil {
		return s.Builder.BuildError(err)
	}

	builder, err := dockerhelpers.NewBuilder(dockerhelpers.BuilderConfiguration{
		Root:        s.Location,
		Dockerfile:  "builder/Dockerfile",
		Ignorefile:  "builder/dockerignore",
		Destination: image,
		Output:      s.Wool,
	})
	if err != nil {
		return s.Builder.BuildError(err)
	}
	_, err = builder.Build(ctx)
	if err != nil {
		return s.Builder.BuildError(err)
	}
	s.Builder.WithDockerImages(image)
	return s.Builder.BuildResponse()
}

func (s *Builder) Deploy(ctx context.Context, req *builderv0.DeploymentRequest) (*builderv0.DeploymentResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)
	s.Builder.LogDeployRequest(req, s.Wool.Debug)
	s.EnvironmentVariables.SetRunning()
	k, err := s.Builder.KubernetesDeploymentRequest(ctx, req)
	if err != nil {
		return s.Builder.DeployError(err)
	}
	err = s.EnvironmentVariables.AddEndpoints(ctx,
		resources.LocalizeNetworkMapping(req.NetworkMappings, "localhost"),
		resources.NewContainerNetworkAccess())
	if err != nil {
		return s.Builder.DeployError(err)
	}
	err = s.EnvironmentVariables.AddEndpoints(ctx, req.DependenciesNetworkMappings, resources.NewContainerNetworkAccess())
	if err != nil {
		return s.Builder.DeployError(err)
	}
	err = s.EnvironmentVariables.AddConfigurations(ctx, req.Configuration)
	if err != nil {
		return s.Builder.DeployError(err)
	}
	err = s.EnvironmentVariables.AddConfigurations(ctx, req.DependenciesConfigurations...)
	if err != nil {
		return s.Builder.DeployError(err)
	}
	confs, err := s.EnvironmentVariables.Configurations()
	if err != nil {
		return s.Builder.DeployError(err)
	}
	cm, err := services.EnvsAsConfigMapData(confs...)
	if err != nil {
		return s.Builder.DeployError(err)
	}
	secrets, err := services.EnvsAsSecretData(s.EnvironmentVariables.Secrets()...)
	if err != nil {
		return s.Builder.DeployError(err)
	}
	params := services.DeploymentParameters{
		ConfigMap:  cm,
		SecretMap:  secrets,
		Parameters: struct{}{},
	}
	err = s.Builder.KustomizeDeploy(ctx, req.Environment, k, deploymentFS, params)
	if err != nil {
		return s.Builder.DeployError(err)
	}
	return s.Builder.DeployResponse()
}

func (s *Builder) Options() []*agentv0.Question {
	return []*agentv0.Question{
		communicate.NewConfirm(&agentv0.Message{Name: HotReload, Message: "Code hot-reload?", Description: "Restart service when code changes"}, true),
		communicate.NewConfirm(&agentv0.Message{Name: DebugSymbols, Message: "Start with debug symbols?", Description: "Build with debug symbols for stack debugging"}, false),
		communicate.NewConfirm(&agentv0.Message{Name: RaceConditionDetectionRun, Message: "Start with race condition detection?", Description: "Build with -race"}, false),
	}
}

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

	err := s.Templates(ctx, create, services.WithFactory(factoryFS).WithPathSelect(ignore))
	if err != nil {
		return s.Builder.CreateError(err)
	}

	// No CreateEndpoints — go-generic has no gRPC
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

//go:embed templates/factory
var factoryFS embed.FS

//go:embed templates/builder
var builderFS embed.FS

//go:embed templates/deployment
var deploymentFS embed.FS
