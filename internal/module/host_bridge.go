package module

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
)

// HostBridge supplies credentials and a signed invocation context for callbacks.
// Tokens are never module configuration and must not be forwarded to media hosts.
type HostBridge interface {
	Headers(context.Context, *HTTPModuleClient) map[string]string
}

type callChainKey struct{}

func WithCallChain(ctx context.Context, chain []string) context.Context {
	return context.WithValue(ctx, callChainKey{}, append([]string(nil), chain...))
}
func CallChain(ctx context.Context) []string {
	chain, _ := ctx.Value(callChainKey{}).([]string)
	return append([]string(nil), chain...)
}

func (c *HTTPModuleClient) SetHostBridge(bridge HostBridge) error {
	if bridge != nil {
		u, _ := url.Parse(c.baseURL)
		if u.Scheme != "https" && !strings.EqualFold(u.Hostname(), "localhost") && !net.ParseIP(u.Hostname()).IsLoopback() {
			return errors.New("宿主反向服务令牌需要 HTTPS 或本机模块地址")
		}
	}
	c.hostBridge = bridge
	return nil
}

// Configure before loading external modules. No manager lock is held when a
// running client issues a callback or prepares its invocation headers.
func (m *Manager) SetHostBridge(bridge HostBridge) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hostBridge = bridge
}
