// Package tooling implements the Tooling gRPC service for the generic
// Go agent. It delegates to the Code server for analysis ops and Runtime
// for test/lint/build. Specializations typically don't override Tooling —
// their Code/Runtime overrides flow through automatically.
package tooling

import (
	"context"
	"fmt"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
	toolingv0 "github.com/codefly-dev/core/generated/go/codefly/services/tooling/v0"

	gocode "github.com/codefly-dev/service-go/pkg/code"
	goruntime "github.com/codefly-dev/service-go/pkg/runtime"
)

// Tooling is the unified Mind-facing interface: LSP-ish ops delegate to
// Code, dev ops (build/test/lint) delegate to Runtime. Mind's ingestion
// pipeline calls ToolingClient.GetCallGraph() and friends via the
// ToolingCodeClient adapter.
type Tooling struct {
	toolingv0.UnimplementedToolingServer
	Code    *gocode.Code
	Runtime *goruntime.Runtime
}

// New builds a Tooling server wired to the given Code and Runtime.
func New(code *gocode.Code, rt *goruntime.Runtime) *Tooling {
	return &Tooling{Code: code, Runtime: rt}
}

// ── LSP Operations (delegate to Code's LSP client) ─────

func (t *Tooling) ListSymbols(ctx context.Context, req *toolingv0.ListSymbolsRequest) (*toolingv0.ListSymbolsResponse, error) {
	resp, err := t.Code.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ListSymbols{ListSymbols: &codev0.ListSymbolsRequest{File: req.File}},
	})
	if err != nil {
		return nil, fmt.Errorf("tooling list_symbols: %w", err)
	}
	ls := resp.GetListSymbols()
	if ls == nil {
		return &toolingv0.ListSymbolsResponse{}, nil
	}
	return &toolingv0.ListSymbolsResponse{Symbols: codeSymbolsToTooling(ls.Symbols)}, nil
}

func (t *Tooling) GetDiagnostics(ctx context.Context, req *toolingv0.GetDiagnosticsRequest) (*toolingv0.GetDiagnosticsResponse, error) {
	resp, err := t.Code.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_GetDiagnostics{GetDiagnostics: &codev0.GetDiagnosticsRequest{File: req.File}},
	})
	if err != nil {
		return nil, fmt.Errorf("tooling get_diagnostics: %w", err)
	}
	gd := resp.GetGetDiagnostics()
	if gd == nil {
		return &toolingv0.GetDiagnosticsResponse{}, nil
	}
	return &toolingv0.GetDiagnosticsResponse{Diagnostics: codeDiagsToTooling(gd.Diagnostics)}, nil
}

func (t *Tooling) GoToDefinition(ctx context.Context, req *toolingv0.GoToDefinitionRequest) (*toolingv0.GoToDefinitionResponse, error) {
	resp, err := t.Code.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_GoToDefinition{GoToDefinition: &codev0.GoToDefinitionRequest{
			File: req.File, Line: req.Line, Column: req.Column,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("tooling go_to_definition: %w", err)
	}
	gtd := resp.GetGoToDefinition()
	if gtd == nil {
		return &toolingv0.GoToDefinitionResponse{}, nil
	}
	return &toolingv0.GoToDefinitionResponse{Locations: codeLocsToTooling(gtd.Locations)}, nil
}

func (t *Tooling) FindReferences(ctx context.Context, req *toolingv0.FindReferencesRequest) (*toolingv0.FindReferencesResponse, error) {
	resp, err := t.Code.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_FindReferences{FindReferences: &codev0.FindReferencesRequest{
			File: req.File, Line: req.Line, Column: req.Column,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("tooling find_references: %w", err)
	}
	fr := resp.GetFindReferences()
	if fr == nil {
		return &toolingv0.FindReferencesResponse{}, nil
	}
	return &toolingv0.FindReferencesResponse{Locations: codeLocsToTooling(fr.Locations)}, nil
}

func (t *Tooling) RenameSymbol(ctx context.Context, req *toolingv0.RenameSymbolRequest) (*toolingv0.RenameSymbolResponse, error) {
	resp, err := t.Code.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_RenameSymbol{RenameSymbol: &codev0.RenameSymbolRequest{
			File: req.File, Line: req.Line, Column: req.Column, NewName: req.NewName,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("tooling rename_symbol: %w", err)
	}
	rs := resp.GetRenameSymbol()
	if rs == nil {
		return &toolingv0.RenameSymbolResponse{Success: false, Error: "no response"}, nil
	}
	return &toolingv0.RenameSymbolResponse{
		Success: rs.Success, Error: rs.Error,
		Edits: codeEditsToTooling(rs.Edits), Files: rs.Files,
	}, nil
}

func (t *Tooling) GetHoverInfo(ctx context.Context, req *toolingv0.GetHoverInfoRequest) (*toolingv0.GetHoverInfoResponse, error) {
	resp, err := t.Code.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_GetHoverInfo{GetHoverInfo: &codev0.GetHoverInfoRequest{
			File: req.File, Line: req.Line, Column: req.Column,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("tooling get_hover_info: %w", err)
	}
	hi := resp.GetGetHoverInfo()
	if hi == nil {
		return &toolingv0.GetHoverInfoResponse{}, nil
	}
	return &toolingv0.GetHoverInfoResponse{Content: hi.Content, Language: hi.Language}, nil
}

func (t *Tooling) GetCompletions(ctx context.Context, req *toolingv0.GetCompletionsRequest) (*toolingv0.GetCompletionsResponse, error) {
	resp, err := t.Code.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_GetCompletions{GetCompletions: &codev0.GetCompletionsRequest{
			File: req.File, Line: req.Line, Column: req.Column,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("tooling get_completions: %w", err)
	}
	gc := resp.GetGetCompletions()
	if gc == nil {
		return &toolingv0.GetCompletionsResponse{}, nil
	}
	var items []*toolingv0.CompletionItem
	for _, item := range gc.Items {
		items = append(items, &toolingv0.CompletionItem{
			Label: item.Label, Detail: item.Detail,
			Documentation: item.Documentation, InsertText: item.InsertText,
		})
	}
	return &toolingv0.GetCompletionsResponse{Items: items, IsIncomplete: gc.IsIncomplete}, nil
}

// ── Code Modification ──────────────────────────────────

func (t *Tooling) Fix(ctx context.Context, req *toolingv0.FixRequest) (*toolingv0.FixResponse, error) {
	resp, err := t.Code.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_Fix{Fix: &codev0.FixRequest{File: req.File}},
	})
	if err != nil {
		return nil, fmt.Errorf("tooling fix: %w", err)
	}
	fix := resp.GetFix()
	if fix == nil {
		return &toolingv0.FixResponse{Success: false, Error: "no response"}, nil
	}
	return &toolingv0.FixResponse{
		Success: fix.Success, Content: fix.Content,
		Error: fix.Error, Actions: fix.Actions,
	}, nil
}

func (t *Tooling) ApplyEdit(ctx context.Context, req *toolingv0.ApplyEditRequest) (*toolingv0.ApplyEditResponse, error) {
	resp, err := t.Code.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ApplyEdit{ApplyEdit: &codev0.ApplyEditRequest{
			File: req.File, Find: req.Find, Replace: req.Replace, AutoFix: req.AutoFix,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("tooling apply_edit: %w", err)
	}
	ae := resp.GetApplyEdit()
	if ae == nil {
		return &toolingv0.ApplyEditResponse{Success: false, Error: "no response"}, nil
	}
	return &toolingv0.ApplyEditResponse{
		Success: ae.Success, Content: ae.Content,
		Error: ae.Error, Strategy: ae.Strategy, FixActions: ae.FixActions,
	}, nil
}

// ── Dependencies ───────────────────────────────────────

func (t *Tooling) ListDependencies(ctx context.Context, _ *toolingv0.ListDependenciesRequest) (*toolingv0.ListDependenciesResponse, error) {
	resp, err := t.Code.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ListDependencies{ListDependencies: &codev0.ListDependenciesRequest{}},
	})
	if err != nil {
		return nil, fmt.Errorf("tooling list_dependencies: %w", err)
	}
	ld := resp.GetListDependencies()
	if ld == nil {
		return &toolingv0.ListDependenciesResponse{}, nil
	}
	var deps []*toolingv0.Dependency
	for _, d := range ld.Dependencies {
		deps = append(deps, &toolingv0.Dependency{Name: d.Name, Version: d.Version, Direct: d.Direct})
	}
	return &toolingv0.ListDependenciesResponse{Dependencies: deps, Error: ld.Error}, nil
}

func (t *Tooling) AddDependency(ctx context.Context, req *toolingv0.AddDependencyRequest) (*toolingv0.AddDependencyResponse, error) {
	resp, err := t.Code.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_AddDependency{AddDependency: &codev0.AddDependencyRequest{
			PackageName: req.PackageName, Version: req.Version,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("tooling add_dependency: %w", err)
	}
	ad := resp.GetAddDependency()
	if ad == nil {
		return &toolingv0.AddDependencyResponse{Success: false, Error: "no response"}, nil
	}
	return &toolingv0.AddDependencyResponse{
		Success: ad.Success, Error: ad.Error, InstalledVersion: ad.InstalledVersion,
	}, nil
}

func (t *Tooling) RemoveDependency(ctx context.Context, req *toolingv0.RemoveDependencyRequest) (*toolingv0.RemoveDependencyResponse, error) {
	resp, err := t.Code.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_RemoveDependency{RemoveDependency: &codev0.RemoveDependencyRequest{
			PackageName: req.PackageName,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("tooling remove_dependency: %w", err)
	}
	rd := resp.GetRemoveDependency()
	if rd == nil {
		return &toolingv0.RemoveDependencyResponse{Success: false, Error: "no response"}, nil
	}
	return &toolingv0.RemoveDependencyResponse{Success: rd.Success, Error: rd.Error}, nil
}

// ── Analysis ───────────────────────────────────────────

func (t *Tooling) GetProjectInfo(ctx context.Context, _ *toolingv0.GetProjectInfoRequest) (*toolingv0.GetProjectInfoResponse, error) {
	resp, err := t.Code.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_GetProjectInfo{GetProjectInfo: &codev0.GetProjectInfoRequest{}},
	})
	if err != nil {
		return nil, fmt.Errorf("tooling get_project_info: %w", err)
	}
	pi := resp.GetGetProjectInfo()
	if pi == nil {
		return &toolingv0.GetProjectInfoResponse{}, nil
	}
	var pkgs []*toolingv0.PackageInfo
	for _, p := range pi.Packages {
		pkgs = append(pkgs, &toolingv0.PackageInfo{
			Name: p.Name, RelativePath: p.RelativePath,
			Files: p.Files, Imports: p.Imports, Doc: p.Doc,
		})
	}
	var deps []*toolingv0.Dependency
	for _, d := range pi.Dependencies {
		deps = append(deps, &toolingv0.Dependency{Name: d.Name, Version: d.Version, Direct: d.Direct})
	}
	return &toolingv0.GetProjectInfoResponse{
		Module: pi.Module, Language: pi.Language, LanguageVersion: pi.LanguageVersion,
		Packages: pkgs, Dependencies: deps, FileHashes: pi.FileHashes, Error: pi.Error,
	}, nil
}

func (t *Tooling) GetCallGraph(ctx context.Context, req *toolingv0.GetCallGraphRequest) (*toolingv0.GetCallGraphResponse, error) {
	t.Code.EnsureInit()
	result := t.Code.ComputeCallGraph(t.Code.SourceDir())

	var calls []*toolingv0.CallEdge
	for _, c := range result.Calls {
		calls = append(calls, &toolingv0.CallEdge{
			CallerId: c.CallerID, CalleeId: c.CalleeID,
			CallType: c.CallType,
			CallSite: &toolingv0.Location{
				File: c.File, Line: int32(c.Line),
			},
		})
	}
	var impls []*toolingv0.ImplementsEdge
	for _, i := range result.Implements {
		impls = append(impls, &toolingv0.ImplementsEdge{
			TypeId: i.TypeID, InterfaceId: i.InterfaceID,
		})
	}
	algorithm := "vta"
	if req.GetAlgorithm() != "" {
		algorithm = req.GetAlgorithm()
	}
	return &toolingv0.GetCallGraphResponse{
		Calls: calls, Implements: impls,
		FunctionsAnalyzed: int32(len(calls)),
		AlgorithmUsed:     algorithm,
		Error:             result.Error,
	}, nil
}

// ── Dev Validation (delegates to Runtime) ──────────────

func (t *Tooling) Build(ctx context.Context, _ *toolingv0.BuildRequest) (*toolingv0.BuildResponse, error) {
	resp, err := t.Runtime.Build(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("tooling build: %w", err)
	}
	success := resp.Status != nil && resp.Status.State == runtimev0.BuildStatus_SUCCESS
	return &toolingv0.BuildResponse{Success: success, Output: resp.Output}, nil
}

func (t *Tooling) Test(ctx context.Context, _ *toolingv0.TestRequest) (*toolingv0.TestResponse, error) {
	resp, err := t.Runtime.Test(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("tooling test: %w", err)
	}
	success := resp.Status != nil && resp.Status.State == runtimev0.TestStatus_SUCCESS
	return &toolingv0.TestResponse{
		Success: success, Output: resp.Output,
		TestsRun: resp.TestsRun, TestsPassed: resp.TestsPassed,
		TestsFailed: resp.TestsFailed, TestsSkipped: resp.TestsSkipped,
		CoveragePct: resp.CoveragePct, Failures: resp.Failures,
	}, nil
}

func (t *Tooling) Lint(ctx context.Context, _ *toolingv0.LintRequest) (*toolingv0.LintResponse, error) {
	resp, err := t.Runtime.Lint(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("tooling lint: %w", err)
	}
	success := resp.Status != nil && resp.Status.State == runtimev0.LintStatus_SUCCESS
	return &toolingv0.LintResponse{Success: success, Output: resp.Output}, nil
}

// ── Type converters (Code proto → Tooling proto) ───────

func codeSymbolsToTooling(symbols []*codev0.Symbol) []*toolingv0.Symbol {
	var out []*toolingv0.Symbol
	for _, s := range symbols {
		ts := &toolingv0.Symbol{
			Name: s.Name, Kind: toolingv0.SymbolKind(s.Kind),
			Signature: s.Signature, Documentation: s.Documentation, Parent: s.Parent,
			// HyperAST-style hash fields — pass through from Code to Tooling.
			QualifiedName: s.QualifiedName,
			BodyHash:      s.BodyHash,
			SignatureHash: s.SignatureHash,
		}
		if s.Location != nil {
			ts.Location = &toolingv0.Location{
				File: s.Location.File, Line: s.Location.Line, Column: s.Location.Column,
				EndLine: s.Location.EndLine, EndColumn: s.Location.EndColumn,
			}
		}
		ts.Children = codeSymbolsToTooling(s.Children)
		out = append(out, ts)
	}
	return out
}

func codeLocsToTooling(locs []*codev0.Location) []*toolingv0.Location {
	var out []*toolingv0.Location
	for _, l := range locs {
		out = append(out, &toolingv0.Location{
			File: l.File, Line: l.Line, Column: l.Column,
			EndLine: l.EndLine, EndColumn: l.EndColumn,
		})
	}
	return out
}

func codeEditsToTooling(edits []*codev0.TextEdit) []*toolingv0.TextEdit {
	var out []*toolingv0.TextEdit
	for _, e := range edits {
		out = append(out, &toolingv0.TextEdit{
			File: e.File, StartLine: e.StartLine, StartColumn: e.StartColumn,
			EndLine: e.EndLine, EndColumn: e.EndColumn, NewText: e.NewText,
		})
	}
	return out
}

func codeDiagsToTooling(diags []*codev0.Diagnostic) []*toolingv0.Diagnostic {
	var out []*toolingv0.Diagnostic
	for _, d := range diags {
		out = append(out, &toolingv0.Diagnostic{
			File: d.File, Line: d.Line, Column: d.Column,
			EndLine: d.EndLine, EndColumn: d.EndColumn,
			Message: d.Message, Severity: toolingv0.DiagnosticSeverity(d.Severity),
			Source: d.Source, Code: d.Code,
		})
	}
	return out
}
