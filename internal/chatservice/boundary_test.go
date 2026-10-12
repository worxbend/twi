package chatservice

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRuntimeHasNoUIImports(t *testing.T) {
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range files {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(".", entry.Name()), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range file.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(path, "bubbletea") || strings.Contains(path, "lipgloss") || strings.HasSuffix(path, "/internal/app") || strings.HasSuffix(path, "/internal/cli") || strings.HasSuffix(path, "/internal/render") {
				t.Errorf("runtime imports UI: %s %s", entry.Name(), path)
			}
		}
	}
}
