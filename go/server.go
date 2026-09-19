package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
)

func main() {
	loadDotEnv()
	if trim(os.Getenv("AI_GATEWAY_API_KEY")) == "" {
		log.Fatal("缺少 AI_GATEWAY_API_KEY。复制 .env.example 为 .env 后填入 Vercel AI Gateway 的 key。")
	}
	host := trim(os.Getenv("HOST"))
	if host == "" {
		host = "127.0.0.1"
	}
	port := positivePort(os.Getenv("PORT"), 8788)
	addr := host + ":" + strconv.Itoa(port)
	mux := http.NewServeMux()
	mux.HandleFunc("/", handle)
	log.Printf("Jev 直觉服务 (%s)  http://%s", "go", addr)
	log.Printf("模型 %s", modelID())
	log.Print("GET /health   POST /v1/impulse   POST /v1/world   POST /v1/tick")
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func handle(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		writeJSON(w, http.StatusNoContent, nil)
		return
	}
	path := r.URL.Path
	if r.Method == http.MethodGet && path == "/health" {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":        true,
			"service":   "jev-intuition",
			"language":  "go",
			"model":     modelID(),
			"endpoints": []string{"/health", "/v1/impulse", "/v1/world", "/v1/tick"},
		})
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "没有这个接口"})
		return
	}
	body, err := readBody(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	switch path {
	case "/v1/impulse":
		out, err := senseAgent(body, gatewayJudge)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	case "/v1/world":
		out, err := senseWorld(body, gatewayJudge)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	case "/v1/tick":
		out, err := senseTick(body, gatewayJudge)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "没有这个接口"})
	}
}

func readBody(r *http.Request) (any, error) {
	payload, err := io.ReadAll(io.LimitReader(r.Body, 262144+1))
	if err != nil {
		return nil, errBad(400, "读取请求失败")
	}
	if len(payload) > 262144 {
		return nil, errBad(413, "请求体超过 256KB")
	}
	if trim(string(payload)) == "" {
		return nil, errBad(400, "请求体为空")
	}
	var body any
	if err := json.Unmarshal(payload, &body); err != nil {
		return nil, errBad(400, "请求体不是合法 JSON")
	}
	return body, nil
}

func writeErr(w http.ResponseWriter, err error) {
	writeJSON(w, statusOf(err), map[string]any{"error": redact(err.Error())})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("access-control-allow-origin", "*")
	w.Header().Set("access-control-allow-methods", "GET, POST, OPTIONS")
	w.Header().Set("access-control-allow-headers", "content-type")
	if status == http.StatusNoContent {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(body)
}

func positivePort(raw string, fallback int) int {
	n, err := strconv.Atoi(trim(raw))
	if err != nil || n <= 0 || n > 65535 {
		return fallback
	}
	return n
}

func loadDotEnv() {
	for _, path := range []string{".env", "../.env"} {
		loadEnvFile(path)
	}
}

func loadEnvFile(path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	allowed := map[string]struct{}{
		"AI_GATEWAY_API_KEY": {},
		"JEV_MODEL":          {},
		"JEV_TIMEOUT_MS":     {},
		"JEV_MAX_RETRIES":    {},
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if _, ok := allowed[key]; !ok {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		_ = os.Setenv(key, value)
	}
}
