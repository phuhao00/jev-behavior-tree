package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const gatewayURL = "https://ai-gateway.vercel.sh/v4/ai/evaluation-model"

func modelID() string {
	if id := trim(os.Getenv("JEV_MODEL")); id != "" {
		return id
	}
	return "typesafe-ai/jev"
}

func gatewayJudge(state any, questions map[string]any) (judgeResult, error) {
	key := trim(os.Getenv("AI_GATEWAY_API_KEY"))
	if key == "" {
		return judgeResult{}, errBad(500, "缺少环境变量 AI_GATEWAY_API_KEY")
	}
	timeout := positiveInt(os.Getenv("JEV_TIMEOUT_MS"), 20_000)
	retries := nonNegativeInt(os.Getenv("JEV_MAX_RETRIES"), 1)
	body, err := json.Marshal(map[string]any{
		"state":     state,
		"questions": questions,
		"providerOptions": map[string]any{
			"gateway": map[string]any{"zeroDataRetention": true},
		},
	})
	if err != nil {
		return judgeResult{}, errBad(500, "无法编码 Jev 请求")
	}

	var last error
	for attempt := 0; attempt <= retries; attempt++ {
		result, status, retry, callErr := postEvaluate(key, body, time.Duration(timeout)*time.Millisecond)
		if callErr == nil {
			return result, nil
		}
		last = callErr
		if !retry || attempt == retries {
			if status != 0 {
				return judgeResult{}, callErr
			}
			break
		}
	}
	if last == nil {
		last = errBad(502, "Jev 调用失败")
	}
	return judgeResult{}, last
}

func postEvaluate(key string, body []byte, timeout time.Duration) (judgeResult, int, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, gatewayURL, bytes.NewReader(body))
	if err != nil {
		return judgeResult{}, 0, false, errBad(500, "无法创建 Jev 请求")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("ai-model-id", modelID())
	req.Header.Set("ai-evaluation-model-specification-version", "4")
	req.Header.Set("ai-gateway-protocol-version", "0.0.1")
	req.Header.Set("ai-gateway-auth-method", "api-key")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return judgeResult{}, 0, true, errBad(504, "Jev 调用超时（"+strconv.Itoa(int(timeout.Milliseconds()))+"ms）")
		}
		return judgeResult{}, 0, true, errBad(502, redact(err.Error()))
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == 401 {
		return judgeResult{}, 401, false, errBad(401, "AI Gateway 拒绝了这个 key。请检查 AI_GATEWAY_API_KEY 是否仍有效。")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw := strings.TrimSpace(string(payload))
		if strings.Contains(strings.ToLower(raw), "credit card") {
			return judgeResult{}, 403, false, errBad(403, "Gateway key 是有效的，但这个 Vercel 账号还没有绑定信用卡，AI Gateway 拒绝了调用。到 Vercel 的 AI 页面加上卡并解锁免费额度后再试。")
		}
		status := resp.StatusCode
		if status < 400 || status >= 600 {
			status = 502
		}
		retry := status == 408 || status == 429 || status >= 500
		if raw == "" {
			raw = "Jev 调用失败"
		}
		return judgeResult{}, status, retry, errBad(status, redact(raw))
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return judgeResult{}, 502, false, errBad(502, "Jev 返回不是合法 JSON")
	}
	answers, _ := decoded["answers"].(map[string]any)
	if answers == nil {
		return judgeResult{}, 502, false, errBad(502, "Jev 返回里没有 answers")
	}
	meta, _ := decoded["providerMetadata"].(map[string]any)
	return judgeResult{
		Answers:          answers,
		ProviderMetadata: meta,
		ModelID:          modelID(),
		Usage:            readUsage(decoded["usage"]),
	}, 200, false, nil
}

func readUsage(v any) *TokenUsage {
	raw, _ := v.(map[string]any)
	if raw == nil {
		return nil
	}
	in := readCount(raw["inputTokens"])
	if in == nil {
		in = readCount(raw["promptTokens"])
	}
	out := readCount(raw["outputTokens"])
	if out == nil {
		out = readCount(raw["completionTokens"])
	}
	total := readCount(raw["totalTokens"])
	if total == nil && in != nil && out != nil {
		sum := *in + *out
		total = &sum
	}
	if in == nil && out == nil && total == nil {
		return nil
	}
	return &TokenUsage{InputTokens: in, OutputTokens: out, TotalTokens: total}
}

func readCount(v any) *float64 {
	n, ok := asFloat(v)
	if !ok || !finite(n) {
		return nil
	}
	return &n
}

func positiveInt(raw string, fallback int) int {
	n, err := strconv.Atoi(trim(raw))
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func nonNegativeInt(raw string, fallback int) int {
	n, err := strconv.Atoi(trim(raw))
	if err != nil || n < 0 {
		return fallback
	}
	return n
}
