package arch_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aenigmma/webhookdispatcher/internal/arch"
)

func TestCheckRules(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		pkg     string
		imp     string
		wantBad bool
	}{
		{"домен и стандартная библиотека", "internal/application/entity", "errors", false},
		{"домен и uuid", "internal/application/entity", "github.com/google/uuid", false},
		{"домен и подпакет uuid", "internal/application/usecase", "github.com/google/uuid/dce", false},
		{"домен и соседний доменный пакет", "internal/application/usecase", arch.ModulePath + "/internal/application/errs", false},
		// net/http формально относится к стандартной библиотеке и правилом не
		// запрещён: транспорт в домене отсекается ревью, а не этой таблицей.
		{"домен и net/http", "internal/application/usecase", "net/http", false},
		{"домен и драйвер базы", "internal/application/entity", "github.com/jackc/pgx/v5", true},
		{"домен и адаптер", "internal/application/usecase", arch.ModulePath + "/internal/adapter/driven/postgres", true},
		{"домен и конфигурация", "internal/application/usecase", arch.ModulePath + "/internal/config", true},

		{"адаптер и стандартная библиотека", "internal/adapter/driver/http", "net/http", false},
		{"адаптер и домен", "internal/adapter/driver/http", arch.ModulePath + "/internal/application/errs", false},
		{"адаптер и он сам", "internal/adapter/driver/http", arch.ModulePath + "/internal/adapter/driver/http", false},
		{"driver и driven", "internal/adapter/driver/http", arch.ModulePath + "/internal/adapter/driven/postgres", true},
		{"driven и driver", "internal/adapter/driven/postgres", arch.ModulePath + "/internal/adapter/driver/worker", true},
		{"driven и соседний driven", "internal/adapter/driven/postgres", arch.ModulePath + "/internal/adapter/driven/httpsender", true},

		{"composition root и адаптер", "cmd/dispatcher", arch.ModulePath + "/internal/adapter/driven/postgres", false},
		{"composition root и конфигурация", "cmd/dispatcher", arch.ModulePath + "/internal/config", false},
		{"адаптер и конфигурация", "internal/adapter/driver/http", arch.ModulePath + "/internal/config", true},
		{"driven и конфигурация", "internal/adapter/driven/postgres", arch.ModulePath + "/internal/config", true},
		{"конфигурация и адаптер", "internal/config", arch.ModulePath + "/internal/adapter/driven/postgres", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := arch.Check(tc.pkg, tc.imp)
			if tc.wantBad && got == nil {
				t.Fatalf("ожидалось нарушение для пакета %s и импорта %s, его нет", tc.pkg, tc.imp)
			}
			if !tc.wantBad && got != nil {
				t.Fatalf("нарушение не ожидалось: %s импортирует %s — %s", got.Package, got.Import, got.Reason)
			}
		})
	}
}

// TestRepositoryRespectsLayerBoundaries — исполняемая версия архитектурного
// соглашения: нарушение направления зависимостей роняет сборку, а не ревью.
func TestRepositoryRespectsLayerBoundaries(t *testing.T) {
	root := moduleRoot(t)

	var violations []arch.Violation
	fset := token.NewFileSet()

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name != "." && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}

		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		pkg := path.Dir(filepath.ToSlash(rel))

		file, err := parser.ParseFile(fset, p, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if v := arch.Check(pkg, imported); v != nil {
				violations = append(violations, *v)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("обход дерева исходников: %v", err)
	}

	for _, v := range violations {
		t.Errorf("%s импортирует %s — %s", v.Package, v.Import, v.Reason)
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("текущий каталог: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod не найден выше текущего каталога")
		}
		dir = parent
	}
}
