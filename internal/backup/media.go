package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/graysui/JMlab/internal/catalog"
)

func exportItemCode(item ExportItem) (string, error) {
	if item.Source == "javdb" || item.Source == "" {
		return catalog.NormalizeCode(item.Code)
	}
	if strings.TrimSpace(item.Code) == "" {
		return "media:" + item.ID, nil
	}
	return item.Code, nil
}

// Hierarchy is restored after every identity exists, regardless of export order.
func (s *Service) importMediaAttributes(ctx context.Context, tx *sql.Tx, doc ExportDocument, options importOptions, mapped importMapping) error {
	if strings.HasSuffix(doc.FormatVersion, ".export.v1") {
		return nil
	}
	types := map[string]string{}
	for _, item := range doc.Items {
		types[item.ID] = defaultString(item.ItemType, "movie")
	}
	for _, item := range doc.Items {
		if options.Mode == "merge" && !mapped.createdItems[item.ID] {
			continue
		}
		kind := types[item.ID]
		switch kind {
		case "movie", "video", "series", "season", "episode":
		default:
			return errors.New("不支持的媒体类型")
		}
		if item.ParentID != "" {
			parentType := types[item.ParentID]
			if mapped.items[item.ParentID] == "" || !((kind == "season" && parentType == "series") || (kind == "episode" && parentType == "season")) {
				return errors.New("媒体父子关系无效")
			}
		} else if kind == "season" || kind == "episode" {
			return errors.New("季或单集缺少父级")
		}
		if kind == "season" || kind == "episode" {
			if item.IndexNumber == nil || *item.IndexNumber < 0 {
				return errors.New("季或单集缺少有效序号")
			}
		}
		if item.ParentID == item.ID {
			return errors.New("媒体不能以自身为父级")
		}
		moduleID := item.MetadataModuleID
		if moduleID == "" {
			moduleID = item.Source
			if moduleID == "" || moduleID == "javdb" {
				moduleID = "reference-provider-javdb"
			}
		}
		_, err := tx.ExecContext(ctx, s.db.Rebind("UPDATE media_items SET item_type = ?, parent_id = ?, index_number = ?, provider_ids_json = ?, extra_attributes_json = ?, metadata_module_id = ? WHERE id = ?"), kind, nullable(mapped.items[item.ParentID]), item.IndexNumber, string(ensureJSON(item.ProviderIDs, "{}")), string(ensureJSON(item.ExtraAttributes, "{}")), moduleID, mapped.items[item.ID])
		if err != nil {
			return err
		}
	}
	return nil
}

func exportSourceURL(x ExportItem, code string) string {
	if x.SourceURL != "" {
		return x.SourceURL
	}
	if x.Source == "" || x.Source == "javdb" {
		return "https://javdb.com/search?q=" + code
	}
	return ""
}

func validateExportMedia(doc ExportDocument) error {
	if strings.HasSuffix(doc.FormatVersion, ".export.v1") {
		return nil
	}
	types := map[string]string{}
	for _, item := range doc.Items {
		types[item.ID] = defaultString(item.ItemType, "movie")
	}
	for _, item := range doc.Items {
		kind := types[item.ID]
		switch kind {
		case "movie", "video", "series":
			if item.ParentID != "" {
				return errors.New("顶层媒体不能设置父级")
			}
		case "season", "episode":
			if item.IndexNumber == nil || *item.IndexNumber < 0 || !((kind == "season" && types[item.ParentID] == "series") || (kind == "episode" && types[item.ParentID] == "season")) {
				return errors.New("季或单集父级和序号无效")
			}
		default:
			return errors.New("媒体类型无效")
		}
		var ids map[string]string
		var extra map[string]any
		if json.Unmarshal(ensureJSON(item.ProviderIDs, "{}"), &ids) != nil || json.Unmarshal(ensureJSON(item.ExtraAttributes, "{}"), &extra) != nil {
			return errors.New("媒体扩展信息必须是 JSON 对象")
		}
	}
	return nil
}
