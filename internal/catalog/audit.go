package catalog

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/graysui/JMlab/internal/auth"
)

type auditRecord struct {
	ID            string `json:"id"`
	ActorUserID   string `json:"actorUserId,omitempty"`
	ActorUsername string `json:"actorUsername,omitempty"`
	ActorName     string `json:"actorName,omitempty"`
	Action        string `json:"action"`
	ResourceType  string `json:"resourceType"`
	ResourceID    string `json:"resourceId,omitempty"`
	RequestID     string `json:"requestId,omitempty"`
	Result        string `json:"result"`
	Metadata      any    `json:"metadata"`
	CreatedAt     string `json:"createdAt"`
}

func (s *Service) listAuditLogs(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	queryValues := r.URL.Query()
	limit := parseTaskLimit(queryValues.Get("limit"), 50, 100)
	offset := parseTaskOffset(queryValues.Get("offset"))
	where := make([]string, 0, 8)
	args := make([]any, 0, 10)
	if !isAdmin(principal.User) {
		where = append(where, "a.actor_user_id = ?")
		args = append(args, principal.User.ID)
	}
	for _, filter := range []struct {
		name  string
		field string
	}{
		{name: "action", field: "a.action"},
		{name: "resourceType", field: "a.resource_type"},
		{name: "resourceId", field: "a.resource_id"},
		{name: "actorUserId", field: "a.actor_user_id"},
		{name: "result", field: "a.result"},
	} {
		value := strings.TrimSpace(queryValues.Get(filter.name))
		if value == "" {
			continue
		}
		if len([]rune(value)) > 160 {
			writeError(w, r, http.StatusBadRequest, "INVALID_AUDIT_FILTER", "审计筛选条件过长")
			return
		}
		where = append(where, filter.field+" = ?")
		args = append(args, value)
	}
	for _, filter := range []struct {
		name  string
		field string
	}{
		{name: "from", field: "a.created_at >= ?"},
		{name: "to", field: "a.created_at <= ?"},
	} {
		value := strings.TrimSpace(queryValues.Get(filter.name))
		if value == "" {
			continue
		}
		if _, err := time.Parse(time.RFC3339, value); err != nil {
			writeError(w, r, http.StatusBadRequest, "INVALID_AUDIT_FILTER", "时间筛选条件必须是 RFC3339")
			return
		}
		where = append(where, filter.field)
		args = append(args, value)
	}
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " WHERE " + strings.Join(where, " AND ")
	}
	baseQuery := " FROM audit_logs a LEFT JOIN users u ON u.id = a.actor_user_id"
	var total int
	if err := s.db.DB().QueryRowContext(r.Context(), s.db.Rebind("SELECT COUNT(*)"+baseQuery+whereSQL), args...).Scan(&total); err != nil {
		writeError(w, r, http.StatusInternalServerError, "AUDIT_LOGS_READ_FAILED", "读取审计日志失败")
		return
	}
	query := "SELECT a.id, COALESCE(a.actor_user_id, ''), COALESCE(u.username, ''), COALESCE(u.display_name, ''), a.action, a.resource_type, COALESCE(a.resource_id, ''), COALESCE(a.request_id, ''), a.result, COALESCE(a.metadata_json, '{}'), a.created_at" + baseQuery + whereSQL + " ORDER BY a.created_at DESC, a.id DESC LIMIT ? OFFSET ?"
	queryArgs := append(append([]any{}, args...), limit, offset)
	rows, err := s.db.DB().QueryContext(r.Context(), s.db.Rebind(query), queryArgs...)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "AUDIT_LOGS_READ_FAILED", "读取审计日志失败")
		return
	}
	items := make([]auditRecord, 0, limit)
	for rows.Next() {
		var item auditRecord
		var metadata string
		if err := rows.Scan(&item.ID, &item.ActorUserID, &item.ActorUsername, &item.ActorName, &item.Action, &item.ResourceType, &item.ResourceID, &item.RequestID, &item.Result, &metadata, &item.CreatedAt); err != nil {
			_ = rows.Close()
			writeError(w, r, http.StatusInternalServerError, "AUDIT_LOGS_READ_FAILED", "读取审计日志失败")
			return
		}
		item.Metadata = sanitizeAuditMetadata(metadata)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		writeError(w, r, http.StatusInternalServerError, "AUDIT_LOGS_READ_FAILED", "读取审计日志失败")
		return
	}
	if err := rows.Close(); err != nil {
		writeError(w, r, http.StatusInternalServerError, "AUDIT_LOGS_READ_FAILED", "读取审计日志失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}

func sanitizeAuditMetadata(raw string) any {
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return map[string]any{}
	}
	return sanitizeAuditValue(value)
}

func sanitizeAuditValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, child := range typed {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "password") || strings.Contains(lower, "passwd") || strings.Contains(lower, "token") || strings.Contains(lower, "cookie") || strings.Contains(lower, "authorization") || strings.Contains(lower, "secret") || strings.Contains(lower, "credential") || lower == "key" || strings.HasSuffix(lower, "_key") || strings.HasSuffix(lower, "apikey") || strings.HasSuffix(lower, "privatekey") || strings.HasSuffix(lower, "accesskey") {
				result[key] = "[已隐藏]"
				continue
			}
			result[key] = sanitizeAuditValue(child)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, child := range typed {
			result[index] = sanitizeAuditValue(child)
		}
		return result
	case string:
		return redactTaskText(typed)
	default:
		return value
	}
}
