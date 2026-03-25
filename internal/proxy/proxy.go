package proxy

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

func modelAllowed(allowed []string, model string) bool {
	if len(allowed) == 0 {
		return true
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return false
	}
	for _, item := range allowed {
		if strings.TrimSpace(item) == model {
			return true
		}
	}
	return false
}

func composeProxyModelID(channelName, dbModel string) string {
	channelName = strings.TrimSpace(channelName)
	dbModel = strings.TrimSpace(dbModel)
	if channelName == "" || dbModel == "" {
		return ""
	}
	prefix := channelName + "/"
	if strings.HasPrefix(dbModel, prefix) {
		return dbModel
	}
	return prefix + dbModel
}

func parseProxyModelID(model string) (channelName string, dbModel string, ok bool) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "", "", false
	}
	parts := strings.SplitN(model, "/", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	channelName = strings.TrimSpace(parts[0])
	dbModel = strings.TrimSpace(parts[1])
	if channelName == "" || dbModel == "" {
		return "", "", false
	}
	return channelName, dbModel, true
}

func rewriteGeminiPathWithUpstreamModel(proxyPath, upstreamModel string) (string, error) {
	const prefix = "/v1beta/models/"
	if !strings.HasPrefix(proxyPath, prefix) {
		return "", fmt.Errorf("invalid gemini path")
	}

	rest := strings.TrimPrefix(proxyPath, prefix)
	var suffix string
	switch {
	case strings.HasSuffix(rest, ":generateContent"):
		suffix = ":generateContent"
	case strings.HasSuffix(rest, ":streamGenerateContent"):
		suffix = ":streamGenerateContent"
	default:
		return "", fmt.Errorf("invalid gemini path")
	}

	upstreamModel = strings.TrimSpace(upstreamModel)
	if upstreamModel == "" {
		return "", fmt.Errorf("empty upstream model")
	}
	parts := strings.Split(upstreamModel, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return prefix + strings.Join(parts, "/") + suffix, nil
}

func rewriteBodyModel(body []byte, upstreamModel string) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("invalid JSON body")
	}
	payload["model"] = upstreamModel
	out, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to encode JSON body")
	}
	return out, nil
}
