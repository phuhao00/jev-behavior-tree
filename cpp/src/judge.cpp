#include "engine.hpp"

#include <cstdlib>
#include <sstream>
#include <string>
#include <thread>
#include <vector>

#ifndef WIN32_LEAN_AND_MEAN
#define WIN32_LEAN_AND_MEAN
#endif
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <winsock2.h>
#include <ws2tcpip.h>
#include <windows.h>
#include <winhttp.h>

namespace jev {
namespace {
std::wstring widen(const std::string& text) {
    if (text.empty()) return {};
    int n = MultiByteToWideChar(CP_UTF8, 0, text.data(), static_cast<int>(text.size()), nullptr, 0);
    std::wstring out(n, L'\0');
    MultiByteToWideChar(CP_UTF8, 0, text.data(), static_cast<int>(text.size()), out.data(), n);
    return out;
}
int env_int(const char* name, int fallback, bool non_negative) {
    auto raw = std::getenv(name);
    if (!raw || !*raw) return fallback;
    try {
        int n = std::stoi(raw);
        if (non_negative ? n < 0 : n <= 0) return fallback;
        return n;
    } catch (...) { return fallback; }
}
struct HttpResult { int status = 0; std::string body; bool transport_error = false; bool timeout = false; };
HttpResult https_post(const std::string& body, int timeout_ms) {
    HttpResult result;
    HINTERNET session = WinHttpOpen(L"jev-behavior-tree/cpp", WINHTTP_ACCESS_TYPE_AUTOMATIC_PROXY, WINHTTP_NO_PROXY_NAME, WINHTTP_NO_PROXY_BYPASS, 0);
    if (!session) { result.transport_error = true; result.body = "WinHttpOpen failed"; return result; }
    WinHttpSetTimeouts(session, timeout_ms, timeout_ms, timeout_ms, timeout_ms);
    HINTERNET connect = WinHttpConnect(session, L"ai-gateway.vercel.sh", INTERNET_DEFAULT_HTTPS_PORT, 0);
    HINTERNET request = connect ? WinHttpOpenRequest(connect, L"POST", L"/v4/ai/evaluation-model", nullptr, WINHTTP_NO_REFERER, WINHTTP_DEFAULT_ACCEPT_TYPES, WINHTTP_FLAG_SECURE) : nullptr;
    std::wstring headers = L"Content-Type: application/json\r\nai-evaluation-model-specification-version: 4\r\nai-gateway-protocol-version: 0.0.1\r\nai-gateway-auth-method: api-key\r\nai-model-id: " + widen(model_id()) + L"\r\nAuthorization: Bearer " + widen(std::getenv("AI_GATEWAY_API_KEY") ? std::getenv("AI_GATEWAY_API_KEY") : "") + L"\r\n";
    BOOL ok = request && WinHttpSendRequest(request, headers.c_str(), static_cast<DWORD>(-1), (LPVOID)body.data(), static_cast<DWORD>(body.size()), static_cast<DWORD>(body.size()), 0) && WinHttpReceiveResponse(request, nullptr);
    if (!ok) {
        result.transport_error = true;
        result.timeout = GetLastError() == ERROR_WINHTTP_TIMEOUT;
        result.body = result.timeout ? "" : "Jev 调用失败";
    } else {
        DWORD status = 0, size = sizeof(status);
        WinHttpQueryHeaders(request, WINHTTP_QUERY_STATUS_CODE | WINHTTP_QUERY_FLAG_NUMBER, WINHTTP_HEADER_NAME_BY_INDEX, &status, &size, WINHTTP_NO_HEADER_INDEX);
        result.status = static_cast<int>(status);
        DWORD available = 0;
        do {
            if (!WinHttpQueryDataAvailable(request, &available) || available == 0) break;
            std::string chunk(available, '\0');
            DWORD read = 0;
            if (!WinHttpReadData(request, chunk.data(), available, &read)) break;
            chunk.resize(read);
            result.body += chunk;
        } while (available > 0);
    }
    if (request) WinHttpCloseHandle(request);
    if (connect) WinHttpCloseHandle(connect);
    if (session) WinHttpCloseHandle(session);
    return result;
}
std::string read_sock(SOCKET sock) {
    std::string data;
    char buf[4096];
    while (data.find("\r\n\r\n") == std::string::npos) {
        int n = recv(sock, buf, sizeof(buf), 0);
        if (n <= 0) break;
        data.append(buf, buf + n);
        if (data.size() > 300000) break;
    }
    auto header_end = data.find("\r\n\r\n");
    if (header_end == std::string::npos) return data;
    size_t length = 0;
    auto lower = data.substr(0, header_end);
    for (auto& ch : lower) ch = static_cast<char>(std::tolower(static_cast<unsigned char>(ch)));
    auto pos = lower.find("content-length:");
    if (pos != std::string::npos) length = static_cast<size_t>(std::strtoul(lower.c_str() + pos + 15, nullptr, 10));
    auto body_start = header_end + 4;
    while (data.size() < body_start + length) {
        int n = recv(sock, buf, sizeof(buf), 0);
        if (n <= 0) break;
        data.append(buf, buf + n);
    }
    return data;
}
void send_all(SOCKET sock, const std::string& data) {
    size_t sent = 0;
    while (sent < data.size()) {
        int n = send(sock, data.data() + sent, static_cast<int>(data.size() - sent), 0);
        if (n <= 0) break;
        sent += static_cast<size_t>(n);
    }
}
std::string http_response(int status, const std::string& body, bool json_body) {
    std::string reason = status == 200 ? "OK" : status == 204 ? "No Content" : status == 400 ? "Bad Request" : status == 404 ? "Not Found" : "Error";
    std::ostringstream out;
    out << "HTTP/1.1 " << status << " " << reason << "\r\n"
        << "access-control-allow-origin: *\r\n"
        << "access-control-allow-methods: GET, POST, OPTIONS\r\n"
        << "access-control-allow-headers: content-type\r\n";
    if (status == 204) { out << "content-length: 0\r\n\r\n"; return out.str(); }
    if (json_body) out << "content-type: application/json; charset=utf-8\r\n";
    out << "content-length: " << body.size() << "\r\n\r\n" << body;
    return out.str();
}
void handle_client(SOCKET sock) {
    auto raw = read_sock(sock);
    auto line_end = raw.find("\r\n");
    auto header_end = raw.find("\r\n\r\n");
    if (line_end == std::string::npos || header_end == std::string::npos) { closesocket(sock); return; }
    auto request_line = raw.substr(0, line_end);
    std::istringstream line(request_line);
    std::string method, target, version;
    line >> method >> target >> version;
    auto path = target.substr(0, target.find('?'));
    auto body = raw.substr(header_end + 4);
    auto reply = [&](int status, const nlohmann::json& payload) {
        send_all(sock, http_response(status, payload.dump(), true));
    };
    try {
        if (method == "OPTIONS") send_all(sock, http_response(204, "", false));
        else if (method == "GET" && path == "/health") reply(200, {{"ok", true}, {"service", "jev-intuition"}, {"language", "cpp"}, {"model", model_id()}, {"endpoints", {"/health", "/v1/impulse", "/v1/world", "/v1/tick"}}});
        else if (method != "POST") reply(404, {{"error", "没有这个接口"}});
        else if (body.size() > 262144) reply(413, {{"error", "请求体超过 256KB"}});
        else if (body.find_first_not_of(" \t\r\n") == std::string::npos) reply(400, {{"error", "请求体为空"}});
        else {
            nlohmann::json parsed = nlohmann::json::parse(body);
            nlohmann::json result;
            if (path == "/v1/impulse") result = sense_agent(parsed, gateway_judge);
            else if (path == "/v1/world") result = sense_world(parsed, gateway_judge);
            else if (path == "/v1/tick") result = sense_tick(parsed, gateway_judge);
            else { reply(404, {{"error", "没有这个接口"}}); closesocket(sock); return; }
            reply(200, result);
        }
    } catch (const IntuitionError& err) {
        reply(err.status, {{"error", redact(err.what())}});
    } catch (const nlohmann::json::parse_error&) {
        reply(400, {{"error", "请求体不是合法 JSON"}});
    } catch (const std::exception& err) {
        reply(502, {{"error", redact(err.what())}});
    }
    closesocket(sock);
}
}

std::string model_id() {
    auto raw = std::getenv("JEV_MODEL");
    if (!raw) return "typesafe-ai/jev";
    std::string id = raw;
    auto a = id.find_first_not_of(" \t");
    auto b = id.find_last_not_of(" \t");
    id = a == std::string::npos ? "" : id.substr(a, b - a + 1);
    return id.empty() ? "typesafe-ai/jev" : id;
}

JudgeResult gateway_judge(const nlohmann::json& state, const nlohmann::json& questions) {
    auto key = std::getenv("AI_GATEWAY_API_KEY");
    if (!key || std::string(key).find_first_not_of(" \t") == std::string::npos) throw err_bad(500, "缺少环境变量 AI_GATEWAY_API_KEY");
    int timeout = env_int("JEV_TIMEOUT_MS", 20000, false);
    int retries = env_int("JEV_MAX_RETRIES", 1, true);
    nlohmann::json body = {{"state", state}, {"questions", questions}, {"providerOptions", {{"gateway", {{"zeroDataRetention", true}}}}}};
    IntuitionError last = err_bad(502, "Jev 调用失败");
    for (int attempt = 0; attempt <= retries; ++attempt) {
        auto response = https_post(body.dump(), timeout);
        if (response.timeout) { last = err_bad(504, "Jev 调用超时（" + std::to_string(timeout) + "ms）"); if (attempt == retries) throw last; continue; }
        if (response.transport_error) { last = err_bad(502, redact(response.body.empty() ? "Jev 调用失败" : response.body)); if (attempt == retries) throw last; continue; }
        if (response.status == 401) throw err_bad(401, "AI Gateway 拒绝了这个 key。请检查 AI_GATEWAY_API_KEY 是否仍有效。");
        if (response.status < 200 || response.status >= 300) {
            auto lower = response.body;
            for (auto& ch : lower) ch = static_cast<char>(std::tolower(static_cast<unsigned char>(ch)));
            if (lower.find("credit card") != std::string::npos) throw err_bad(403, "Gateway key 是有效的，但这个 Vercel 账号还没有绑定信用卡，AI Gateway 拒绝了调用。到 Vercel 的 AI 页面加上卡并解锁免费额度后再试。");
            int mapped = response.status >= 400 && response.status < 600 ? response.status : 502;
            auto raw = response.body.empty() ? std::string("Jev 调用失败") : redact(response.body);
            last = err_bad(mapped, raw);
            bool retry = mapped == 408 || mapped == 429 || mapped >= 500;
            if (!retry || attempt == retries) throw last;
            continue;
        }
        auto decoded = nlohmann::json::parse(response.body, nullptr, false);
        if (decoded.is_discarded() || !decoded.is_object() || !decoded.contains("answers") || !decoded["answers"].is_object()) throw err_bad(502, decoded.is_discarded() ? "Jev 返回不是合法 JSON" : "Jev 返回里没有 answers");
        JudgeResult result;
        result.answers = decoded["answers"];
        result.provider_metadata = decoded.contains("providerMetadata") && decoded["providerMetadata"].is_object() ? decoded["providerMetadata"] : nlohmann::json::object();
        result.model_id = model_id();
        return result;
    }
    throw last;
}

int run_server(const std::string& host, int port) {
    WSADATA wsa;
    if (WSAStartup(MAKEWORD(2, 2), &wsa) != 0) return 1;
    SOCKET listener = socket(AF_INET, SOCK_STREAM, IPPROTO_TCP);
    sockaddr_in addr{};
    addr.sin_family = AF_INET;
    inet_pton(AF_INET, host.c_str(), &addr.sin_addr);
    addr.sin_port = htons(static_cast<u_short>(port));
    int yes = 1;
    setsockopt(listener, SOL_SOCKET, SO_REUSEADDR, (char*)&yes, sizeof(yes));
    if (bind(listener, (sockaddr*)&addr, sizeof(addr)) != 0 || listen(listener, 16) != 0) { closesocket(listener); WSACleanup(); return 1; }
    std::printf("Jev 直觉服务 (cpp)  http://%s:%d\n模型 %s\nGET /health   POST /v1/impulse   POST /v1/world   POST /v1/tick\n", host.c_str(), port, model_id().c_str());
    for (;;) {
        SOCKET client = accept(listener, nullptr, nullptr);
        if (client == INVALID_SOCKET) continue;
        std::thread(handle_client, client).detach();
    }
}
}  // namespace jev
