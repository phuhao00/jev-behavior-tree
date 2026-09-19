#pragma once

#include "policy.hpp"

#include <functional>

namespace jev {

struct JudgeResult {
    nlohmann::json answers = nlohmann::json::object();
    nlohmann::json provider_metadata = nlohmann::json::object();
    std::string model_id;
};

using Judge = std::function<JudgeResult(const nlohmann::json&, const nlohmann::json&)>;

nlohmann::json sense_agent(const nlohmann::json& input, const Judge& judge);
nlohmann::json sense_world(const nlohmann::json& input, const Judge& judge);
nlohmann::json sense_tick(const nlohmann::json& input, const Judge& judge);

std::string model_id();
JudgeResult gateway_judge(const nlohmann::json& state, const nlohmann::json& questions);
int run_server(const std::string& host, int port);

}  // namespace jev
