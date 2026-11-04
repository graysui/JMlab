package stream

import "testing"

// 内置 Provider 的手工来源必须列出允许主机：空列表继续被拒绝。
// 这是有意的安全约束，修复模块媒体校验时不能放宽它。
func TestValidateCandidateURLStillRequiresAllowedHosts(t *testing.T) {
	for _, hosts := range [][]string{nil, {}} {
		if err := ValidateCandidateURL("https://cdn.example.com/a.m3u8", hosts); err == nil {
			t.Fatalf("空主机列表（%v）应当被拒绝", hosts)
		}
	}
	if err := ValidateCandidateURL("https://cdn.example.com/a.m3u8", []string{"cdn.example.com"}); err != nil {
		t.Fatalf("列出的主机应当通过: %v", err)
	}
	if err := ValidateCandidateURL("https://other.example.com/a.m3u8", []string{"cdn.example.com"}); err == nil {
		t.Fatal("不在列表中的主机应当被拒绝")
	}
}

// 模块未声明 mediaAccess 时，文档承诺“默认只允许公开 HTTPS 媒体”。
func TestValidatePublicHTTPSURLAllowsPublicHTTPS(t *testing.T) {
	for _, raw := range []string{
		"https://surrit.com/abc/playlist.m3u8",
		"https://austin-billion.mushroomtrack.com/hls/x/index.m3u8",
		"https://cdn.example.com:443/a.m3u8",
		"https://203.0.113.7/a.m3u8", // 公网 IP 字面量
		"  https://cdn.example.com/a.m3u8  ",
	} {
		if err := ValidatePublicHTTPSURL(raw); err != nil {
			t.Errorf("%q 应当通过: %v", raw, err)
		}
	}
}

func TestValidatePublicHTTPSURLRejectsUnsafeAddresses(t *testing.T) {
	for name, raw := range map[string]string{
		"http 明文":      "http://cdn.example.com/a.m3u8",
		"非 443 端口":     "https://cdn.example.com:8443/a.m3u8",
		"带用户信息":        "https://user:pw@cdn.example.com/a.m3u8",
		"localhost":    "https://localhost/a.m3u8",
		"子域 localhost": "https://x.localhost/a.m3u8",
		".local":       "https://nas.local/a.m3u8",
		".internal":    "https://svc.internal/a.m3u8",
		"回环 IP":        "https://127.0.0.1/a.m3u8",
		"私网 10.x":      "https://10.0.0.5/a.m3u8",
		"私网 192.168":   "https://192.168.1.1/a.m3u8",
		"链路本地/云元数据":    "https://169.254.169.254/latest/meta-data",
		"未指定地址":        "https://0.0.0.0/a.m3u8",
		"IPv6 回环":      "https://[::1]/a.m3u8",
		"空主机":          "https:///a.m3u8",
		"非 URL":        "not a url",
		"空串":           "",
		"file 协议":      "file:///etc/passwd",
		"ftp 协议":       "ftp://cdn.example.com/a",
	} {
		if err := ValidatePublicHTTPSURL(raw); err == nil {
			t.Errorf("%s（%q）应当被拒绝", name, raw)
		}
	}
}

// 公开 HTTPS 校验与带主机列表的校验共用同一套地址安全规则：
// 凡是带列表校验会拒绝的不安全地址，公开校验也必须拒绝，不能更宽松。
func TestValidatePublicHTTPSURLIsNeverLooserThanAllowListOnUnsafeAddresses(t *testing.T) {
	for _, raw := range []string{"https://127.0.0.1/a", "https://localhost/a", "http://cdn.example.com/a", "https://cdn.example.com:8443/a", "https://169.254.169.254/a"} {
		// 把目标主机本身放进允许列表，使“不在列表”不成为拒绝原因，只剩地址安全规则。
		withList := ValidateCandidateURL(raw, []string{"cdn.example.com"})
		public := ValidatePublicHTTPSURL(raw)
		if withList != nil && public == nil {
			t.Errorf("%q：带列表校验拒绝，公开校验却放行", raw)
		}
	}
}
