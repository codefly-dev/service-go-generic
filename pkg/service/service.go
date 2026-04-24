// Package service defines the generic Go agent's shared state.
// Specializations (go-grpc, …) embed *Service in their own Service and
// add protocol-specific fields (endpoints, richer settings).
package service

import (
	"context"

	"github.com/codefly-dev/core/agents/services"
	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	"github.com/codefly-dev/core/resources"
	runners "github.com/codefly-dev/core/runners/base"
	golanghelpers "github.com/codefly-dev/core/runners/golang"
)

// Settings is the generic Go agent's configuration. Specializations embed
// this inline (yaml:",inline") to inherit GoAgentSettings fields:
//
//	type Settings struct {
//	    goservice.Settings `yaml:",inline"`
//	    RestEndpoint bool `yaml:"rest-endpoint"`
//	}
type Settings struct {
	golanghelpers.GoAgentSettings `yaml:",inline"`
}

// Service carries the shared state used by Runtime, Code, Tooling, Builder.
// Specializations embed *Service to inherit the identity, logger, location,
// and source resolution.
type Service struct {
	*services.Base
	Settings *Settings

	// SourceLocation is the path to the Go sources, set during Load. It
	// typically points at `<service>/code` (via Settings.GoSourceDir()) but
	// falls back to the service root if there's a go.mod there.
	SourceLocation string

	// ActiveEnv is the plugin's active RunnerEnvironment — set by
	// Runtime.Init via CreateRunnerEnvironment and consumed by Code /
	// Tooling / commands so every spawn routes through the same mode
	// (native / docker / nix). Distinct from Runtime.RunnerEnvironment
	// which is the Go-specific wrapper (GoRunnerEnvironment); this is
	// the underlying interface, obtained via env.Env() on the wrapper.
	// Nil before Runtime.Init — call-sites fall back to a fresh
	// NativeEnvironment for pre-init ops (typically Code file-level).
	ActiveEnv runners.RunnerEnvironment
}

// New builds a generic Go Service bound to the given agent manifest.
func New(agent *resources.Agent) *Service {
	return &Service{
		Base:     services.NewServiceBase(context.Background(), agent),
		Settings: &Settings{},
	}
}

// GetAgentInformation returns the generic Go agent advertisement.
// Specializations should override this; their overrides typically add
// protocols (HTTP/gRPC) and techniques. The generic advertisement has no
// README because README rendering depends on embed.FS which can't cross
// package boundaries — each specialization's binary embeds and renders
// its own.
func (s *Service) GetAgentInformation(_ context.Context, _ *agentv0.AgentInformationRequest) (*agentv0.AgentInformation, error) {
	return &agentv0.AgentInformation{
		RuntimeRequirements: []*agentv0.Runtime{
			{Type: agentv0.Runtime_GO},
			{Type: agentv0.Runtime_NIX},
		},
		Capabilities: []*agentv0.Capability{
			{Type: agentv0.Capability_BUILDER},
			{Type: agentv0.Capability_RUNTIME},
		},
		Languages: []*agentv0.Language{
			{Type: agentv0.Language_GO},
		},
		Protocols: []*agentv0.Protocol{},
		ReadMe:    "Generic Go service. Specializations add protocols.",
	}, nil
}
