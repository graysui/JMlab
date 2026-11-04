package playback

import (
	"testing"

	"github.com/graysui/JMlab/internal/module"
)

// registerPolicyModule 注册一个只带媒体授权声明的模块，返回指向它的源记录。
func registerPolicyModule(t *testing.T, s *Service, id string, access *module.MediaAccess) sourceRecord {
	t.Helper()
	entry := &module.ModuleEntry{
		Manifest:       &module.Manifest{ID: id, Name: id, Version: "1", Type: module.TypeStream, MediaAccess: access},
		Status:         module.StatusActive,
		StreamInstance: discoveryFixture{id: id},
	}
	if err := s.modules.Register(entry); err != nil {
		t.Fatal(err)
	}
	return sourceRecord{ModuleID: id}
}

// 宿主文档承诺“播放模块默认只允许公开 HTTPS 媒体”。未声明 mediaAccess 的模块，
// 公开 HTTPS 地址必须通过；此前会因空主机列表被一律拒绝，导致这类模块一个流都播不了。
func TestModuleWithoutMediaAccessAllowsPublicHTTPS(t *testing.T) {
	s, _ := discoveryService(t)
	source := registerPolicyModule(t, s, "no-access", nil)
	for _, raw := range []string{
		"https://surrit.com/uuid/playlist.m3u8",
		"https://austin-billion.mushroomtrack.com/hls/x/index.m3u8",
		"https://other-random-subdomain.mushroomtrack.com/hls/y/index.m3u8",
	} {
		if err := s.validateUpstreamURL(source, raw); err != nil {
			t.Errorf("%q 应当通过: %v", raw, err)
		}
	}
}

// 声明了 mediaAccess 但没有任何来源（allowedOrigins 为空）同样走默认策略。
func TestModuleWithEmptyAllowedOriginsAllowsPublicHTTPS(t *testing.T) {
	s, _ := discoveryService(t)
	source := registerPolicyModule(t, s, "empty-origins", &module.MediaAccess{})
	if err := s.validateUpstreamURL(source, "https://cdn.example.com/a.m3u8"); err != nil {
		t.Fatalf("空 allowedOrigins 应走默认公开 HTTPS 策略: %v", err)
	}
}

// 默认策略不能被放宽成“什么都放行”：明文 HTTP、特殊端口、私网、元数据地址仍然拒绝。
func TestModuleWithoutMediaAccessStillRejectsUnsafeURLs(t *testing.T) {
	s, _ := discoveryService(t)
	source := registerPolicyModule(t, s, "no-access", nil)
	for name, raw := range map[string]string{
		"明文 HTTP":    "http://cdn.example.com/a.m3u8",
		"非 443 端口":   "https://cdn.example.com:8443/a.m3u8",
		"回环 IP":      "https://127.0.0.1/a.m3u8",
		"localhost":  "https://localhost/a.m3u8",
		"私网 10.x":    "https://10.0.0.5/a.m3u8",
		"私网 192.168": "https://192.168.1.1/a.m3u8",
		"云元数据":       "https://169.254.169.254/latest/meta-data",
		"带用户信息":      "https://user:pw@cdn.example.com/a.m3u8",
		"含片段":        "https://cdn.example.com/a.m3u8#frag",
		"非 HTTP 协议":  "ftp://cdn.example.com/a",
		"file 协议":    "file:///etc/passwd",
		"空地址":        "",
	} {
		if err := s.validateUpstreamURL(source, raw); err == nil {
			t.Errorf("%s（%q）应当被拒绝", name, raw)
		}
	}
}

// 声明了 allowedOrigins 的模块行为保持不变：只有精确匹配的来源通过。
func TestModuleWithAllowedOriginsKeepsExactMatching(t *testing.T) {
	s, _ := discoveryService(t)
	source := registerPolicyModule(t, s, "exact", &module.MediaAccess{AllowedOrigins: []string{"https://surrit.com"}})
	if err := s.validateUpstreamURL(source, "https://surrit.com/a.m3u8"); err != nil {
		t.Errorf("授权的来源应通过: %v", err)
	}
	if err := s.validateUpstreamURL(source, "https://cdn.example.com/a.m3u8"); err == nil {
		t.Error("声明了 allowedOrigins 后，其他公开 HTTPS 来源必须被拒绝")
	}
	if err := s.validateUpstreamURL(source, "https://sub.surrit.com/a.m3u8"); err == nil {
		t.Error("allowedOrigins 是精确匹配，子域不应通过")
	}
}

// 私网与 HTTP 仍然只能通过 allowedOrigins + allowPrivate 明确授权，默认策略不放行它们。
func TestPrivateAndHTTPStillNeedExplicitAuthorization(t *testing.T) {
	s, _ := discoveryService(t)
	plain := registerPolicyModule(t, s, "plain", nil)
	if err := s.validateUpstreamURL(plain, "http://192.168.1.5:5244/a.mp4"); err == nil {
		t.Error("未授权的私网 HTTP 地址必须被拒绝")
	}
	granted := registerPolicyModule(t, s, "granted", &module.MediaAccess{AllowedOrigins: []string{"http://192.168.1.5:5244"}, AllowPrivate: true})
	if err := s.validateUpstreamURL(granted, "http://192.168.1.5:5244/a.mp4"); err != nil {
		t.Errorf("明确授权的私网来源应通过: %v", err)
	}
	httpOnly := registerPolicyModule(t, s, "http-only", &module.MediaAccess{AllowedOrigins: []string{"http://media.example.com"}})
	if err := s.validateUpstreamURL(httpOnly, "http://media.example.com/a.mp4"); err != nil {
		t.Errorf("明确授权的 HTTP 来源应通过: %v", err)
	}
}
