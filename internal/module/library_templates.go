package module

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// LibraryTemplate is owned by the module, and copied into a library when used.
// Scope binds the saved filter to this module's metadata or recorded streams.
type LibraryTemplate struct {
	ID            string                `json:"id"`
	Name          string                `json:"name"`
	Description   string                `json:"description,omitempty"`
	ItemType      string                `json:"itemType"`
	Scope         string                `json:"scope"`
	IsAdult       bool                  `json:"isAdult"`
	Filter        LibraryTemplateFilter `json:"filter"`
	Sort          LibraryTemplateSort   `json:"sort"`
	RankingScopes []LibraryRankingScope `json:"rankingScopes,omitempty"`
}

type LibraryTemplateFilter struct {
	MinRating         *float64 `json:"minRating,omitempty"`
	HasPlayableSource *bool    `json:"hasPlayableSource,omitempty"`
	IsFavorite        *bool    `json:"isFavorite,omitempty"`
	Years             []int    `json:"years,omitempty"`
}

type LibraryTemplateSort struct {
	Field     string `json:"field"`
	Direction string `json:"direction"`
}

type LibraryRankingScope struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	RankingType   string `json:"rankingType"`
	RankingPeriod string `json:"rankingPeriod"`
}

func (m *Manifest) validateLibraryTemplates() error {
	if len(m.LibraryTemplates) > 32 {
		return errors.New("每个模块最多声明 32 个虚拟库模板")
	}
	seen := map[string]bool{}
	for _, template := range m.LibraryTemplates {
		if !idRegex.MatchString(template.ID) || seen[template.ID] || strings.TrimSpace(template.Name) == "" || len(template.Name) > 200 || len(template.Description) > 2000 {
			return errors.New("虚拟库模板 ID、名称或说明无效，ID 不可重复")
		}
		seen[template.ID] = true
		if template.ItemType != "movie" && template.ItemType != "mixed" {
			return errors.New("虚拟库模板类型必须为 movie 或 mixed")
		}
		if (template.Scope != "metadata" && template.Scope != "stream") || (m.Type != TypeComposite && template.Scope != string(m.Type)) {
			return errors.New("虚拟库模板范围必须与模块类型匹配")
		}
		if rating := template.Filter.MinRating; rating != nil && (math.IsNaN(*rating) || math.IsInf(*rating, 0) || *rating < 0 || *rating > 10) {
			return errors.New("模板最低评分必须在 0 到 10 之间")
		}
		if len(template.Filter.Years) > 100 {
			return errors.New("模板年份过多")
		}
		for _, year := range template.Filter.Years {
			if year < 1900 || year > 2100 {
				return errors.New("模板年份必须在 1900 到 2100 之间")
			}
		}
		switch template.Sort.Field {
		case "title", "code", "year", "rating", "releaseDate", "updatedAt", "createdAt":
		default:
			return errors.New("模板排序字段无效")
		}
		if template.Sort.Direction != "asc" && template.Sort.Direction != "desc" {
			return errors.New("模板排序方向必须为 asc 或 desc")
		}
		if len(template.RankingScopes) > 128 {
			return errors.New("模板榜单范围过多")
		}
		scopes := map[string]bool{}
		for _, scope := range template.RankingScopes {
			if template.Scope != "metadata" {
				return errors.New("榜单范围需要元数据模块提供")
			}
			if !idRegex.MatchString(scope.ID) || scopes[scope.ID] || strings.TrimSpace(scope.Name) == "" || len(scope.Name) > 200 {
				return errors.New("模板榜单范围 ID 或名称无效")
			}
			scopes[scope.ID] = true

			if !ValidRankingValue(scope.RankingType) || !ValidRankingValue(scope.RankingPeriod) {
				return fmt.Errorf("模板榜单范围 %s 的类型或周期无效", scope.ID)
			}
			if len(m.Rankings) > 0 {
				if _, ok := m.Ranking(scope.RankingType, scope.RankingPeriod); !ok {
					return fmt.Errorf("模板榜单范围 %s 未在模块榜单中声明", scope.ID)
				}
			}
		}
	}
	return nil
}
