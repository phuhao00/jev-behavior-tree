#pragma once

#include <optional>
#include <string>
#include <utility>
#include <vector>

#include <nlohmann/json.hpp>

namespace jev {

inline constexpr const char* kHoldTactic = "hold";
inline constexpr const char* kHoldDirective = "hold_atmosphere";

using Pairs = std::vector<std::pair<std::string, std::string>>;

std::vector<std::string> threat_levels();
std::vector<std::string> tension_levels();
Pairs world_directives();
std::optional<Pairs> tactics_for_role(const std::string& role);
std::optional<std::pair<std::string, std::string>> prior_for_role(const std::string& role);
const char* opening_kind_for(const std::string& role);
std::pair<std::string, Pairs> opening_question(const std::string& role);
nlohmann::json pairs_json(const Pairs& pairs);

}  // namespace jev
