#pragma once

#include <optional>
#include <string>
#include <vector>

#include <nlohmann/json.hpp>

namespace jev {

enum class Tri { False, True, Uncertain };

inline void to_json(nlohmann::json& j, Tri value) {
    if (value == Tri::True) j = true;
    else if (value == Tri::False) j = false;
    else j = "uncertain";
}

struct Policy {
    double switch_confidence = 0.72;
    double interrupt_at = 0.75;
    double uncertain_margin = 0.12;
};

struct Decision {
    std::string disposition;
    std::string executing;
    std::string because;
};

struct TargetCandidate {
    std::string id;
    std::string relation;
    std::optional<double> distance;
    std::optional<double> health;
    std::string kind;
};

struct Bound {
    std::optional<std::string> target_id;
    std::string target_source = "none";
};

struct ScoreRead {
    double score = 0;
    int level = 0;
    std::string label;
};

class IntuitionError : public std::runtime_error {
public:
    int status;
    IntuitionError(int status, std::string message);
};

IntuitionError err_bad(int status, std::string message);
std::string redact(const std::string& message);

std::optional<double> as_float(const nlohmann::json& value);
Policy resolve_policy(const nlohmann::json* input);
Tri classify_boolean(std::optional<double> probability, double margin);
Tri classify_interrupt(std::optional<double> probability, double interrupt_at);
Decision decide_disposition(std::string current, const std::string& suggested, std::optional<double> confidence, Tri interrupt, const std::optional<std::string>& hold_key, double switch_confidence);
bool tactic_needs_target(const std::string& tactic);
Bound bind_target(const std::string& tactic, const std::string& model_target_id, const std::vector<TargetCandidate>& roster);
ScoreRead score_band(double score, const std::vector<std::string>& labels);

}  // namespace jev
