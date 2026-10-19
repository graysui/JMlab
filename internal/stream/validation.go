package stream

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

func ValidateCandidateURL(raw string, allowedHosts []string) error {
	hosts, err := normalizeAllowedHosts(allowedHosts)
	if err != nil {
		return err
	}
	return validatePublicHTTPSURL(raw, hosts)
}

func hostAllowed(host string, allowed []string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, candidate := range allowed {
		candidate = strings.ToLower(strings.TrimSuffix(candidate, "."))
		if host == candidate || strings.HasSuffix(host, "."+candidate) {
			return true
		}
	}
	return false
}

func validatePublicHTTPSURL(raw string, allowed []string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return errors.New("URL 必须是无用户信息的 HTTPS URL")
	}
	if port := parsed.Port(); port != "" && port != "443" {
		return errors.New("URL 只允许使用 HTTPS 默认端口 443")
	}
	if err := validateHostSafety(parsed.Hostname()); err != nil {
		return err
	}
	if len(allowed) > 0 && !hostAllowed(parsed.Hostname(), allowed) {
		return errors.New("URL 主机不在允许列表中")
	}
	return nil
}

func validateHostSafety(host string) error {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return fmt.Errorf("禁止使用本地主机名: %s", host)
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
			return fmt.Errorf("禁止使用回环、私网或链路本地地址: %s", host)
		}
	}
	return nil
}

func containsSensitiveQuery(query url.Values) bool {
	for key := range query {
		key = strings.ToLower(key)
		for _, sensitive := range []string{"token", "cookie", "authorization", "password", "passwd", "secret", "api_key", "apikey", "access_key"} {
			if strings.Contains(key, sensitive) {
				return true
			}
		}
	}
	return false
}

func normalizeAllowedHosts(raw []string) ([]string, error) {
	if len(raw) == 0 || len(raw) > 50 {
		return nil, errors.New("allowedHosts 必须包含 1 到 50 个主机名")
	}
	result := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, value := range raw {
		host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
		if host == "" || strings.ContainsAny(host, "/?#@ ") || strings.Contains(host, "*") {
			return nil, fmt.Errorf("allowedHosts 包含无效主机名: %s", value)
		}
		if strings.Contains(host, ":") && net.ParseIP(host) == nil {
			return nil, fmt.Errorf("allowedHosts 不允许包含端口: %s", value)
		}
		if err := validateHostSafety(host); err != nil {
			return nil, err
		}
		if _, exists := seen[host]; !exists {
			seen[host] = struct{}{}
			result = append(result, host)
		}
	}
	return result, nil
}
