package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	corecode "github.com/codefly-dev/core/code"
	"github.com/codefly-dev/core/companions/lsp"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
	"github.com/codefly-dev/core/languages"

	"github.com/codefly-dev/core/wool"
)

// Code implements the Code gRPC service for the go-generic agent.
// It embeds GoCodeServer (which provides ListSymbols via AST, GetProjectInfo,
// ListDependencies, and all DefaultCodeServer operations) and overrides only
// truly agent-specific handlers: LSP operations, Fix/ApplyEdit with goimports,
// and dependency add/remove via the go tool.
type Code struct {
	*corecode.GoCodeServer
	*Service
	lspClient   lsp.Client
	initialized bool
}

func NewCode(svc *Service) *Code {
	c := &Code{
		Service:      svc,
		GoCodeServer: corecode.NewGoCodeServer(".", nil),
	}
	return c
}

// InitServer creates the GoCodeServer once sourceDir is resolved.
// Called automatically when sourceLocation is first set (in Runtime.Load),
// or can be called explicitly after configuration.
func (c *Code) InitServer() {
	c.GoCodeServer = corecode.NewGoCodeServer(c.sourceDir(), nil)
	c.registerOverrides()
	c.initialized = true
}

func (c *Code) ensureInit() {
	if !c.initialized {
		c.InitServer()
	}
}

func (c *Code) sourceDir() string {
	if c.sourceLocation != "" {
		return c.sourceLocation
	}
	if wd := os.Getenv("CODEFLY_AGENT_WORKDIR"); wd != "" {
		return wd
	}
	return c.Location + "/code"
}

func (c *Code) ensureLSP(ctx context.Context) (lsp.Client, error) {
	if c.lspClient != nil {
		return c.lspClient, nil
	}
	sourceDir := c.sourceDir()
	w := wool.Get(ctx).In("Code.ensureLSP")
	w.Info("starting LSP client", wool.DirField(sourceDir))
	client, err := lsp.NewClient(ctx, languages.GO, sourceDir)
	if err != nil {
		return nil, err
	}
	c.lspClient = client
	return client, nil
}

// registerOverrides wires agent-specific handlers on top of GoCodeServer.
// GoCodeServer already provides: list_symbols (AST), get_project_info, list_dependencies.
// We override list_symbols with LSP when available, and add Fix/ApplyEdit with goimports.
func (c *Code) registerOverrides() {
	c.Override("fix", c.handleFix)
	c.Override("apply_edit", c.handleApplyEdit)
	c.Override("list_symbols", c.handleListSymbols)
	c.Override("get_diagnostics", c.handleGetDiagnostics)
	c.Override("go_to_definition", c.handleGoToDefinition)
	c.Override("find_references", c.handleFindReferences)
	c.Override("rename_symbol", c.handleRenameSymbol)
	c.Override("get_hover_info", c.handleGetHoverInfo)
	c.Override("add_dependency", c.handleAddDependency)
	c.Override("remove_dependency", c.handleRemoveDependency)
	c.Override("get_call_graph", c.handleGetCallGraph)
}

// --- Lazy init wrappers ---

func (c *Code) GetProjectInfo(ctx context.Context, req *codev0.GetProjectInfoRequest) (*codev0.GetProjectInfoResponse, error) {
	c.ensureInit()
	return c.GoCodeServer.GetProjectInfo(ctx, req)
}

func (c *Code) ListSymbols(ctx context.Context, req *codev0.ListSymbolsRequest) (*codev0.ListSymbolsResponse, error) {
	c.ensureInit()
	return c.GoCodeServer.ListSymbols(ctx, req)
}

func (c *Code) Execute(ctx context.Context, req *codev0.CodeRequest) (*codev0.CodeResponse, error) {
	c.ensureInit()
	return c.GoCodeServer.Execute(ctx, req)
}

// --- Go-specific: Fix (goimports + gofmt) ---

func (c *Code) handleFix(ctx context.Context, req *codev0.CodeRequest) (*codev0.CodeResponse, error) {
	r := req.GetFix()
	absPath := filepath.Join(c.sourceDir(), r.File)
	data, err := os.ReadFile(absPath)
	if err != nil {
		return fixResp(false, "", fmt.Sprintf("file not found: %s", r.File), nil), nil
	}

	tmpFile, err := os.CreateTemp("", "mind-fix-*.go")
	if err != nil {
		return fixResp(false, "", fmt.Sprintf("create temp: %v", err), nil), nil
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return fixResp(false, "", fmt.Sprintf("write temp: %v", err), nil), nil
	}
	tmpFile.Close()

	var actions []string
	if out, err := exec.CommandContext(ctx, "goimports", "-w", tmpPath).CombinedOutput(); err != nil {
		wool.Get(ctx).In("Code.Fix").Warn("goimports failed", wool.Field("error", string(out)))
	} else {
		actions = append(actions, "goimports")
	}
	if out, err := exec.CommandContext(ctx, "gofmt", "-w", tmpPath).CombinedOutput(); err != nil {
		wool.Get(ctx).In("Code.Fix").Warn("gofmt failed", wool.Field("error", string(out)))
	} else {
		actions = append(actions, "gofmt")
	}
	result, _ := os.ReadFile(tmpPath)
	return fixResp(true, string(result), "", actions), nil
}

// --- Go-specific: ApplyEdit with auto-fix ---

func (c *Code) handleApplyEdit(ctx context.Context, req *codev0.CodeRequest) (*codev0.CodeResponse, error) {
	r := req.GetApplyEdit()
	absPath := filepath.Join(c.sourceDir(), r.File)
	data, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return applyEditResp(false, "", "", fmt.Sprintf("file not found: %s", r.File), nil), nil
		}
		return nil, fmt.Errorf("reading %s: %w", r.File, err)
	}

	result := corecode.SmartEdit(string(data), r.Find, r.Replace)
	if !result.OK {
		return applyEditResp(false, "", "", "FIND block does not match any content in the file", nil), nil
	}

	edited := result.Content
	var fixActions []string
	if r.AutoFix {
		tmpFile, tmpErr := os.CreateTemp("", "mind-edit-*.go")
		if tmpErr == nil {
			tmpPath := tmpFile.Name()
			defer os.Remove(tmpPath)
			tmpFile.Write([]byte(edited))
			tmpFile.Close()

			if out, fixErr := exec.CommandContext(ctx, "goimports", "-w", tmpPath).CombinedOutput(); fixErr != nil {
				wool.Get(ctx).In("Code.ApplyEdit").Warn("goimports failed", wool.Field("error", string(out)))
			} else {
				fixActions = append(fixActions, "goimports")
			}
			if out, fixErr := exec.CommandContext(ctx, "gofmt", "-w", tmpPath).CombinedOutput(); fixErr != nil {
				wool.Get(ctx).In("Code.ApplyEdit").Warn("gofmt failed", wool.Field("error", string(out)))
			} else {
				fixActions = append(fixActions, "gofmt")
			}
			if fixed, readErr := os.ReadFile(tmpPath); readErr == nil {
				edited = string(fixed)
			}
		}
	}
	return applyEditResp(true, edited, result.Strategy, "", fixActions), nil
}

// --- Go-specific: LSP operations ---

func (c *Code) handleListSymbols(ctx context.Context, req *codev0.CodeRequest) (*codev0.CodeResponse, error) {
	r := req.GetListSymbols()
	client, err := c.ensureLSP(ctx)
	if err != nil {
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_ListSymbols{ListSymbols: &codev0.ListSymbolsResponse{
			Status: &codev0.ListSymbolsStatus{State: codev0.ListSymbolsStatus_ERROR, Message: err.Error()},
		}}}, nil
	}
	symbols, err := client.ListSymbols(ctx, r.File)
	if err != nil {
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_ListSymbols{ListSymbols: &codev0.ListSymbolsResponse{
			Status: &codev0.ListSymbolsStatus{State: codev0.ListSymbolsStatus_ERROR, Message: err.Error()},
		}}}, nil
	}
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_ListSymbols{ListSymbols: &codev0.ListSymbolsResponse{
		Status: &codev0.ListSymbolsStatus{State: codev0.ListSymbolsStatus_SUCCESS}, Symbols: symbols,
	}}}, nil
}

func (c *Code) handleGetDiagnostics(ctx context.Context, req *codev0.CodeRequest) (*codev0.CodeResponse, error) {
	r := req.GetGetDiagnostics()
	client, err := c.ensureLSP(ctx)
	if err != nil {
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_GetDiagnostics{GetDiagnostics: &codev0.GetDiagnosticsResponse{}}}, nil
	}
	results, err := client.Diagnostics(ctx, r.File)
	if err != nil {
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_GetDiagnostics{GetDiagnostics: &codev0.GetDiagnosticsResponse{}}}, nil
	}
	var diags []*codev0.Diagnostic
	for _, d := range results {
		diags = append(diags, &codev0.Diagnostic{
			File: d.File, Line: d.Line, Column: d.Column, EndLine: d.EndLine, EndColumn: d.EndColumn,
			Message: d.Message, Severity: lspSevToProto(d.Severity), Source: d.Source, Code: d.Code,
		})
	}
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_GetDiagnostics{GetDiagnostics: &codev0.GetDiagnosticsResponse{Diagnostics: diags}}}, nil
}

func (c *Code) handleGoToDefinition(ctx context.Context, req *codev0.CodeRequest) (*codev0.CodeResponse, error) {
	r := req.GetGoToDefinition()
	client, err := c.ensureLSP(ctx)
	if err != nil {
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_GoToDefinition{GoToDefinition: &codev0.GoToDefinitionResponse{}}}, nil
	}
	locs, err := client.Definition(ctx, r.File, int(r.Line), int(r.Column))
	if err != nil {
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_GoToDefinition{GoToDefinition: &codev0.GoToDefinitionResponse{}}}, nil
	}
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_GoToDefinition{GoToDefinition: &codev0.GoToDefinitionResponse{Locations: lspLocsToProto(locs)}}}, nil
}

func (c *Code) handleFindReferences(ctx context.Context, req *codev0.CodeRequest) (*codev0.CodeResponse, error) {
	r := req.GetFindReferences()
	client, err := c.ensureLSP(ctx)
	if err != nil {
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_FindReferences{FindReferences: &codev0.FindReferencesResponse{}}}, nil
	}
	locs, err := client.References(ctx, r.File, int(r.Line), int(r.Column))
	if err != nil {
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_FindReferences{FindReferences: &codev0.FindReferencesResponse{}}}, nil
	}
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_FindReferences{FindReferences: &codev0.FindReferencesResponse{Locations: lspLocsToProto(locs)}}}, nil
}

func (c *Code) handleRenameSymbol(ctx context.Context, req *codev0.CodeRequest) (*codev0.CodeResponse, error) {
	r := req.GetRenameSymbol()
	client, err := c.ensureLSP(ctx)
	if err != nil {
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_RenameSymbol{RenameSymbol: &codev0.RenameSymbolResponse{Success: false, Error: err.Error()}}}, nil
	}
	edits, err := client.Rename(ctx, r.File, int(r.Line), int(r.Column), r.NewName)
	if err != nil {
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_RenameSymbol{RenameSymbol: &codev0.RenameSymbolResponse{Success: false, Error: err.Error()}}}, nil
	}
	var textEdits []*codev0.TextEdit
	fileSet := make(map[string]bool)
	for _, e := range edits {
		textEdits = append(textEdits, &codev0.TextEdit{
			File: e.File, StartLine: e.StartLine, StartColumn: e.StartColumn,
			EndLine: e.EndLine, EndColumn: e.EndColumn, NewText: e.NewText,
		})
		fileSet[e.File] = true
	}
	var files []string
	for f := range fileSet {
		files = append(files, f)
	}
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_RenameSymbol{RenameSymbol: &codev0.RenameSymbolResponse{Success: true, Edits: textEdits, Files: files}}}, nil
}

func (c *Code) handleGetHoverInfo(ctx context.Context, req *codev0.CodeRequest) (*codev0.CodeResponse, error) {
	r := req.GetGetHoverInfo()
	client, err := c.ensureLSP(ctx)
	if err != nil {
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_GetHoverInfo{GetHoverInfo: &codev0.GetHoverInfoResponse{}}}, nil
	}
	hover, err := client.Hover(ctx, r.File, int(r.Line), int(r.Column))
	if err != nil {
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_GetHoverInfo{GetHoverInfo: &codev0.GetHoverInfoResponse{}}}, nil
	}
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_GetHoverInfo{GetHoverInfo: &codev0.GetHoverInfoResponse{Content: hover.Content, Language: hover.Language}}}, nil
}

// --- Go-specific: Dependency management ---

func (c *Code) handleAddDependency(ctx context.Context, req *codev0.CodeRequest) (*codev0.CodeResponse, error) {
	r := req.GetAddDependency()
	pkg := r.PackageName
	if r.Version != "" {
		pkg += "@" + r.Version
	}
	cmd := exec.CommandContext(ctx, "go", "get", pkg)
	cmd.Dir = c.sourceDir()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_AddDependency{AddDependency: &codev0.AddDependencyResponse{
			Success: false, Error: fmt.Sprintf("go get: %s", string(out)),
		}}}, nil
	}
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_AddDependency{AddDependency: &codev0.AddDependencyResponse{Success: true, InstalledVersion: r.Version}}}, nil
}

func (c *Code) handleRemoveDependency(ctx context.Context, req *codev0.CodeRequest) (*codev0.CodeResponse, error) {
	r := req.GetRemoveDependency()
	cmd := exec.CommandContext(ctx, "go", "mod", "edit", "-droprequire", r.PackageName)
	cmd.Dir = c.sourceDir()
	if out, err := cmd.CombinedOutput(); err != nil {
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_RemoveDependency{RemoveDependency: &codev0.RemoveDependencyResponse{
			Success: false, Error: fmt.Sprintf("go mod edit: %s", string(out)),
		}}}, nil
	}
	tidyCmd := exec.CommandContext(ctx, "go", "mod", "tidy")
	tidyCmd.Dir = c.sourceDir()
	tidyCmd.CombinedOutput()
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_RemoveDependency{RemoveDependency: &codev0.RemoveDependencyResponse{Success: true}}}, nil
}

// --- Helpers ---

func fixResp(success bool, content, errMsg string, actions []string) *codev0.CodeResponse {
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_Fix{Fix: &codev0.FixResponse{
		Success: success, Content: content, Error: errMsg, Actions: actions,
	}}}
}

func applyEditResp(success bool, content, strategy, errMsg string, fixActions []string) *codev0.CodeResponse {
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_ApplyEdit{ApplyEdit: &codev0.ApplyEditResponse{
		Success: success, Content: content, Strategy: strategy, Error: errMsg, FixActions: fixActions,
	}}}
}

func lspSevToProto(sev string) codev0.DiagnosticSeverity {
	switch sev {
	case "error":
		return codev0.DiagnosticSeverity_DIAGNOSTIC_SEVERITY_ERROR
	case "warning":
		return codev0.DiagnosticSeverity_DIAGNOSTIC_SEVERITY_WARNING
	case "information":
		return codev0.DiagnosticSeverity_DIAGNOSTIC_SEVERITY_INFORMATION
	case "hint":
		return codev0.DiagnosticSeverity_DIAGNOSTIC_SEVERITY_HINT
	default:
		return codev0.DiagnosticSeverity_DIAGNOSTIC_SEVERITY_UNKNOWN
	}
}

func lspLocsToProto(locs []lsp.LocationResult) []*codev0.Location {
	var out []*codev0.Location
	for _, l := range locs {
		out = append(out, &codev0.Location{
			File: l.File, Line: int32(l.Line), Column: int32(l.Column),
			EndLine: int32(l.EndLine), EndColumn: int32(l.EndColumn),
		})
	}
	return out
}

