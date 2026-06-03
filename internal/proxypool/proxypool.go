package proxypool

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	defaultListURL         = "https://proxy.webshare.io/api/v2/proxy/list/?mode=direct&page=1&page_size=100"
	defaultRefreshInterval = 30 * time.Minute
)

type Proxy struct {
	ID                    string `json:"id"`
	Username              string `json:"username"`
	Password              string `json:"password"`
	ProxyAddress          string `json:"proxy_address"`
	Port                  int    `json:"port"`
	Valid                 bool   `json:"valid"`
	LastVerification      string `json:"last_verification,omitempty"`
	CountryCode           string `json:"country_code,omitempty"`
	CityName              string `json:"city_name,omitempty"`
	ASNName               string `json:"asn_name,omitempty"`
	ASNNumber             int    `json:"asn_number,omitempty"`
	HighCountryConfidence bool   `json:"high_country_confidence,omitempty"`
	CreatedAt             string `json:"created_at,omitempty"`
	ProxyURL              string `json:"proxy_url"`
}

type Pool struct {
	apiKey          string
	listURL         string
	refreshInterval time.Duration
	httpClient      *http.Client

	mu      sync.RWMutex
	proxies []Proxy
}

type responseBody struct {
	Results []Proxy `json:"results"`
}

var defaultPool = New("", "", defaultRefreshInterval)

func New(apiKey, listURL string, refreshInterval time.Duration) *Pool {
	listURL = strings.TrimSpace(listURL)
	if listURL == "" {
		listURL = defaultListURL
	}
	if refreshInterval <= 0 {
		refreshInterval = defaultRefreshInterval
	}
	return &Pool{
		apiKey:          strings.TrimSpace(apiKey),
		listURL:         listURL,
		refreshInterval: refreshInterval,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

func StartFromEnv(ctx context.Context) {
	apiKey := firstEnv("WEBSHARE_API_KEY", "webshare_api_key")
	if apiKey == "" {
		log.Debug("webshare proxy pool disabled: WEBSHARE_API_KEY is not configured")
		return
	}

	listURL := firstEnv("WEBSHARE_PROXY_LIST_URL", "webshare_proxy_list_url")
	refreshInterval := envDuration("WEBSHARE_PROXY_REFRESH_INTERVAL", defaultRefreshInterval)

	pool := New(apiKey, listURL, refreshInterval)
	defaultPool = pool
	pool.Start(ctx)
}

func Random() (Proxy, bool) {
	return defaultPool.Random()
}

func RandomProxyURL() (string, bool) {
	proxy, ok := Random()
	if !ok || strings.TrimSpace(proxy.ProxyURL) == "" {
		return "", false
	}
	return proxy.ProxyURL, true
}

func (p *Pool) Start(ctx context.Context) {
	if p == nil || p.apiKey == "" {
		return
	}

	go func() {
		p.refresh(ctx)

		ticker := time.NewTicker(p.refreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.refresh(ctx)
			}
		}
	}()
}

func (p *Pool) Random() (Proxy, bool) {
	if p == nil {
		return Proxy{}, false
	}
	p.mu.RLock()
	snapshot := append([]Proxy(nil), p.proxies...)
	p.mu.RUnlock()
	if len(snapshot) == 0 {
		return Proxy{}, false
	}

	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(snapshot))))
	if err != nil {
		return snapshot[0], true
	}
	return snapshot[int(n.Int64())], true
}

func (p *Pool) refresh(ctx context.Context) {
	proxies, err := p.fetch(ctx)
	if err != nil {
		log.WithError(err).Warn("failed to refresh webshare proxy pool")
		return
	}
	p.mu.Lock()
	p.proxies = proxies
	p.mu.Unlock()
	log.Infof("webshare proxy pool refreshed: %d proxies", len(proxies))
}

func (p *Pool) fetch(ctx context.Context) ([]Proxy, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.listURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create proxy list request: %w", err)
	}
	req.Header.Set("Authorization", "Token "+p.apiKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute proxy list request: %w", err)
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.WithError(errClose).Warn("failed to close webshare proxy response body")
		}
	}()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("proxy list request failed with status %d", resp.StatusCode)
	}

	var body responseBody
	if errDecode := json.NewDecoder(resp.Body).Decode(&body); errDecode != nil {
		return nil, fmt.Errorf("decode proxy list response: %w", errDecode)
	}

	proxies := make([]Proxy, 0, len(body.Results))
	for _, proxy := range body.Results {
		if !proxy.Valid {
			continue
		}
		proxy.Username = strings.TrimSpace(proxy.Username)
		proxy.Password = strings.TrimSpace(proxy.Password)
		proxy.ProxyAddress = strings.TrimSpace(proxy.ProxyAddress)
		if proxy.Username == "" || proxy.ProxyAddress == "" || proxy.Port <= 0 {
			continue
		}
		proxy.ProxyURL = buildProxyURL(proxy)
		if proxy.ProxyURL == "" {
			continue
		}
		proxies = append(proxies, proxy)
	}

	return proxies, nil
}

func buildProxyURL(proxy Proxy) string {
	host := net.JoinHostPort(proxy.ProxyAddress, strconv.Itoa(proxy.Port))
	u := &url.URL{
		Scheme: "http",
		User:   url.UserPassword(proxy.Username, proxy.Password),
		Host:   host,
		Path:   "/",
	}
	return u.String()
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err == nil && parsed > 0 {
		return parsed
	}
	if seconds, errAtoi := strconv.Atoi(value); errAtoi == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	log.Warnf("invalid %s value, using default interval", key)
	return fallback
}
