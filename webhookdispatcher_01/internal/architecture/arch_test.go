package architecture_test

import (
	"os/exec"
	"strings"
	"testing"
)

const (
	modulePath      = "github.com/example/webhookdispatcher"
	applicationPath = modulePath + "/internal/application"
	adapterPath     = modulePath + "/internal/adapter"
)

// deps lists the transitive import graph of the given package pattern.
func deps(t *testing.T, pattern string) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", pattern)
	cmd.Dir = "../.." // module root
	// Stdout only: warnings such as "matched no packages" go to stderr.
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list %s: %v", pattern, err)
	}
	var pkgs []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			pkgs = append(pkgs, line)
		}
	}
	return pkgs
}

// isStdlib reports whether an import path belongs to the standard library.
// Standard library paths never carry a dot in their first segment.
func isStdlib(pkg string) bool {
	first, _, _ := strings.Cut(pkg, "/")
	return !strings.Contains(first, ".")
}

// TestApplicationImportsOnlyStdlibAndUUID enforces the strict hexagonal rule:
// the domain layer may depend on the standard library and github.com/google/uuid
// only. Adding any other import to internal/application fails this test.
func TestApplicationImportsOnlyStdlibAndUUID(t *testing.T) {
	allowed := map[string]bool{"github.com/google/uuid": true}
	for _, pkg := range deps(t, "./internal/application/...") {
		switch {
		case isStdlib(pkg):
		case allowed[pkg]:
		case strings.HasPrefix(pkg, applicationPath):
		default:
			t.Errorf("internal/application must not depend on %s", pkg)
		}
	}
}

// TestAdaptersDoNotDependOnEachOther enforces that adapters talk to the domain
// only, never to a sibling adapter.
func TestAdaptersDoNotDependOnEachOther(t *testing.T) {
	cmd := exec.Command("go", "list", "-f", "{{.ImportPath}}", "./internal/adapter/...")
	cmd.Dir = "../.."
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list adapters: %v", err)
	}
	adapters := strings.Fields(string(out))
	if len(adapters) == 0 {
		t.Skip("no adapter packages yet")
	}
	for _, adapter := range adapters {
		for _, dep := range deps(t, adapter) {
			if strings.HasPrefix(dep, adapterPath) && dep != adapter {
				t.Errorf("adapter %s must not depend on adapter %s", adapter, dep)
			}
		}
	}
}
