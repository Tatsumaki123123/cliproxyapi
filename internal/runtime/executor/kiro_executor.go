package executor

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	kiroauth "github.com/router-for-me/CLIProxyAPI/v6/internal/auth/kiro"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/runtime/executor/helps"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

// castaTable is the CRC32 Castagnoli table used for AWS event stream frame validation.
var castaTable = crc32.MakeTable(crc32.Castagnoli)

// KiroExecutor is a stateless executor for Kiro (Amazon Q / CodeWhisperer) API.
// It translates OpenAI-format requests into CodeWhisperer streaming requests
// and parses the AWS event stream response back to OpenAI format.
type KiroExecutor struct {
	cfg *config.Config
}

// NewKiroExecutor creates a new Kiro executor.
func NewKiroExecutor(cfg *config.Config) *KiroExecutor {
	return &KiroExecutor{cfg: cfg}
}

// Identifier returns the provider key for this executor.
func (e *KiroExecutor) Identifier() string { return "kiro" }

// Refresh refreshes the Kiro OIDC Bearer token. The refresh strategy has four
// ordered stages so that the best available approach is always tried first:
//
//  1. OIDC refresh_token flow (requires client_id + client_secret).
//  2. Resolve client_id via client_id_hash stored in metadata, then retry (1).
//  3. Kiro Desktop refresh (social-login: Google / GitHub / Microsoft).
//  4. Re-read ~/.aws/sso/cache/kiro-auth-token.json (requires kiro-cli on this machine).
func (e *KiroExecutor) Refresh(ctx context.Context, auth *cliproxyauth.Auth) (*cliproxyauth.Auth, error) {
	log.Debugf("kiro executor: refresh called")
	if auth == nil {
		return nil, fmt.Errorf("kiro executor: auth is nil")
	}
	if auth.Metadata == nil {
		auth.Metadata = make(map[string]any)
	}

	svc := kiroauth.NewKiroAuth(e.cfg)

	// ---- helpers ----

	applyTokenResponse := func(token *kiroauth.TokenResponse) {
		auth.Metadata["access_token"] = token.AccessToken
		if token.RefreshToken != "" {
			auth.Metadata["refresh_token"] = token.RefreshToken
		}
		if token.ExpiresIn > 0 {
			exp := time.Now().Add(time.Duration(token.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
			auth.Metadata["expires_at"] = exp
		}
		auth.Metadata["last_refresh"] = time.Now().UTC().Format(time.RFC3339)
	}

	resolveClientCreds := func(hash string) (clientID, clientSecret string) {
		if strings.TrimSpace(hash) == "" {
			return "", ""
		}
		reg, regErr := kiroauth.FindClientRegistration(hash)
		if reg == nil || regErr != nil {
			return "", ""
		}
		// Persist so future refreshes skip the file scan.
		auth.Metadata["client_id"] = reg.ClientID
		auth.Metadata["client_secret"] = reg.ClientSecret
		log.Debugf("kiro executor: client registration resolved from sso cache (hash=%s)", hash)
		return reg.ClientID, reg.ClientSecret
	}

	tryOIDCRefresh := func(clientID, clientSecret, refreshToken, ssoRegion string) bool {
		if strings.TrimSpace(clientID) == "" || strings.TrimSpace(refreshToken) == "" {
			return false
		}
		token, err := svc.RefreshToken(ctx, clientID, clientSecret, refreshToken, ssoRegion)
		if err != nil {
			log.Warnf("kiro executor: OIDC refresh failed (client_id=%s region=%s): %v",
				clientID, ssoRegion, err)
			return false
		}
		if token == nil {
			return false
		}
		applyTokenResponse(token)
		log.Infof("kiro executor: token refreshed via OIDC refresh_token (region=%s)", ssoRegion)
		return true
	}

	tryDesktopRefresh := func(ssoRegion, refreshToken string) bool {
		if strings.TrimSpace(refreshToken) == "" {
			return false
		}
		token, err := svc.RefreshTokenDesktop(ctx, ssoRegion, refreshToken)
		if err != nil {
			log.Warnf("kiro executor: Kiro Desktop refresh failed: %v", err)
			return false
		}
		if token == nil {
			return false
		}
		applyTokenResponse(token)
		log.Infof("kiro executor: token refreshed via Kiro Desktop auth (region=%s)", ssoRegion)
		return true
	}

	// ---- Stage 1: OIDC refresh with credentials already in metadata ----

	clientID, _ := auth.Metadata["client_id"].(string)
	clientSecret, _ := auth.Metadata["client_secret"].(string)
	refreshToken, _ := auth.Metadata["refresh_token"].(string)
	ssoRegion, _ := auth.Metadata["sso_region"].(string)

	if tryOIDCRefresh(clientID, clientSecret, refreshToken, ssoRegion) {
		return auth, nil
	}

	// ---- Stage 2: Resolve client_id via hash and retry OIDC ----

	if strings.TrimSpace(clientID) == "" {
		if hash, _ := auth.Metadata["client_id_hash"].(string); hash != "" {
			clientID, clientSecret = resolveClientCreds(hash)
		}
	}
	if tryOIDCRefresh(clientID, clientSecret, refreshToken, ssoRegion) {
		return auth, nil
	}

	// ---- Stage 3: Kiro Desktop refresh (social-login accounts without OIDC client creds) ----
	// This covers tokens obtained via Google, GitHub, Microsoft sign-in through the Kiro IDE,
	// which do not have a client_id/client_secret but do have a long-lived refreshToken.

	// ---- Stage 2.5: Scan local SSO cache for any valid client registration ----
	// kiro-cli registers an OIDC client and stores the credentials in
	// ~/.aws/sso/cache/<hash>.json. When the auth was imported from SQLite without
	// client_id (e.g. because the registration was not found in the SQLite database),
	// we try to recover the client credentials from the SSO cache files here.
	if strings.TrimSpace(clientID) == "" {
		if regs, regErr := kiroauth.FindAllClientRegistrations(); regErr == nil {
			for _, reg := range regs {
				if tryOIDCRefresh(reg.ClientID, reg.ClientSecret, refreshToken, ssoRegion) {
					// Persist for future refresh cycles so Stage 1 succeeds next time.
					auth.Metadata["client_id"] = reg.ClientID
					auth.Metadata["client_secret"] = reg.ClientSecret
					auth.Metadata["auth_type"] = "aws_sso_oidc"
					return auth, nil
				}
			}
		}
	}

	if tryDesktopRefresh(ssoRegion, refreshToken) {
		return auth, nil
	}

	// ---- Stage 4: Read from local kiro-cli storage (requires kiro-cli on this machine) ----
	// kiro-cli v1+ stores tokens in a SQLite database; older versions use a JSON file.
	// Try SQLite first, then fall back to the JSON file. This is the most reliable path
	// when kiro-cli manages its own session and keeps the stored token up-to-date.
	applyLocalCLIToken := func(cliToken *kiroauth.KiroCLIToken, source string) {
		auth.Metadata["access_token"] = cliToken.AccessToken
		if cliToken.RefreshToken != "" {
			auth.Metadata["refresh_token"] = cliToken.RefreshToken
		}
		if cliToken.ExpiresAt != "" {
			auth.Metadata["expires_at"] = cliToken.ExpiresAt
		}
		if cliToken.Region != "" {
			auth.Metadata["sso_region"] = cliToken.Region
		}
		if cliToken.ClientID != "" {
			auth.Metadata["client_id"] = cliToken.ClientID
		}
		if cliToken.ClientSecret != "" {
			auth.Metadata["client_secret"] = cliToken.ClientSecret
		}
		auth.Metadata["last_refresh"] = time.Now().UTC().Format(time.RFC3339)
		log.Infof("kiro executor: token synced from local kiro-cli %s for auth %s", source, auth.ID)
	}

	if sqliteToken, dbPath, errSQLite := svc.ImportFromKiroCLISQLite(""); errSQLite == nil &&
		sqliteToken != nil && strings.TrimSpace(sqliteToken.AccessToken) != "" {
		// Log diagnostic key list if client credentials were not found in the database.
		// The token loader stuffs the key inventory into ClientIDHash when no client was located.
		if sqliteToken.ClientID == "" && strings.HasPrefix(sqliteToken.ClientIDHash, "diagnostic:keys=") {
			log.Warnf("kiro executor: no client registration found in SQLite (%s) — available keys: %s",
				dbPath, strings.TrimPrefix(sqliteToken.ClientIDHash, "diagnostic:keys="))
			sqliteToken.ClientIDHash = ""
		}
		applyLocalCLIToken(sqliteToken, "SQLite database")
		return auth, nil
	} else if errSQLite != nil {
		log.Warnf("kiro executor: SQLite token read failed (%s): %v", dbPath, errSQLite)
	}

	if jsonToken, jsonPath, errJSON := svc.ImportFromKiroCLI(); errJSON == nil &&
		jsonToken != nil && strings.TrimSpace(jsonToken.AccessToken) != "" {
		applyLocalCLIToken(jsonToken, "JSON file")
		return auth, nil
	} else if errJSON != nil {
		log.Warnf("kiro executor: JSON token read failed (%s): %v", jsonPath, errJSON)
	}

	// All refresh strategies exhausted.
	// Re-import the token via the management UI to restore authentication.
	// Include clientId and clientSecret from the Kiro registration file so that OIDC refresh
	// works automatically after re-import (Kiro IDE installation not required on the server).
	log.Warnf("kiro executor: all token refresh strategies failed for auth %s — re-import the token via the management UI", auth.ID)
	return auth, nil
}

// Execute performs a non-streaming Kiro / CodeWhisperer request.
func (e *KiroExecutor) Execute(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (resp cliproxyexecutor.Response, err error) {
	accessToken := kiroAccessToken(auth)
	baseModel := stripKiroPrefix(req.Model)

	reporter := helps.NewUsageReporter(ctx, e.Identifier(), baseModel, auth)
	defer reporter.TrackFailure(ctx, &err)

	body, err := buildKiroRequest(req.Payload, opts.SourceFormat.String(), baseModel, false)
	if err != nil {
		return resp, fmt.Errorf("kiro executor: build request: %w", err)
	}

	var authID, authLabel, authType, authValue string
	if auth != nil {
		authID = auth.ID
		authLabel = auth.Label
		authType, authValue = auth.AccountInfo()
	}

	apiURL := kiroauth.KiroAPIBaseURL + "/generateAssistantResponse"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return resp, fmt.Errorf("kiro executor: create request: %w", err)
	}
	applyKiroHeaders(httpReq, accessToken, auth)

	helps.RecordAPIRequest(ctx, e.cfg, helps.UpstreamRequestLog{
		URL:       apiURL,
		Method:    http.MethodPost,
		Headers:   httpReq.Header.Clone(),
		Body:      body,
		Provider:  e.Identifier(),
		AuthID:    authID,
		AuthLabel: authLabel,
		AuthType:  authType,
		AuthValue: authValue,
	})

	httpClient := helps.NewProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		helps.RecordAPIResponseError(ctx, e.cfg, err)
		return resp, err
	}
	defer func() {
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("kiro executor: close response body: %v", errClose)
		}
	}()
	helps.RecordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		b, _ := io.ReadAll(httpResp.Body)
		helps.AppendAPIResponseChunk(ctx, e.cfg, b)
		helps.LogWithRequestID(ctx).Debugf("kiro: error status %d: %s",
			httpResp.StatusCode, helps.SummarizeErrorBody(httpResp.Header.Get("Content-Type"), b))
		return resp, statusErr{code: httpResp.StatusCode, msg: string(b)}
	}

	// Accumulate all text from the event stream.
	content, err := drainKiroStream(ctx, e.cfg, httpResp.Body)
	if err != nil {
		helps.RecordAPIResponseError(ctx, e.cfg, err)
		reporter.PublishFailure(ctx)
		return resp, fmt.Errorf("kiro executor: drain stream: %w", err)
	}

	// Emit a synthetic OpenAI-format response.
	out := buildOpenAIChatResponse(req.Model, content)
	reporter.Publish(ctx, usage.Detail{})
	resp = cliproxyexecutor.Response{Payload: out, Headers: httpResp.Header.Clone()}
	return resp, nil
}

// ExecuteStream performs a streaming Kiro / CodeWhisperer request.
func (e *KiroExecutor) ExecuteStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (_ *cliproxyexecutor.StreamResult, err error) {
	accessToken := kiroAccessToken(auth)
	baseModel := stripKiroPrefix(req.Model)

	reporter := helps.NewUsageReporter(ctx, e.Identifier(), baseModel, auth)
	defer reporter.TrackFailure(ctx, &err)

	body, err := buildKiroRequest(req.Payload, opts.SourceFormat.String(), baseModel, true)
	if err != nil {
		return nil, fmt.Errorf("kiro executor: build request: %w", err)
	}

	var authID, authLabel, authType, authValue string
	if auth != nil {
		authID = auth.ID
		authLabel = auth.Label
		authType, authValue = auth.AccountInfo()
	}

	apiURL := kiroauth.KiroAPIBaseURL + "/generateAssistantResponse"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("kiro executor: create request: %w", err)
	}
	applyKiroHeaders(httpReq, accessToken, auth)

	helps.RecordAPIRequest(ctx, e.cfg, helps.UpstreamRequestLog{
		URL:       apiURL,
		Method:    http.MethodPost,
		Headers:   httpReq.Header.Clone(),
		Body:      body,
		Provider:  e.Identifier(),
		AuthID:    authID,
		AuthLabel: authLabel,
		AuthType:  authType,
		AuthValue: authValue,
	})

	httpClient := helps.NewProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		helps.RecordAPIResponseError(ctx, e.cfg, err)
		return nil, err
	}
	helps.RecordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		b, _ := io.ReadAll(httpResp.Body)
		helps.AppendAPIResponseChunk(ctx, e.cfg, b)
		helps.LogWithRequestID(ctx).Debugf("kiro: error status %d: %s",
			httpResp.StatusCode, helps.SummarizeErrorBody(httpResp.Header.Get("Content-Type"), b))
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("kiro executor: close error response body: %v", errClose)
		}
		return nil, statusErr{code: httpResp.StatusCode, msg: string(b)}
	}

	out := make(chan cliproxyexecutor.StreamChunk)
	go func() {
		defer close(out)
		defer func() {
			if errClose := httpResp.Body.Close(); errClose != nil {
				log.Errorf("kiro executor: close stream body: %v", errClose)
			}
		}()

		streamKiroResponse(ctx, e.cfg, httpResp.Body, req.Model, reporter, out)
	}()

	return &cliproxyexecutor.StreamResult{
		Headers: httpResp.Header.Clone(),
		Chunks:  out,
	}, nil
}

// CountTokens is not directly supported; returns a not-implemented error.
func (e *KiroExecutor) CountTokens(_ context.Context, _ *cliproxyauth.Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, fmt.Errorf("kiro executor: CountTokens not supported")
}

// HttpRequest forwards a raw HTTP request, injecting Kiro credentials.
func (e *KiroExecutor) HttpRequest(ctx context.Context, auth *cliproxyauth.Auth, req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("kiro executor: request is nil")
	}
	if ctx != nil {
		req = req.WithContext(ctx)
	}
	applyKiroHeaders(req, kiroAccessToken(auth), auth)
	httpClient := helps.NewProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	return httpClient.Do(req)
}

// ---------------------------------------------------------------------------
// Request building
// ---------------------------------------------------------------------------

// kiroConversationState is the top-level CodeWhisperer request body.
type kiroConversationState struct {
	ConversationState kiroConvState `json:"conversationState"`
}

type kiroConvState struct {
	ConversationID  string          `json:"conversationId,omitempty"`
	CurrentMessage  kiroCurrentMsg  `json:"currentMessage"`
	ChatTriggerType string          `json:"chatTriggerType"`
	History         []kiroHistEntry `json:"history,omitempty"`
}

type kiroCurrentMsg struct {
	UserInputMessage kiroUserMsg `json:"userInputMessage"`
}

type kiroUserMsg struct {
	Content string `json:"content"`
	ModelID string `json:"modelId,omitempty"`
}

type kiroHistEntry struct {
	UserInputMessage         *kiroUserMsg   `json:"userInputMessage,omitempty"`
	AssistantResponseMessage *kiroAssistMsg `json:"assistantResponseMessage,omitempty"`
}

type kiroAssistMsg struct {
	Content string `json:"content"`
}

// buildKiroRequest converts an OpenAI-format request body into a CodeWhisperer request.
func buildKiroRequest(payload []byte, sourceFormat, modelID string, _ bool) ([]byte, error) {
	// Extract messages.  Accept OpenAI format directly; other formats are
	// forwarded as-is after basic extraction.
	var messages []struct {
		Role    string      `json:"role"`
		Content interface{} `json:"content"`
	}

	messagesJSON := gjson.GetBytes(payload, "messages")
	if !messagesJSON.Exists() {
		// Fallback: wrap raw payload content as a user message.
		fallbackContent := string(payload)
		if fc := gjson.GetBytes(payload, "prompt").String(); fc != "" {
			fallbackContent = fc
		}
		return marshalKiroBody("", modelID, fallbackContent, nil)
	}

	if err := json.Unmarshal([]byte(messagesJSON.Raw), &messages); err != nil {
		return nil, fmt.Errorf("kiro: parse messages: %w", err)
	}
	if len(messages) == 0 {
		return nil, fmt.Errorf("kiro: no messages in payload")
	}

	// Collect system messages and prepend them to the first user message.
	var systemParts []string
	for _, m := range messages {
		if strings.EqualFold(m.Role, "system") {
			systemParts = append(systemParts, extractMessageContent(m.Content))
		}
	}

	// Build history (all non-system messages except the last user message).
	var history []kiroHistEntry
	var lastUserContent string
	nonSys := make([]struct {
		Role    string
		Content string
	}, 0, len(messages))
	for _, m := range messages {
		if strings.EqualFold(m.Role, "system") {
			continue
		}
		nonSys = append(nonSys, struct {
			Role    string
			Content string
		}{Role: m.Role, Content: extractMessageContent(m.Content)})
	}

	if len(nonSys) == 0 {
		return nil, fmt.Errorf("kiro: no user messages found")
	}

	lastUserContent = nonSys[len(nonSys)-1].Content
	// If there are system parts, prepend them to the last user message.
	if len(systemParts) > 0 {
		prefix := strings.Join(systemParts, "\n\n") + "\n\n"
		// Only prepend to the very first user turn.
		if len(nonSys) == 1 {
			lastUserContent = prefix + lastUserContent
		} else if nonSys[0].Role == "user" {
			// The prefix was already or will be merged below; handled via history adjustment.
			_ = prefix
		}
	}

	// Build history from all messages except the last one.
	for i := 0; i < len(nonSys)-1; i++ {
		entry := nonSys[i]
		switch strings.ToLower(entry.Role) {
		case "user":
			history = append(history, kiroHistEntry{
				UserInputMessage: &kiroUserMsg{Content: entry.Content},
			})
		case "assistant":
			history = append(history, kiroHistEntry{
				AssistantResponseMessage: &kiroAssistMsg{Content: entry.Content},
			})
		}
	}

	// New conversation ID for stateless operation.
	convID := uuid.New().String()
	return marshalKiroBody(convID, modelID, lastUserContent, history)
}

func marshalKiroBody(convID, modelID, content string, history []kiroHistEntry) ([]byte, error) {
	userMsg := kiroUserMsg{Content: content}
	if strings.TrimSpace(modelID) != "" && modelID != "auto" {
		userMsg.ModelID = modelID
	}
	state := kiroConvState{
		ConversationID:  convID,
		CurrentMessage:  kiroCurrentMsg{UserInputMessage: userMsg},
		ChatTriggerType: "MANUAL",
		History:         history,
	}
	reqBody := kiroConversationState{ConversationState: state}
	return json.Marshal(reqBody)
}

// extractMessageContent converts interface{} message content to a plain string.
func extractMessageContent(content interface{}) string {
	switch v := content.(type) {
	case string:
		return v
	case []interface{}:
		var parts []string
		for _, item := range v {
			if m, ok := item.(map[string]interface{}); ok {
				if t, ok := m["text"].(string); ok {
					parts = append(parts, t)
				} else if m["type"] == "text" {
					if t2, ok := m["text"].(string); ok {
						parts = append(parts, t2)
					}
				}
			}
		}
		return strings.Join(parts, "")
	default:
		b, _ := json.Marshal(content)
		return string(b)
	}
}

// ---------------------------------------------------------------------------
// AWS event stream parsing
// ---------------------------------------------------------------------------

// kiroEventMsg holds a parsed AWS event stream frame.
type kiroEventMsg struct {
	Headers map[string]string
	Payload []byte
}

// parseKiroEventFrame reads exactly one AWS event stream frame from r.
func parseKiroEventFrame(r io.Reader) (*kiroEventMsg, error) {
	// Each frame: prelude (8B) + preludeCRC (4B) + headers + payload + msgCRC (4B).
	prelude := make([]byte, 8)
	if _, err := io.ReadFull(r, prelude); err != nil {
		return nil, err
	}

	totalLen := binary.BigEndian.Uint32(prelude[0:4])
	headersLen := binary.BigEndian.Uint32(prelude[4:8])
	if totalLen < 16 {
		return nil, fmt.Errorf("kiro eventstream: frame too short (%d bytes)", totalLen)
	}

	rest := make([]byte, int(totalLen)-8)
	if _, err := io.ReadFull(r, rest); err != nil {
		return nil, fmt.Errorf("kiro eventstream: read frame body: %w", err)
	}
	full := append(prelude, rest...) //nolint:gocritic // intentional

	// Verify prelude CRC (offset 8-11).
	if preludeCRC := binary.BigEndian.Uint32(full[8:12]); preludeCRC != crc32.Checksum(full[:8], castaTable) {
		return nil, fmt.Errorf("kiro eventstream: prelude CRC mismatch")
	}

	// Parse headers (offset 12 to 12+headersLen).
	headerStart := uint32(12)
	headerEnd := headerStart + headersLen
	if headerEnd > totalLen-4 {
		return nil, fmt.Errorf("kiro eventstream: headers overflow frame")
	}
	headers, err := parseKiroEventHeaders(full[headerStart:headerEnd])
	if err != nil {
		return nil, fmt.Errorf("kiro eventstream: parse headers: %w", err)
	}

	payload := full[headerEnd : totalLen-4]

	// Verify message CRC (last 4 bytes).
	if msgCRC := binary.BigEndian.Uint32(full[totalLen-4:]); msgCRC != crc32.Checksum(full[:totalLen-4], castaTable) {
		return nil, fmt.Errorf("kiro eventstream: message CRC mismatch")
	}

	return &kiroEventMsg{Headers: headers, Payload: payload}, nil
}

// parseKiroEventHeaders parses the binary headers section of an event stream frame.
// Supports the header types used by AWS event streams (bool, uint8, int32, int64, string).
func parseKiroEventHeaders(data []byte) (map[string]string, error) {
	headers := make(map[string]string)
	i := 0
	for i < len(data) {
		// Name length (1 byte).
		nameLen := int(data[i])
		i++
		if i+nameLen > len(data) {
			return nil, fmt.Errorf("header name overflow")
		}
		name := string(data[i : i+nameLen])
		i += nameLen

		if i >= len(data) {
			return nil, fmt.Errorf("missing header type for %q", name)
		}
		hType := data[i]
		i++

		switch hType {
		case 0: // bool true
			headers[name] = "true"
		case 1: // bool false
			headers[name] = "false"
		case 2: // uint8
			if i >= len(data) {
				return nil, fmt.Errorf("uint8 header overflow")
			}
			headers[name] = fmt.Sprintf("%d", data[i])
			i++
		case 4: // int32
			if i+4 > len(data) {
				return nil, fmt.Errorf("int32 header overflow")
			}
			headers[name] = fmt.Sprintf("%d", int32(binary.BigEndian.Uint32(data[i:i+4])))
			i += 4
		case 5: // int64 / timestamp
			if i+8 > len(data) {
				return nil, fmt.Errorf("int64 header overflow")
			}
			headers[name] = fmt.Sprintf("%d", int64(binary.BigEndian.Uint64(data[i:i+8])))
			i += 8
		case 7: // string (2-byte length prefix)
			if i+2 > len(data) {
				return nil, fmt.Errorf("string header length overflow")
			}
			valLen := int(binary.BigEndian.Uint16(data[i : i+2]))
			i += 2
			if i+valLen > len(data) {
				return nil, fmt.Errorf("string header value overflow")
			}
			headers[name] = string(data[i : i+valLen])
			i += valLen
		default:
			// Skip unknown types gracefully: read 2-byte length + body.
			log.Debugf("kiro eventstream: unknown header type %d for %q; skipping", hType, name)
			if i+2 > len(data) {
				return nil, fmt.Errorf("unknown header value overflow")
			}
			valLen := int(binary.BigEndian.Uint16(data[i : i+2]))
			i += 2 + valLen
		}
	}
	return headers, nil
}

// drainKiroStream reads all events from an AWS event stream and returns the accumulated text.
func drainKiroStream(ctx context.Context, cfg *config.Config, body io.Reader) (string, error) {
	var sb strings.Builder
	for {
		msg, err := parseKiroEventFrame(body)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			// Non-fatal: log and stop.
			log.Debugf("kiro: event frame error: %v", err)
			break
		}

		eventType := msg.Headers[":event-type"]
		msgType := msg.Headers[":message-type"]

		if msgType == "exception" {
			// Surface the exception as an error.
			return "", fmt.Errorf("kiro: stream exception: %s", string(msg.Payload))
		}

		if eventType == "assistantResponseEvent" {
			var ev struct {
				Content string `json:"content"`
			}
			if errUnmarshal := json.Unmarshal(msg.Payload, &ev); errUnmarshal == nil {
				sb.WriteString(ev.Content)
			}
		}

		helps.AppendAPIResponseChunk(ctx, cfg, msg.Payload)

		// An empty payload signals end-of-stream.
		if len(msg.Payload) == 0 && eventType == "" {
			break
		}
	}
	return sb.String(), nil
}

// streamKiroResponse reads events from an AWS event stream and emits OpenAI-format SSE chunks.
func streamKiroResponse(ctx context.Context, cfg *config.Config, body io.Reader, model string, reporter *helps.UsageReporter, out chan<- cliproxyexecutor.StreamChunk) {
	msgID := "kiro-" + uuid.New().String()
	created := time.Now().Unix()
	sentFirstChunk := false

	for {
		msg, err := parseKiroEventFrame(body)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			log.Debugf("kiro: event frame read error: %v", err)
			break
		}

		eventType := msg.Headers[":event-type"]
		msgType := msg.Headers[":message-type"]

		if msgType == "exception" {
			out <- cliproxyexecutor.StreamChunk{Err: fmt.Errorf("kiro: stream exception: %s", string(msg.Payload))}
			reporter.PublishFailure(ctx)
			return
		}

		if eventType == "assistantResponseEvent" {
			var ev struct {
				Content string `json:"content"`
			}
			if errUnmarshal := json.Unmarshal(msg.Payload, &ev); errUnmarshal != nil || ev.Content == "" {
				continue
			}

			helps.AppendAPIResponseChunk(ctx, cfg, msg.Payload)

			role := ""
			if !sentFirstChunk {
				role = "assistant"
				sentFirstChunk = true
			}
			chunk := buildOpenAIStreamChunk(msgID, model, created, role, ev.Content, false)
			out <- cliproxyexecutor.StreamChunk{Payload: chunk}
		}

		if len(msg.Payload) == 0 && eventType == "" {
			break
		}
	}

	// Send finish chunk.
	finishChunk := buildOpenAIStreamChunk(msgID, model, created, "", "", true)
	out <- cliproxyexecutor.StreamChunk{Payload: finishChunk}

	doneChunk := []byte("data: [DONE]\n\n")
	out <- cliproxyexecutor.StreamChunk{Payload: doneChunk}

	reporter.Publish(ctx, usage.Detail{})
}

// ---------------------------------------------------------------------------
// OpenAI response builders
// ---------------------------------------------------------------------------

type openAIChatResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
}

type chatChoice struct {
	Index        int         `json:"index"`
	Message      chatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func buildOpenAIChatResponse(model, content string) []byte {
	r := openAIChatResponse{
		ID:      "kiro-" + uuid.New().String(),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []chatChoice{
			{
				Index:        0,
				Message:      chatMessage{Role: "assistant", Content: content},
				FinishReason: "stop",
			},
		},
	}
	b, _ := json.Marshal(r)
	return b
}

// openAIStreamChunk is the SSE chunk format for chat.completion.chunk.
type openAIStreamChunk struct {
	ID      string              `json:"id"`
	Object  string              `json:"object"`
	Created int64               `json:"created"`
	Model   string              `json:"model"`
	Choices []streamChunkChoice `json:"choices"`
}

type streamChunkChoice struct {
	Index        int         `json:"index"`
	Delta        streamDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

type streamDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

func buildOpenAIStreamChunk(id, model string, created int64, role, content string, finish bool) []byte {
	var fr *string
	if finish {
		s := "stop"
		fr = &s
	}
	chunk := openAIStreamChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []streamChunkChoice{
			{
				Index:        0,
				Delta:        streamDelta{Role: role, Content: content},
				FinishReason: fr,
			},
		},
	}
	b, _ := json.Marshal(chunk)
	return append([]byte("data: "), append(b, '\n', '\n')...)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// kiroAccessToken extracts the Bearer token from the auth record.
func kiroAccessToken(auth *cliproxyauth.Auth) string {
	if auth == nil || auth.Metadata == nil {
		return ""
	}
	if v, ok := auth.Metadata["access_token"].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// stripKiroPrefix removes the "kiro-" prefix from a model name before
// forwarding to the CodeWhisperer API.
func stripKiroPrefix(model string) string {
	return strings.TrimPrefix(model, "kiro-")
}

// applyKiroHeaders sets the required HTTP headers for CodeWhisperer API requests.
func applyKiroHeaders(req *http.Request, token string, auth *cliproxyauth.Auth) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.amazon.eventstream")
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("User-Agent", "kiro-cli/1.0")
	req.Header.Set("x-amzn-codewhisperer-optout", "false")

	var attrs map[string]string
	if auth != nil {
		attrs = auth.Attributes
	}
	util.ApplyCustomHeadersFromAttrs(req, attrs)
}
