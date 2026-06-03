package auth

import "strings"

func ApplyProxyURLFromMetadata(auth *Auth) {
	if auth == nil || len(auth.Metadata) == 0 || strings.TrimSpace(auth.ProxyURL) != "" {
		return
	}
	for _, key := range []string{"proxy_url", "proxyUrl"} {
		raw, ok := auth.Metadata[key].(string)
		if !ok {
			continue
		}
		if proxyURL := strings.TrimSpace(raw); proxyURL != "" {
			auth.ProxyURL = proxyURL
			return
		}
	}
}
