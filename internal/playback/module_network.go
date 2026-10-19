package playback

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/graysui/JMlab/internal/module"
	provider "github.com/graysui/JMlab/internal/stream"
)

func originKey(u *url.URL) string {
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return strings.ToLower(u.Scheme) + "://" + net.JoinHostPort(strings.ToLower(strings.TrimSuffix(u.Hostname(), ".")), port)
}

func allowedIP(ip net.IP, private bool) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() {
		return private
	}
	if !ip.IsGlobalUnicast() {
		return false
	}
	for _, block := range []string{"0.0.0.0/8", "100.64.0.0/10", "198.18.0.0/15", "240.0.0.0/4"} {
		_, network, _ := net.ParseCIDR(block)
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

func (s *Service) mediaPolicy(source sourceRecord) (*module.MediaAccess, error) {
	if s.modules == nil {
		return nil, nil
	}
	entry, err := s.sourceModule(source)
	if err != nil {
		return nil, err
	}
	if source.ProviderID != "" {
		return nil, nil
	}
	return entry.Manifest.MediaAccess, nil
}

func (s *Service) validateUpstreamURL(source sourceRecord, raw string) error {
	if source.ProviderID != "" {
		return provider.ValidateCandidateURL(raw, source.AllowedHosts)
	}
	if s.modules == nil {
		return errors.New("播放源模块尚未配置")
	}
	policy, err := s.mediaPolicy(source)
	if err != nil {
		return err
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("媒体地址格式无效")
	}
	matched := false
	if policy != nil && len(policy.AllowedOrigins) > 0 {
		for _, rawOrigin := range policy.AllowedOrigins {
			origin, err := url.Parse(rawOrigin)
			if err == nil && originKey(origin) == originKey(u) {
				matched = true
				break
			}
		}
		if !matched {
			return errors.New("媒体地址不在模块允许的来源中")
		}
	}
	if u.Scheme == "http" && !matched {
		return errors.New("HTTP 媒体来源需要明确授权")
	}
	private := policy != nil && policy.AllowPrivate && matched
	if ip := net.ParseIP(u.Hostname()); ip != nil && !allowedIP(ip, private) {
		return errors.New("媒体地址的网段未获授权")
	}
	if policy == nil || len(policy.AllowedOrigins) == 0 {
		return provider.ValidateCandidateURL(raw, nil)
	}
	return nil
}

type moduleResponseBody struct {
	io.ReadCloser
	transport *http.Transport
}

func (b *moduleResponseBody) Close() error {
	err := b.ReadCloser.Close()
	b.transport.CloseIdleConnections()
	return err
}

func (s *Service) fetchModuleStream(ctx context.Context, r *http.Request, source sourceRecord, target string) (*http.Response, error) {
	if err := s.validateUpstreamURL(source, target); err != nil {
		return nil, err
	}
	policy, err := s.mediaPolicy(source)
	if err != nil {
		return nil, err
	}
	headers, err := s.sourceHeaders(source)
	if err != nil {
		return nil, err
	}
	rootURL, err := s.sourceURL(source)
	if err != nil {
		return nil, err
	}
	root, _ := url.Parse(rootURL)
	targetURL, _ := url.Parse(target)
	request, err := http.NewRequestWithContext(ctx, r.Method, target, nil)
	if err != nil {
		return nil, errors.New("媒体请求创建失败")
	}
	for key, value := range headers {
		if originKey(root) == originKey(targetURL) || safeCrossOriginHeader(key) {
			request.Header.Set(key, value)
		}
	}
	if value := r.Header.Get("Range"); value != "" {
		if err := validateRange(value); err != nil {
			return nil, err
		}
		request.Header.Set("Range", value)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.ResponseHeaderTimeout = firstByteTimeout
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		lookupCtx, cancel := context.WithTimeout(ctx, dialTimeout)
		defer cancel()
		addresses, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
		if err != nil {
			return nil, errors.New("媒体主机解析失败")
		}
		private := policy != nil && policy.AllowPrivate && len(policy.AllowedOrigins) > 0
		for _, address := range addresses {
			if !allowedIP(address.IP, private) {
				return nil, errors.New("媒体主机解析到未授权网段")
			}
		}
		for _, address := range addresses {
			connection, err := (&net.Dialer{Timeout: dialTimeout}).DialContext(lookupCtx, network, net.JoinHostPort(address.IP.String(), port))
			if err == nil {
				return connection, nil
			}
		}
		return nil, errors.New("媒体主机连接失败")
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("媒体重定向次数过多")
		}
		if err := s.validateUpstreamURL(source, next.URL.String()); err != nil {
			return errors.New("媒体重定向未获授权")
		}
		if originKey(next.URL) != originKey(root) {
			for key := range next.Header {
				if !safeCrossOriginHeader(key) && !strings.EqualFold(key, "Range") {
					next.Header.Del(key)
				}
			}
		}
		return nil
	}}
	response, err := client.Do(request)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, errors.New("上游播放请求失败")
	}
	response.Body = &moduleResponseBody{ReadCloser: response.Body, transport: transport}
	return response, nil
}
func safeCrossOriginHeader(key string) bool {
	switch strings.ToLower(key) {
	case "accept", "user-agent", "referer", "origin":
		return true
	}
	return false
}
