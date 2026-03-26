package main

import (
	"context"
	"embed"

	"github.com/codefly-dev/core/builders"
	"github.com/codefly-dev/core/templates"
	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/agents/services"
	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	configurations "github.com/codefly-dev/core/resources"
	golanghelpers "github.com/codefly-dev/core/runners/golang"
	"github.com/codefly-dev/core/shared"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Agent version
var agent = shared.Must(configurations.LoadFromFs[configurations.Agent](shared.Embed(infoFS)))

var requirements = builders.NewDependencies(agent.Name,
	builders.NewDependency("service.codefly.yaml"),
	builders.NewDependency("code").WithPathSelect(shared.NewSelect("*.go")),
)

type Settings struct {
	golanghelpers.GoAgentSettings `yaml:",inline"`
}

const HotReload = golanghelpers.SettingHotReload
const DebugSymbols = golanghelpers.SettingDebugSymbols
const RaceConditionDetectionRun = golanghelpers.SettingRaceConditionDetectionRun

// Service is the go-generic agent service. No gRPC/REST endpoints — plain Go binary for Layer 1 dogfooding.
type Service struct {
	*services.Base
	*Settings
	sourceLocation string
}

func (s *Service) GetAgentInformation(ctx context.Context, _ *agentv0.AgentInformationRequest) (*agentv0.AgentInformation, error) {
	defer s.Wool.Catch()

	readme, err := templates.ApplyTemplateFrom(ctx, shared.Embed(readmeFS), "templates/agent/README.md", s.Information)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &agentv0.AgentInformation{
		RuntimeRequirements: []*agentv0.Runtime{
			{Type: agentv0.Runtime_GO},
		},
		Capabilities: []*agentv0.Capability{
			{Type: agentv0.Capability_BUILDER},
			{Type: agentv0.Capability_RUNTIME},
		},
		Languages: []*agentv0.Language{
			{Type: agentv0.Language_GO},
		},
		Protocols:  []*agentv0.Protocol{}, // No gRPC/REST — generic Go only
		ReadMe:     readme,
	}, nil
}

func NewService() *Service {
	return &Service{
		Base:     services.NewServiceBase(context.Background(), agent),
		Settings: &Settings{},
	}
}

// GoVersion is the version of Go
const GoVersion = "1.26"

// AlpineVersion is the version of Alpine
const AlpineVersion = "3.21"

// Runtime Image
var runtimeImage = &configurations.DockerImage{Name: "codeflydev/go", Tag: "0.0.10"}

func main() {
	svc := NewService()
	agents.Serve(agents.PluginRegistration{
		Agent:   svc,
		Runtime: NewRuntime(svc),
		Builder: NewBuilder(svc),
		Code:    NewCode(svc),
	})
}

//go:embed agent.codefly.yaml
var infoFS embed.FS

//go:embed templates/agent
var readmeFS embed.FS
