package twitch

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestDomainPackageImportsNoTransport enforces the boundary this package
// exists to draw.
//
// internal/twitch is the model internal/render and internal/app are written
// against. The moment it imports an HTTP client, an IRC library or an OAuth
// exchange, every one of those packages inherits the dependency, drawing a
// chat line starts requiring Twitch's transport machinery to compile, and the
// adapters stop being replaceable. That is exactly the state this package was
// split out of, and a dependency creeps back in far more easily than it is
// removed, so it is checked rather than trusted.
//
// Adapters belong in twitch/helix and twitch/irc, which import this package
// and not the reverse.
func TestDomainPackageImportsNoTransport(t *testing.T) {
	banned := map[string]string{
		"net/http":                           "HTTP belongs in the helix adapter",
		"github.com/gempir/go-twitch-irc/v4": "the IRC library belongs in the irc adapter",
		"encoding/json":                      "wire decoding belongs in the adapters",
		"net":                                "networking belongs in the adapters",
	}
	bannedPrefixes := []string{
		"github.com/worxbend/twi/internal/twitch/", // no depending on our own adapters
	}

	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list package files: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no Go files found; this test would pass vacuously")
	}

	fset := token.NewFileSet()
	checked := 0
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		checked++
		for _, spec := range file.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("%s: bad import path %s", path, spec.Path.Value)
			}
			if why, bad := banned[imported]; bad {
				t.Errorf("%s imports %q: %s", filepath.Base(path), imported, why)
			}
			for _, prefix := range bannedPrefixes {
				if strings.HasPrefix(imported, prefix) {
					t.Errorf("%s imports %q: dependencies must point inward, so an adapter "+
						"may import this package but never the reverse",
						filepath.Base(path), imported)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no non-test Go files checked; this test would pass vacuously")
	}
}
