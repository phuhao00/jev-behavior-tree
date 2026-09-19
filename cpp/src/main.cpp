#include "engine.hpp"

#include <cstdlib>
#include <iostream>
#include <string>

namespace {
void load_dotenv() {
    for (const char* path : {".env", "../.env"}) {
        FILE* file = nullptr;
        fopen_s(&file, path, "rb");
        if (!file) continue;
        std::string data;
        char buf[1024];
        size_t n = 0;
        while ((n = fread(buf, 1, sizeof(buf), file)) > 0) data.append(buf, n);
        fclose(file);
        size_t i = 0;
        while (i < data.size()) {
            auto end = data.find('\n', i);
            if (end == std::string::npos) end = data.size();
            auto line = data.substr(i, end - i);
            i = end + 1;
            if (!line.empty() && line.back() == '\r') line.pop_back();
            auto start = line.find_first_not_of(" \t");
            if (start == std::string::npos || line[start] == '#') continue;
            line = line.substr(start);
            if (line.rfind("export ", 0) == 0) line = line.substr(7);
            auto cut = line.find('=');
            if (cut == std::string::npos) continue;
            auto key = line.substr(0, cut);
            auto key_end = key.find_last_not_of(" \t");
            key = key_end == std::string::npos ? "" : key.substr(0, key_end + 1);
            if (key != "AI_GATEWAY_API_KEY" && key != "JEV_MODEL" && key != "JEV_TIMEOUT_MS" && key != "JEV_MAX_RETRIES") continue;
            if (std::getenv(key.c_str())) continue;
            auto value = line.substr(cut + 1);
            auto a = value.find_first_not_of(" \t");
            auto b = value.find_last_not_of(" \t");
            value = a == std::string::npos ? "" : value.substr(a, b - a + 1);
            if (value.size() >= 2 && ((value.front() == '"' && value.back() == '"') || (value.front() == '\'' && value.back() == '\''))) value = value.substr(1, value.size() - 2);
            _putenv_s(key.c_str(), value.c_str());
        }
    }
}
int positive_port(const char* raw, int fallback) {
    if (!raw || !*raw) return fallback;
    try {
        int n = std::stoi(raw);
        return n > 0 && n <= 65535 ? n : fallback;
    } catch (...) { return fallback; }
}
}

int main() {
    load_dotenv();
    auto key = std::getenv("AI_GATEWAY_API_KEY");
    if (!key || std::string(key).find_first_not_of(" \t") == std::string::npos) {
        std::cerr << "缺少 AI_GATEWAY_API_KEY。复制 .env.example 为 .env 后填入 Vercel AI Gateway 的 key。\n";
        return 1;
    }
    std::string host = "127.0.0.1";
    if (auto raw = std::getenv("HOST")) if (std::string(raw).find_first_not_of(" \t") != std::string::npos) host = raw;
    return jev::run_server(host, positive_port(std::getenv("PORT"), 8791));
}
