package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestGoCodeDoesNotReadDocumentationTree(t *testing.T) {
	root := filepath.Join(repoRoot(t), "go")
	guard := filepath.Join("cmd", "iw", "repository_boundary_test.go")
	var violations []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == guard {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		references, err := documentationPathReferences(relative, data)
		if err != nil {
			return err
		}
		violations = append(violations, references...)
		return nil
	})
	if err != nil {
		t.Fatalf("inspect Go source for documentation reads: %v", err)
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("Go source contains repository documentation paths:\n%s", strings.Join(violations, "\n"))
	}
}

func documentationPathReferences(filename string, data []byte) ([]string, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, filename, data, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	var references []string
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err == nil && isRepositoryDocumentationPath(value) {
			references = append(references, fileSet.Position(literal.Pos()).String())
		}
		return true
	})
	for _, group := range file.Comments {
		for _, comment := range group.List {
			const prefix = "//go:embed "
			if !strings.HasPrefix(comment.Text, prefix) {
				continue
			}
			for _, pattern := range strings.Fields(strings.TrimPrefix(comment.Text, prefix)) {
				pattern = strings.TrimPrefix(pattern, "all:")
				if unquoted, err := strconv.Unquote(pattern); err == nil {
					pattern = unquoted
				}
				if isRepositoryDocumentationPath(pattern) {
					references = append(references, fileSet.Position(comment.Pos()).String())
				}
			}
		}
	}
	return references, nil
}

func isRepositoryDocumentationPath(value string) bool {
	documentationDirectory := "docs"
	value = filepath.ToSlash(value)
	for strings.HasPrefix(value, "../") {
		value = strings.TrimPrefix(value, "../")
	}
	value = strings.TrimPrefix(value, "./")
	return value == documentationDirectory || strings.HasPrefix(value, documentationDirectory+"/")
}
