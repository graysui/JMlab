package moduleadmin

import (
	"context"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/graysui/JMlab/internal/auth"
	"github.com/graysui/JMlab/internal/module"
)

func packageOwner(r *http.Request) string {
	principal, _ := auth.PrincipalFromContext(r.Context())
	return principal.User.ID
}

func (s *Service) inspectPackage(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(90 * time.Second))
	var input io.Reader
	var expected string
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType == "multipart/form-data" {
		r.Body = http.MaxBytesReader(w, r.Body, module.MaxPackageBytes+(1<<20))
		reader, err := r.MultipartReader()
		if err != nil {
			fail(w, r, 400, "INVALID_PACKAGE", "上传格式无效，请重新选择 ZIP 文件")
			return
		}
		part, err := reader.NextPart()
		if err != nil || part.FormName() != "file" {
			fail(w, r, 400, "INVALID_PACKAGE", "请选择 ZIP 模块包")
			return
		}
		defer part.Close()
		input = part
	} else if mediaType == "application/json" {
		var request struct {
			URL    string `json:"url"`
			SHA256 string `json:"sha256"`
		}
		if decode(w, r, &request) != nil {
			fail(w, r, 400, "INVALID_PACKAGE", "请填写模块 ZIP 下载地址")
			return
		}
		response, err := downloadPackage(r.Context(), request.URL, s.packageClient)
		if err != nil {
			fail(w, r, 400, "PACKAGE_DOWNLOAD_FAILED", err.Error())
			return
		}
		defer response.Body.Close()
		input, expected = response.Body, request.SHA256
	} else {
		fail(w, r, 415, "INVALID_PACKAGE", "请上传 ZIP 文件或提交下载网址")
		return
	}
	preview, err := s.manager.InspectPackage(r.Context(), packageOwner(r), input, expected)
	if err != nil {
		fail(w, r, 400, "INVALID_PACKAGE", err.Error())
		return
	}
	s.audit(r, "inspect_module_package", preview.Manifest.ID)
	respond(w, 200, preview)
}

func (s *Service) installPackage(w http.ResponseWriter, r *http.Request) {
	var input module.PackageInstallOptions
	if decode(w, r, &input) != nil {
		fail(w, r, 400, "INVALID_PACKAGE", "模块安装设置格式不正确")
		return
	}
	id, err := s.manager.InstallPackage(r.Context(), packageOwner(r), input)
	if err != nil {
		fail(w, r, 400, "PACKAGE_INSTALL_FAILED", err.Error())
		return
	}
	s.audit(r, "install_module_package", id)
	respond(w, 201, map[string]string{"status": "installed", "moduleId": id})
}

func (s *Service) cancelPackage(w http.ResponseWriter, r *http.Request) {
	s.manager.CancelPackage(packageOwner(r), chi.URLParam(r, "packageToken"))
	respond(w, 200, map[string]string{"status": "cancelled"})
}

func validDownloadURL(u *url.URL) bool {
	return u != nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil && u.Fragment == "" && !strings.HasSuffix(strings.ToLower(u.Hostname()), ".localhost") && !strings.EqualFold(u.Hostname(), "localhost")
}

// Pin each connection to resolved public IPs, also after redirects. Do not use
// environment proxies or forward host/session credentials to package providers.
func publicPackageIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() {
		return false
	}
	for _, raw := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001::/23", "2001:db8::/32", "2002::/16", "fc00::/7"} {
		if netip.MustParsePrefix(raw).Contains(addr) {
			return false
		}
	}
	return true
}

func packageHTTPClient() *http.Client {
	transport := &http.Transport{Proxy: nil, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 15 * time.Second, DisableKeepAlives: true}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, errors.New("下载地址无效")
		}
		lookupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		addresses, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
		if err != nil || len(addresses) == 0 {
			return nil, errors.New("无法解析模块下载地址")
		}
		for _, address := range addresses {
			if address.Zone != "" || !publicPackageIP(address.IP) {
				return nil, errors.New("下载地址必须指向公网服务器，不能访问本机或内网")
			}
		}
		for _, address := range addresses {
			connection, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(lookupCtx, network, net.JoinHostPort(address.IP.String(), port))
			if err == nil {
				return connection, nil
			}
		}
		return nil, errors.New("无法连接模块下载服务器")
	}
	return &http.Client{Transport: transport, Timeout: 55 * time.Second, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if len(via) > 5 || !validDownloadURL(next.URL) || (via[0].URL.Scheme == "https" && next.URL.Scheme != "https") {
			return errors.New("模块下载重定向无效")
		}
		next.Header.Del("Referer")
		return nil
	}}
}

func downloadPackage(ctx context.Context, raw string, client *http.Client) (*http.Response, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !validDownloadURL(u) {
		return nil, errors.New("请输入无账号密码、无片段的 HTTP/HTTPS 公网下载地址")
	}
	request, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, errors.New("下载地址无效")
	}
	request.Header.Set("Accept", "application/zip, application/octet-stream")
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("模块包下载失败，请检查公网地址、重定向或网络连接")
	}
	if response.StatusCode != 200 || response.ContentLength > module.MaxPackageBytes {
		response.Body.Close()
		return nil, errors.New("下载失败或模块包超过 64 MB，请使用 ZIP 文件直链")
	}
	return response, nil
}
