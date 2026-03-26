package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

// newTestCode creates a Code instance pointing at a temporary Go project.
func newTestCode(t *testing.T) (*Code, string) {
	t.Helper()
	dir := t.TempDir()

	modContent := "module testmod\n\ngo 1.21\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(modContent), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := NewService()
	svc.sourceLocation = dir
	code := NewCode(svc)
	code.InitServer()
	return code, dir
}

func TestFix_GoImports(t *testing.T) {
	if _, err := exec.LookPath("goimports"); err != nil {
		t.Skip("goimports not installed, skipping")
	}

	code, dir := newTestCode(t)

	src := "package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	codeResp, err := code.Execute(context.Background(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_Fix{Fix: &codev0.FixRequest{File: "main.go"}},
	})
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	resp := codeResp.GetFix()
	if !resp.Success {
		t.Fatalf("Fix failed: %s", resp.Error)
	}
	if !strings.Contains(resp.Content, `"fmt"`) {
		t.Errorf("expected goimports to add fmt import, got:\n%s", resp.Content)
	}
}

func TestFix_GoFmt(t *testing.T) {
	if _, err := exec.LookPath("gofmt"); err != nil {
		t.Skip("gofmt not installed, skipping")
	}

	code, dir := newTestCode(t)

	src := "package main\n\nimport \"fmt\"\n\nfunc main() {\nfmt.Println(   \"hello\"   )\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	codeResp, err := code.Execute(context.Background(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_Fix{Fix: &codev0.FixRequest{File: "main.go"}},
	})
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	resp := codeResp.GetFix()
	if !resp.Success {
		t.Fatalf("Fix failed: %s", resp.Error)
	}
	if strings.Contains(resp.Content, `"hello"   )`) {
		t.Errorf("gofmt did not normalize spacing:\n%s", resp.Content)
	}
}

func TestFix_NoFile(t *testing.T) {
	code, _ := newTestCode(t)

	codeResp, err := code.Execute(context.Background(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_Fix{Fix: &codev0.FixRequest{File: "nonexistent.go"}},
	})
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	resp := codeResp.GetFix()
	if resp.Success {
		t.Error("expected Fix to fail for nonexistent file")
	}
	if !strings.Contains(resp.Error, "not found") {
		t.Errorf("expected 'not found' in error, got: %s", resp.Error)
	}
}

func TestReadFile(t *testing.T) {
	code, dir := newTestCode(t)

	content := "package main\n\nfunc main() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	codeResp, err := code.Execute(context.Background(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ReadFile{ReadFile: &codev0.ReadFileRequest{Path: "main.go"}},
	})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	resp := codeResp.GetReadFile()
	if !resp.Exists {
		t.Fatal("expected file to exist")
	}
	if resp.Content != content {
		t.Errorf("content mismatch: got %q, want %q", resp.Content, content)
	}
}

func TestReadFile_NotFound(t *testing.T) {
	code, _ := newTestCode(t)

	codeResp, err := code.Execute(context.Background(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ReadFile{ReadFile: &codev0.ReadFileRequest{Path: "nope.go"}},
	})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	resp := codeResp.GetReadFile()
	if resp.Exists {
		t.Error("expected file to not exist")
	}
}

func TestWriteFile(t *testing.T) {
	code, dir := newTestCode(t)

	content := "package sub\n"
	codeResp, err := code.Execute(context.Background(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_WriteFile{WriteFile: &codev0.WriteFileRequest{
			Path: "sub/lib.go", Content: content,
		}},
	})
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	resp := codeResp.GetWriteFile()
	if !resp.Success {
		t.Fatalf("WriteFile failed: %s", resp.Error)
	}

	got, err := os.ReadFile(filepath.Join(dir, "sub", "lib.go"))
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}
	if string(got) != content {
		t.Errorf("written content mismatch: got %q, want %q", string(got), content)
	}
}

func TestListFiles(t *testing.T) {
	code, dir := newTestCode(t)

	os.MkdirAll(filepath.Join(dir, "pkg"), 0o755)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "pkg", "lib.go"), []byte("package pkg\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test\n"), 0o644)

	codeResp, err := code.Execute(context.Background(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ListFiles{ListFiles: &codev0.ListFilesRequest{
			Recursive: true, Extensions: []string{".go"},
		}},
	})
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	resp := codeResp.GetListFiles()

	paths := make(map[string]bool)
	for _, f := range resp.Files {
		paths[f.Path] = true
	}
	if !paths["main.go"] {
		t.Error("expected main.go in listing")
	}
	if !paths["pkg/lib.go"] {
		t.Error("expected pkg/lib.go in listing")
	}
	if paths["README.md"] {
		t.Error("README.md should be filtered out with .go extension filter")
	}
}

func TestListFiles_NonRecursive(t *testing.T) {
	code, dir := newTestCode(t)

	os.MkdirAll(filepath.Join(dir, "pkg"), 0o755)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "pkg", "lib.go"), []byte("package pkg\n"), 0o644)

	codeResp, err := code.Execute(context.Background(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ListFiles{ListFiles: &codev0.ListFilesRequest{Recursive: false}},
	})
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	for _, f := range codeResp.GetListFiles().Files {
		if strings.Contains(f.Path, "pkg/lib.go") {
			t.Error("non-recursive listing should not include files in subdirectories")
		}
	}
}
