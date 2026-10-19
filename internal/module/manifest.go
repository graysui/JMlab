package module

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
)

var idRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{2,62}$`)

// TransportType 模块通信信道类型
type TransportType string

const (
	TransportInProcess TransportType = "in_process" // 进程内原生集成（如官方核心参考模块）
	TransportHTTP      TransportType = "http"       // HTTP/JSON-RPC (REST Webhook / Service)
	TransportGRPC      TransportType = "grpc"       // gRPC (TCP / Unix Socket)
)

// TransportConfig 通信连接配置
type TransportConfig struct {
	Type   TransportType `json:"type"`             // in_process, http, grpc
	Socket string        `json:"socket,omitempty"` // unix socket 路径（Linux/macOS）或 named pipe（Windows）
	URL    string        `json:"url,omitempty"`    // http://127.0.0.1:8088
}

// ConfigField 模块在管理后台动态呈现的配置表单字段定义
type ConfigField struct {
	Options     []FieldOption `json:"options,omitempty"`
	Type        string        `json:"type"`                  // string, number, boolean, password, json
	Label       string        `json:"label"`                 // 显示名
	Description string        `json:"description,omitempty"` // 描述
	Required    bool          `json:"required"`              // 是否必填
	Secret      bool          `json:"secret"`                // 是否为密码/敏感凭据
	Default     any           `json:"default,omitempty"`     // 默认值
}

// Manifest 模块声明清单
type Manifest struct {
	Rankings           []RankingDefinition    `json:"rankings,omitempty"`
	Actions            []ActionDescriptor     `json:"actions,omitempty"`
	Permissions        *HostPermissions       `json:"permissions,omitempty"`
	MediaAccess        *MediaAccess           `json:"mediaAccess,omitempty"`
	ID                 string                 `json:"id"`
	Name               string                 `json:"name"`
	Version            string                 `json:"version"`
	Description        string                 `json:"description,omitempty"`
	Author             string                 `json:"author,omitempty"`
	Type               ModuleType             `json:"type"` // metadata, stream, composite
	SupportedItemTypes []ItemType             `json:"supportedItemTypes,omitempty"`
	Transport          TransportConfig        `json:"transport"`
	Entrypoint         string                 `json:"entrypoint,omitempty"` // 模块独立运行入口脚本/可执行文件名
	ConfigSchema       map[string]ConfigField `json:"configSchema,omitempty"`
	Subscriptions      []string               `json:"subscriptions,omitempty"` // 声明订阅的事件主题
	LibraryTemplates   []LibraryTemplate      `json:"libraryTemplates,omitempty"`
	Directory          string                 `json:"-"` // 所在目录物理绝对路径
}

type HostPermissions struct {
	Storage bool               `json:"storage,omitempty"`
	Invoke  []InvokePermission `json:"invoke,omitempty"`
}

type InvokePermission struct {
	ModuleID string   `json:"moduleId"`
	Methods  []string `json:"methods"`
}

// MediaAccess authorizes exact media origins independently of the module's RPC address.
type MediaAccess struct {
	AllowedOrigins []string `json:"allowedOrigins,omitempty"`
	AllowPrivate   bool     `json:"allowPrivate,omitempty"`
}

// Validate 检查 Manifest 字段是否合法
func (m *Manifest) Validate() error {
	if m == nil {
		return errors.New("module manifest is required")
	}
	if !idRegex.MatchString(m.ID) {
		return fmt.Errorf("invalid module id %q: must match [a-z0-9-_]{3,63}", m.ID)
	}
	if m.Permissions != nil {
		if len(m.Permissions.Invoke) > 16 {
			return errors.New("模块调用授权过多")
		}
		for _, permission := range m.Permissions.Invoke {
			if !idRegex.MatchString(permission.ModuleID) || permission.ModuleID == m.ID {
				return errors.New("跨模块授权目标无效")
			}
			for _, method := range permission.Methods {
				switch method {
				case "caps", "search", "detail", "rankings", "discover", "resolve", "probe":
				default:
					return errors.New("跨模块调用方法无效")
				}
			}
		}
	}
	if len(m.Subscriptions) > 16 {
		return errors.New("模块事件订阅过多")
	}
	for _, topic := range m.Subscriptions {
		if !eventTopics[topic] {
			return errors.New("不支持的模块事件主题")
		}
	}
	if m.Name == "" {
		return errors.New("module name is required")
	}
	if err := m.validateActions(); err != nil {
		return err
	}
	if err := m.validateRankings(); err != nil {
		return err
	}
	if err := m.validateLibraryTemplates(); err != nil {
		return err
	}
	if m.Version == "" {
		return errors.New("module version is required")
	}
	switch m.Type {
	case TypeMetadata, TypeStream, TypeComposite:
	default:
		return fmt.Errorf("unsupported module type %q", m.Type)
	}
	switch m.Transport.Type {
	case TransportInProcess, TransportHTTP, TransportGRPC:
	default:
		return fmt.Errorf("unsupported transport type %q", m.Transport.Type)
	}
	if m.Transport.Type == TransportHTTP {
		endpoint, err := url.Parse(m.Transport.URL)
		if err != nil || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" ||
			(endpoint.Scheme != "http" && endpoint.Scheme != "https") {
			return errors.New("模块地址必须是无凭证、查询串和片段的 HTTP/HTTPS 地址")
		}
	}
	for name, field := range m.ConfigSchema {
		if name == "" || len(name) > 100 {
			return errors.New("invalid configuration field name")
		}
		switch field.Type {
		case "string", "number", "boolean", "password", "json":
		default:
			return fmt.Errorf("unsupported config field type %q", field.Type)
		}
		if len(field.Options) > 128 || len(field.Options) > 0 && field.Type != "string" {
			return errors.New("配置选项仅支持字符串且不能超过 128 个")
		}
		seen := map[string]bool{}
		for _, option := range field.Options {
			if option.Value == "" || option.Label == "" || seen[option.Value] {
				return errors.New("配置选项的值和名称不能为空或重复")
			}
			seen[option.Value] = true
		}
	}
	if m.MediaAccess != nil {
		if m.MediaAccess.AllowPrivate && len(m.MediaAccess.AllowedOrigins) == 0 {
			return errors.New("私网播放源必须明确配置允许的来源地址")
		}
		for _, raw := range m.MediaAccess.AllowedOrigins {
			origin, err := url.Parse(raw)
			if err != nil || origin.Hostname() == "" || origin.User != nil || (origin.Path != "" && origin.Path != "/") || origin.RawQuery != "" || origin.Fragment != "" || (origin.Scheme != "http" && origin.Scheme != "https") {
				return errors.New("媒体来源地址只能包含 HTTP/HTTPS 协议、主机和端口")
			}
		}
	}
	return nil
}

// LoadManifestFromDir 从模块所在目录读取并解析 manifest.json
func LoadManifestFromDir(dirPath string) (*Manifest, error) {
	manifestPath := filepath.Join(dirPath, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest.json: %w", err)
	}

	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("failed to parse manifest.json: %w", err)
	}
	m.Directory = dirPath

	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}
