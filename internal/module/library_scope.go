package module

import (
	"errors"
	"strings"
)

// LibraryScope is a host policy for every stream module, independent of its
// private configSchema. A selected empty list deliberately matches nothing.
type LibraryScope struct {
	Mode       string   `json:"mode"`
	LibraryIDs []string `json:"libraryIds"`
}

func ValidateLibraryScope(manifest *Manifest, scope *LibraryScope) error {
	if scope == nil {
		return nil
	}
	if manifest.Type != TypeStream && manifest.Type != TypeComposite {
		return errors.New("只有播放源模块可以设置适用媒体库")
	}
	if scope.Mode != "all" && scope.Mode != "selected" {
		return errors.New("适用媒体库请选择全部或指定媒体库")
	}
	if len(scope.LibraryIDs) > 100 || scope.Mode == "all" && len(scope.LibraryIDs) > 0 {
		return errors.New("适用媒体库范围无效")
	}
	seen := map[string]bool{}
	for _, id := range scope.LibraryIDs {
		if strings.TrimSpace(id) != id || id == "" || len(id) > 200 || seen[id] {
			return errors.New("适用媒体库标识无效或重复")
		}
		seen[id] = true
	}
	return nil
}

func copyLibraryScope(scope *LibraryScope) *LibraryScope {
	if scope == nil {
		return nil
	}
	return &LibraryScope{Mode: scope.Mode, LibraryIDs: append([]string{}, scope.LibraryIDs...)}
}
