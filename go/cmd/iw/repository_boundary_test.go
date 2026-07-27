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

func TestGoTestsDoNotReadDocumentationTree(t *testing.T) {
	root := filepath.Join(repoRoot(t), "go")
	var violations []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		references, err := documentationPathLiterals(relative, data)
		if err != nil {
			return err
		}
		violations = append(violations, references...)
		return nil
	})
	if err != nil {
		t.Fatalf("inspect Go tests for documentation reads: %v", err)
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("Go tests contain repository documentation paths:\n%s", strings.Join(violations, "\n"))
	}
}

func documentationPathLiterals(filename string, data []byte) ([]string, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, filename, data, 0)
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
	return references, nil
}

func isRepositoryDocumentationPath(value string) bool {
	documentationDirectory := "do" + "cs"
	value = filepath.ToSlash(value)
	for strings.HasPrefix(value, "../") {
		value = strings.TrimPrefix(value, "../")
	}
	value = strings.TrimPrefix(value, "./")
	return value == documentationDirectory || strings.HasPrefix(value, documentationDirectory+"/")
}
