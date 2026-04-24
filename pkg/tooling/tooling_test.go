package tooling_test

import (
	"testing"

	"github.com/codefly-dev/core/resources"

	gocode "github.com/codefly-dev/service-go/pkg/code"
	goruntime "github.com/codefly-dev/service-go/pkg/runtime"
	goservice "github.com/codefly-dev/service-go/pkg/service"
	gotooling "github.com/codefly-dev/service-go/pkg/tooling"
)

// TestToolingWiring verifies Tooling holds the Code and Runtime pointers
// the caller supplied. Specializations compose by passing the same pair.
func TestToolingWiring(t *testing.T) {
	svc := goservice.New(&resources.Agent{Kind: "codefly:service", Name: "go"})
	c := gocode.New(svc)
	rt := goruntime.New(svc)

	tl := gotooling.New(c, rt)
	if tl == nil {
		t.Fatal("New returned nil")
	}
	if tl.Code != c {
		t.Error("Tooling.Code is not the Code passed to New")
	}
	if tl.Runtime != rt {
		t.Error("Tooling.Runtime is not the Runtime passed to New")
	}
}
