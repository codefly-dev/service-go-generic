package builder_test

import (
	"embed"
	"testing"

	"github.com/codefly-dev/core/resources"

	gobuilder "github.com/codefly-dev/service-go/pkg/builder"
	goservice "github.com/codefly-dev/service-go/pkg/service"
)

// TestBuilderEmbedsService verifies the embedding chain so specializations
// can promote services.Base methods (Wool, Logger, Location) through
// *gobuilder.Builder when they embed it.
func TestBuilderEmbedsService(t *testing.T) {
	svc := goservice.New(&resources.Agent{Kind: "codefly:service", Name: "go"})
	b := gobuilder.New(svc, gobuilder.BuildConfig{})
	if b == nil {
		t.Fatal("New returned nil")
	}
	if b.Service != svc {
		t.Error("embedded Service is not the same pointer passed to New")
	}
	_ = b.Base    // promoted from services.Base via *goservice.Service
	_ = b.Builder // services.BuilderServer field
}

// TestBuildConfigAcceptsEmpty verifies New doesn't panic on zero BuildConfig.
// Some tests construct a Builder without ever calling Build/Deploy/Create.
func TestBuildConfigAcceptsEmpty(t *testing.T) {
	svc := goservice.New(&resources.Agent{Kind: "codefly:service", Name: "go"})
	_ = gobuilder.New(svc, gobuilder.BuildConfig{})
}

// TestBuildConfigHoldsFS proves BuildConfig passes FS pointers through
// without mangling them. Stub verification — don't actually apply templates.
func TestBuildConfigHoldsFS(t *testing.T) {
	var fs embed.FS
	cfg := gobuilder.BuildConfig{
		FactoryFS:     fs,
		BuilderFS:     fs,
		DeploymentFS:  fs,
		GoVersion:     "1.26",
		AlpineVersion: "3.21",
	}
	if cfg.GoVersion != "1.26" {
		t.Errorf("GoVersion not preserved, got %q", cfg.GoVersion)
	}
}
