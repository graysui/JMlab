package modulehost

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/graysui/JMlab/internal/module"
)

func (s *Service) invoke(w http.ResponseWriter, r *http.Request) {
	source := caller(r)
	claims, err := s.parseTicket(r.Header.Get("X-JMlab-Call-Ticket"), source)
	if err != nil {
		fail(w, 401, "INVALID_CALL_TICKET", "跨模块调用需要当前有效的调用凭据")
		return
	}
	var input struct {
		ModuleID string          `json:"moduleId"`
		Method   string          `json:"method"`
		Payload  json.RawMessage `json:"payload"`
	}
	if decode(w, r, &input) != nil || len(input.Payload) > 32*1024 {
		fail(w, 400, "INVALID_MODULE_CALL", "模块调用参数无效")
		return
	}
	allowed := false
	if source.Manifest.Permissions != nil {
		for _, grant := range source.Manifest.Permissions.Invoke {
			if grant.ModuleID == input.ModuleID {
				for _, method := range grant.Methods {
					if method == input.Method {
						allowed = true
					}
				}
			}
		}
	}
	if !allowed {
		fail(w, 403, "MODULE_CALL_NOT_GRANTED", "未授权调用此模块或方法")
		return
	}
	if len(claims.Chain) >= maximumDepth {
		fail(w, 409, "MODULE_CALL_DEPTH", "模块调用超过深度上限")
		return
	}
	for _, id := range claims.Chain {
		if id == input.ModuleID {
			fail(w, 409, "MODULE_CALL_CYCLE", "不允许循环调用模块")
			return
		}
	}
	target, ok := s.registry.Get(input.ModuleID)
	if !ok || target.Status != module.StatusActive {
		fail(w, 503, "TARGET_MODULE_UNAVAILABLE", "目标模块已停用或不可用")
		return
	}
	ctx, cancel := context.WithTimeout(module.WithCallChain(r.Context(), claims.Chain), 5*time.Second)
	defer cancel()
	result, err := dispatch(ctx, target, input.Method, input.Payload)
	if err != nil {
		fail(w, 502, "MODULE_CALL_FAILED", "目标模块未能完成调用")
		return
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > 8*1024*1024 {
		fail(w, 502, "MODULE_RESPONSE_INVALID", "目标模块响应无效或过大")
		return
	}
	respond(w, 200, json.RawMessage(encoded))
}
func dispatch(ctx context.Context, target *module.ModuleEntry, method string, payload json.RawMessage) (any, error) {
	var input struct {
		Query            string            `json:"query"`
		ItemType         module.ItemType   `json:"itemType"`
		Page             int               `json:"page"`
		PageSize         int               `json:"pageSize"`
		SourceID         string            `json:"sourceId"`
		ProviderIDs      map[string]string `json:"providerIds"`
		RankingType      string            `json:"rankingType"`
		Period           string            `json:"period"`
		ItemID           string            `json:"itemId"`
		Title            string            `json:"title"`
		Extra            map[string]any    `json:"extra"`
		CandidateID      string            `json:"candidateId"`
		EncryptedPayload string            `json:"encryptedPayload"`
		ClientHeaders    map[string]string `json:"clientHeaders"`
	}
	if len(payload) > 0 && json.Unmarshal(payload, &input) != nil {
		return nil, errors.New("invalid payload")
	}
	if input.Page < 1 {
		input.Page = 1
	}
	if input.Page > 500 {
		return nil, errors.New("invalid page")
	}
	if input.PageSize < 1 {
		input.PageSize = 20
	}
	if input.PageSize > 100 {
		input.PageSize = 100
	}
	meta, stream := target.MetadataInstance, target.StreamInstance
	switch method {
	case "caps":
		if meta != nil {
			return meta.Caps(ctx)
		}
		if caps, ok := stream.(interface {
			Caps(context.Context) (module.MetadataCaps, error)
		}); ok {
			return caps.Caps(ctx)
		}
	case "search":
		if meta != nil && strings.TrimSpace(input.Query) != "" && len(input.Query) <= 200 {
			items, total, err := meta.Search(ctx, input.Query, input.ItemType, input.Page, input.PageSize)
			return map[string]any{"items": items, "total": total}, err
		}
	case "detail":
		if meta != nil && input.SourceID != "" && len(input.SourceID) <= 256 {
			return meta.GetDetail(ctx, input.SourceID, input.ProviderIDs)
		}
	case "rankings":
		if meta != nil {
			return meta.GetRankings(ctx, input.RankingType, input.Period, input.Page)
		}
	case "discover":
		if stream != nil && input.ItemID != "" {
			items, err := stream.DiscoverSources(ctx, input.ItemID, input.Title, input.ItemType, input.ProviderIDs, input.Extra)
			return map[string]any{"candidates": items}, err
		}
	case "resolve":
		if stream != nil && input.CandidateID != "" {
			return stream.ResolveStream(ctx, input.CandidateID, input.EncryptedPayload, input.ClientHeaders)
		}
	case "probe":
		if stream != nil && input.CandidateID != "" {
			return stream.ProbeSource(ctx, input.CandidateID, input.EncryptedPayload)
		}
	}
	return nil, errors.New("unsupported method or payload")
}
