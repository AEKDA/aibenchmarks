// Package arch описывает направление зависимостей между слоями проекта.
//
// Правила вынесены в чистую функцию, чтобы их можно было проверить таблицей, а
// обход дерева исходников оставался тонкой обёрткой над ней.
package arch

import "strings"

// ModulePath — путь модуля; используется для отличия внутренних импортов от внешних.
const ModulePath = "github.com/aenigmma/webhookdispatcher"

const (
	applicationPrefix = "internal/application"
	adapterPrefix     = "internal/adapter/"
	configPrefix      = "internal/config"
	cmdPrefix         = "cmd/"
)

// allowedInApplication перечисляет внешние модули, разрешённые слою домена.
var allowedInApplication = []string{"github.com/google/uuid"}

// Violation описывает одно нарушение направления зависимостей.
type Violation struct {
	Package string
	Import  string
	Reason  string
}

// IsStdlib сообщает, принадлежит ли импорт стандартной библиотеке.
//
// Признак — отсутствие точки в первом сегменте пути: у внешних модулей там
// всегда доменное имя.
func IsStdlib(importPath string) bool {
	first, _, _ := strings.Cut(importPath, "/")
	return !strings.Contains(first, ".")
}

// Check проверяет один импорт одного пакета.
//
// pkg — путь пакета относительно корня модуля (например internal/application/errs).
// Возвращает nil, если импорт допустим.
func Check(pkg, importPath string) *Violation {
	deny := func(reason string) *Violation {
		return &Violation{Package: pkg, Import: importPath, Reason: reason}
	}

	internal, isInternal := strings.CutPrefix(importPath, ModulePath+"/")

	// Окружение читается только в composition root: любой другой слой не должен
	// тянуть конфигурацию (а вслед за ней и зависимость от окружения).
	if isInternal && (internal == configPrefix || strings.HasPrefix(internal, configPrefix+"/")) &&
		!strings.HasPrefix(pkg, cmdPrefix) && !strings.HasPrefix(pkg, configPrefix) {
		return deny("окружение читается только в composition root (" + cmdPrefix + ")")
	}

	switch {
	case pkg == applicationPrefix || strings.HasPrefix(pkg, applicationPrefix+"/"):
		if isInternal && (internal == applicationPrefix || strings.HasPrefix(internal, applicationPrefix+"/")) {
			return nil
		}
		if IsStdlib(importPath) {
			return nil
		}
		for _, allowed := range allowedInApplication {
			if importPath == allowed || strings.HasPrefix(importPath, allowed+"/") {
				return nil
			}
		}
		return deny("слой application импортирует только стандартную библиотеку и " +
			strings.Join(allowedInApplication, ", "))

	case strings.HasPrefix(pkg, adapterPrefix):
		if !isInternal || !strings.HasPrefix(internal, adapterPrefix) {
			return nil
		}
		if internal == pkg {
			return nil
		}
		return deny("адаптеры не зависят друг от друга напрямую")

	default:
		if isInternal && strings.HasPrefix(internal, adapterPrefix) && !strings.HasPrefix(pkg, cmdPrefix) {
			return deny("адаптеры связываются только в composition root (" + cmdPrefix + ")")
		}
		return nil
	}
}
