#include "policy.hpp"

#include <algorithm>
#include <cmath>
#include <cctype>

namespace jev {

IntuitionError::IntuitionError(int status, std::string message) : std::runtime_error(std::move(message)), status(status) {}

IntuitionError err_bad(int status, std::string message) { return IntuitionError(status, std::move(message)); }

std::string redact(const std::string& message) {
    std::string out;
    for (size_t i = 0; i < message.size();) {
        if (message.compare(i, 4, "vck_") == 0) {
            size_t j = i + 4;
            while (j < message.size() && (std::isalnum(static_cast<unsigned char>(message[j])) || message[j] == '_' || message[j] == '-')) ++j;
            out += "[redacted]";
            i = j;
            continue;
        }
        if (i + 7 <= message.size()) {
            auto head = message.substr(i, 7);
            for (auto& ch : head) ch = static_cast<char>(std::tolower(static_cast<unsigned char>(ch)));
            if (head == "bearer ") {
                out += "Bearer [redacted]";
                i += 7;
                while (i < message.size() && !std::isspace(static_cast<unsigned char>(message[i]))) ++i;
                continue;
            }
        }
        out.push_back(message[i++]);
    }
    if (out.size() > 500) out.resize(500);
    return out;
}

std::optional<double> as_float(const nlohmann::json& value) {
    if (value.is_number()) return value.get<double>();
    return std::nullopt;
}

Policy resolve_policy(const nlohmann::json* input) {
    Policy policy;
    if (!input || !input->is_object()) return policy;
    for (const char* key : {"switchConfidence", "interruptAt", "uncertainMargin"}) {
        if (!input->contains(key) || (*input)[key].is_null()) continue;
        auto value = as_float((*input)[key]);
        if (!value || !std::isfinite(*value) || *value < 0 || *value > 1) throw err_bad(400, std::string("policy.") + key + " 必须是 0 到 1 之间的数字");
        if (std::string(key) == "switchConfidence") policy.switch_confidence = *value;
        else if (std::string(key) == "interruptAt") policy.interrupt_at = *value;
        else policy.uncertain_margin = *value;
    }
    return policy;
}

Tri classify_boolean(std::optional<double> probability, double margin) {
    if (!probability || !std::isfinite(*probability)) return Tri::Uncertain;
    if (*probability >= 0.5 + margin) return Tri::True;
    if (*probability <= 0.5 - margin) return Tri::False;
    return Tri::Uncertain;
}

Tri classify_interrupt(std::optional<double> probability, double interrupt_at) {
    if (!probability || !std::isfinite(*probability)) return Tri::Uncertain;
    if (*probability >= interrupt_at) return Tri::True;
    if (*probability <= 1.0 - interrupt_at) return Tri::False;
    return Tri::Uncertain;
}

static std::string trim_copy(std::string text) {
    auto not_space = [](unsigned char ch) { return !std::isspace(ch); };
    text.erase(text.begin(), std::find_if(text.begin(), text.end(), not_space));
    text.erase(std::find_if(text.rbegin(), text.rend(), not_space).base(), text.end());
    return text;
}

Decision decide_disposition(std::string current, const std::string& suggested, std::optional<double> confidence, Tri interrupt, const std::optional<std::string>& hold_key, double switch_confidence) {
    current = trim_copy(current);
    std::string expanded = hold_key && suggested == *hold_key && !current.empty() ? current : suggested;
    if (current.empty()) return {"switch", expanded, "first-decision"};
    if (expanded == current) return {"continue", current, "still-fitting"};
    if (interrupt == Tri::True) return {"switch", expanded, "interrupt"};
    if (!confidence || *confidence >= switch_confidence) return {"switch", expanded, "confident"};
    return {"hold", current, "hysteresis"};
}

bool tactic_needs_target(const std::string& tactic) {
    return tactic != "hold" && tactic != "patrol" && tactic != "hide" && tactic != "flee";
}

static double dist_or_far(const TargetCandidate& item) { return item.distance.value_or(1e9); }

static std::optional<std::string> fallback_target(const std::string& tactic, std::vector<TargetCandidate> ranked) {
    std::sort(ranked.begin(), ranked.end(), [](const auto& a, const auto& b) { return dist_or_far(a) < dist_or_far(b); });
    if (tactic == "assist") {
        std::vector<TargetCandidate> allies;
        for (const auto& entry : ranked) if (entry.relation == "ally") allies.push_back(entry);
        std::sort(allies.begin(), allies.end(), [](const auto& a, const auto& b) {
            auto ha = a.health.value_or(1.0);
            auto hb = b.health.value_or(1.0);
            if (ha != hb) return ha < hb;
            return dist_or_far(a) < dist_or_far(b);
        });
        if (!allies.empty()) return allies.front().id;
        return std::nullopt;
    }
    if (tactic == "investigate" || tactic == "interact") {
        for (const auto& entry : ranked) if (entry.kind == "interest" || entry.kind == "hazard" || entry.kind == "prop") return entry.id;
        if (!ranked.empty()) return ranked.front().id;
        return std::nullopt;
    }
    for (const auto& entry : ranked) if (entry.relation == "enemy") return entry.id;
    if (!ranked.empty()) return ranked.front().id;
    return std::nullopt;
}

Bound bind_target(const std::string& tactic, const std::string& model_target_id, const std::vector<TargetCandidate>& roster) {
    if (!tactic_needs_target(tactic)) return {};
    if (model_target_id != "none" && !model_target_id.empty()) {
        for (const auto& entry : roster) if (entry.id == model_target_id) return {model_target_id, "model"};
    }
    if (auto fallback = fallback_target(tactic, roster)) return {*fallback, "geometric-fallback"};
    return {};
}

ScoreRead score_band(double score, const std::vector<std::string>& labels) {
    double max = labels.empty() ? 0 : static_cast<double>(labels.size() - 1);
    double clamped = std::clamp(score, 0.0, max);
    int level = static_cast<int>(std::clamp(std::round(clamped), 0.0, max));
    ScoreRead read;
    read.score = std::round(clamped * 100.0) / 100.0;
    read.level = level;
    read.label = level >= 0 && static_cast<size_t>(level) < labels.size() ? labels[static_cast<size_t>(level)] : "unknown";
    return read;
}

}  // namespace jev
