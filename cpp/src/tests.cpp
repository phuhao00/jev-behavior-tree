#include "engine.hpp"
#include "policy.hpp"

#include <iostream>
#include <cstdlib>

namespace {
int failures = 0;
void check(bool ok, const char* expr, int line) {
    if (!ok) { std::cerr << "FAIL " << line << " " << expr << "\n"; ++failures; }
}
#define CHECK(cond) check((cond), #cond, __LINE__)
}

int main() {
    using namespace jev;
    auto low = decide_disposition("patrol", "engage", 0.4, Tri::False, "hold", 0.72);
    CHECK(low.disposition == "hold" && low.executing == "patrol" && low.because == "hysteresis");
    auto interrupt = decide_disposition("patrol", "flee", 0.4, Tri::True, "hold", 0.72);
    CHECK(interrupt.executing == "flee" && interrupt.because == "interrupt");
    auto hold = decide_disposition("patrol", "hold", 0.95, Tri::True, "hold", 0.72);
    CHECK(hold.executing == "patrol" && hold.because == "still-fitting");
    CHECK(bind_target("patrol", "player", {}).target_source == "none");
    auto assist = bind_target("assist", "none", {
        {"wolf", "enemy", 2.0, 1.0, "creature"},
        {"mira", "ally", 6.0, 0.2, "npc"},
        {"squire", "ally", 3.0, 0.9, "npc"},
    });
    CHECK(assist.target_id && *assist.target_id == "mira" && assist.target_source == "geometric-fallback");

    auto guard = nlohmann::json::parse(R"({
      "scene":{"place":"test yard"},
      "agent":{"id":"rook","role":"guard","health":1,"currentTactic":"patrol","currentTargetId":"wolf","secondsOnTactic":4},
      "player":{"id":"player","kind":"player","distance":4,"visible":true,"health":1,"relation":"neutral","activity":"sprinting with a blade"},
      "nearby":[
        {"id":"wolf","kind":"creature","distance":9,"health":0.8,"relation":"enemy","activity":"circling"},
        {"id":"mira","kind":"npc","faction":"chapel","distance":3,"health":0.2,"relation":"ally","activity":"on the ground"}
      ]
    })");
    auto answered = [](std::string tactic, double confidence, double interrupt, std::string target) {
        return [=](const nlohmann::json&, const nlohmann::json&) {
            JudgeResult result;
            result.model_id = "typesafe-ai/jev";
            result.answers = {
                {"tactic", {{"type","choice"},{"choice",tactic},{"probabilities",{{tactic,0.8},{"hold",0.2}}}}},
                {"target", {{"type","choice"},{"choice",target},{"probabilities",{{target,0.9},{"none",0.1}}}}},
                {"threat", {{"type","score"},{"score",2.2}}},
                {"interrupt", {{"type","boolean"},{"probability",interrupt}}},
                {"opening", {{"type","boolean"},{"probability",0.2}}},
                {"playerHostile", {{"type","boolean"},{"probability",0.91}}},
                {"allyNeedsHelp", {{"type","boolean"},{"probability",0.2}}},
            };
            result.provider_metadata = {{"typesafe", {{"confidence", {{"tactic", confidence}, {"threat", 0.8}}}}}};
            return result;
        };
    };
    bool called = false;
    auto dead = guard;
    dead["agent"]["health"] = 0;
    auto skipped = sense_agent(dead, [&](const nlohmann::json&, const nlohmann::json&) -> JudgeResult { called = true; throw err_bad(500, "should not be called"); });
    CHECK(!called && skipped["disposition"] == "incapacitated" && skipped["tactic"] == "none");

    auto held = sense_agent(guard, answered("engage", 0.2, 0.1, "player"));
    CHECK(held["suggestedTactic"] == "engage" && held["tactic"] == "patrol" && held["disposition"] == "hold" && held["because"] == "hysteresis");
    CHECK(held["targetId"].is_null() && held["playerHostile"] == true && held["confidenceSource"] == "typesafe");

    auto fresh = guard;
    fresh["agent"].erase("currentTargetId");
    auto locked = sense_agent(fresh, answered("engage", 0.9, 0.1, "player"));
    CHECK(locked["tactic"] == "engage" && locked["because"] == "confident" && locked["targetSource"] == "model" && locked["targetId"] == "player");

    auto kept_body = guard;
    kept_body["agent"]["currentTactic"] = "engage";
    auto kept = sense_agent(kept_body, answered("engage", 0.95, 0.1, "player"));
    CHECK(kept["disposition"] == "continue" && kept["targetSource"] == "kept" && kept["targetId"] == "wolf" && kept["suggestedTargetId"] == "player");

    auto world = sense_world(nlohmann::json::parse(R"({"scene":{"place":"chapel","currentDirective":"hold_atmosphere","secondsOnDirective":10},"player":{"activity":"walking","health":1,"dominance":"passing"}})"), [](const nlohmann::json&, const nlohmann::json&) {
        JudgeResult result;
        result.model_id = "typesafe-ai/jev";
        result.answers = {
            {"directive", {{"type","choice"},{"choice","ambush_now"},{"probabilities",{{"ambush_now",0.55},{"hold_atmosphere",0.45}}}}},
            {"tension", {{"type","score"},{"score",1.1}}},
            {"overextended", {{"type","boolean"},{"probability",0.2}}},
        };
        result.provider_metadata = {{"typesafe", {{"confidence", {{"directive", 0.3}}}}}};
        return result;
    });
    CHECK(world["directive"] == "hold_atmosphere" && world["suggestedDirective"] == "ambush_now" && world["disposition"] == "hold" && world["playerOverextended"] == false);
    if (failures) { std::cerr << failures << " failed\n"; return 1; }
    std::cout << "10 checks passed\n";
    return 0;
}
