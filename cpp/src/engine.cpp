#include "engine.hpp"
#include "roles.hpp"

#include <algorithm>
#include <cctype>
#include <chrono>
#include <cmath>
#include <functional>
#include <mutex>
#include <regex>
#include <set>
#include <thread>

namespace jev {
namespace {
using json = nlohmann::json;

struct Vec3 { double x = 0, y = 0; std::optional<double> z; };
struct Entity {
    std::string id, kind, name, faction, relation, activity;
    std::optional<double> distance, health;
    std::optional<bool> visible;
    std::vector<std::string> tags;
    std::optional<Vec3> pos;
};
struct Scene {
    std::string place, time_of_day, weather, current_directive;
    std::optional<double> seconds_on_directive;
    std::vector<std::string> recent_events;
};
struct AgentState {
    std::string id, role, name, personality, goal, faction, activity, current_tactic;
    std::optional<double> health, stamina, seconds_on_tactic;
    std::optional<std::string> current_target_id;
    std::vector<std::string> memory;
    std::optional<Vec3> pos;
};
struct Presence { std::string id, role, activity, faction; std::optional<double> health; };
struct WorldPlayer { std::string activity, dominance; std::optional<double> health; };
struct SenseReq { Scene scene; AgentState agent; std::vector<Entity> nearby; std::optional<Entity> player; Pairs tactics; Policy policy; };
struct WorldReq { Scene scene; std::vector<Presence> presences; std::optional<WorldPlayer> player; Pairs directives; Policy policy; };
struct Roster { std::string id, choice_key, kind, name, faction, relation, activity; std::optional<double> distance, health; std::optional<bool> visible; std::vector<std::string> tags; };

const json* at(const json& node, const char* key) {
    if (!node.is_object() || !node.contains(key) || node[key].is_null()) return nullptr;
    return &node[key];
}
const json& need_obj(const json& node, const std::string& path) {
    if (!node.is_object()) throw err_bad(400, path + " 必须是对象");
    return node;
}
std::string text_of(const json* node, const std::string& path, size_t max, bool required) {
    if (!node) { if (required) throw err_bad(400, path + " 不能为空"); return ""; }
    if (!node->is_string()) throw err_bad(400, path + " 不能为空");
    auto text = node->get<std::string>();
    auto a = text.find_first_not_of(" \t\r\n");
    auto b = text.find_last_not_of(" \t\r\n");
    text = a == std::string::npos ? "" : text.substr(a, b - a + 1);
    if (text.empty()) { if (required) throw err_bad(400, path + " 不能为空"); return ""; }
    if (text.size() > max) throw err_bad(400, path + " 超过 " + std::to_string(max) + " 字");
    return text;
}
std::optional<double> finite_of(const json* node, const std::string& path, bool required) {
    if (!node) { if (required) throw err_bad(400, path + " 必须是有限数字"); return std::nullopt; }
    auto value = as_float(*node);
    if (!value || !std::isfinite(*value)) throw err_bad(400, path + " 必须是有限数字");
    return value;
}
std::optional<double> unit_of(const json* node, const std::string& path) {
    auto value = finite_of(node, path, false);
    if (!value) return std::nullopt;
    if (*value < 0 || *value > 1) throw err_bad(400, path + " 必须在 0 到 1 之间");
    return value;
}
std::optional<double> nonneg_of(const json* node, const std::string& path) {
    auto value = finite_of(node, path, false);
    if (value && *value < 0) throw err_bad(400, path + " 不能是负数");
    return value;
}
bool valid_id(const std::string& text) { return std::regex_match(text, std::regex("^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$")); }
std::string id_of(const json* node, const std::string& path) {
    auto text = node && node->is_string() ? node->get<std::string>() : "";
    if (!valid_id(text)) throw err_bad(400, path + " 需要 1 到 64 位的英文、数字、_ . : -");
    return text;
}
std::vector<std::string> string_list(const json* node, const std::string& path, size_t max, size_t chars) {
    if (!node) return {};
    if (!node->is_array()) throw err_bad(400, path + " 必须是字符串数组");
    std::vector<std::string> out;
    size_t start = node->size() > max ? node->size() - max : 0;
    for (size_t i = start; i < node->size(); ++i) out.push_back(text_of(&(*node)[i], path + "[" + std::to_string(i) + "]", chars, true));
    return out;
}
std::optional<Vec3> vec_of(const json* node, const std::string& path) {
    if (!node) return std::nullopt;
    auto raw = need_obj(*node, path);
    Vec3 v;
    v.x = *finite_of(at(raw, "x"), path + ".x", true);
    v.y = *finite_of(at(raw, "y"), path + ".y", true);
    v.z = finite_of(at(raw, "z"), path + ".z", false);
    return v;
}
Pairs option_map(const json& node, const std::string& path, size_t min, size_t max) {
    auto raw = need_obj(node, path);
    if (raw.size() < min || raw.size() > max) throw err_bad(400, path + " 需要 " + std::to_string(min) + " 到 " + std::to_string(max) + " 个选项");
    Pairs out;
    static const std::regex key_re("^[A-Za-z][A-Za-z0-9_]{0,40}$");
    for (auto it = raw.begin(); it != raw.end(); ++it) {
        if (!std::regex_match(it.key(), key_re)) throw err_bad(400, path + "." + it.key() + " 的 id 需要以字母开头，只能含英文、数字和下划线");
        if (!it.value().is_string()) throw err_bad(400, path + "." + it.key() + " 需要一段情境描述，不能为空");
        auto text = text_of(&it.value(), path + "." + it.key(), 500, true);
        out.emplace_back(it.key(), text);
    }
    return out;
}
Scene scene_of(const json* node) {
    auto raw = need_obj(node ? *node : json(nullptr), "scene");
    Scene s;
    s.place = text_of(at(raw, "place"), "scene.place", 200, true);
    s.time_of_day = text_of(at(raw, "timeOfDay"), "scene.timeOfDay", 40, false);
    s.weather = text_of(at(raw, "weather"), "scene.weather", 80, false);
    s.current_directive = text_of(at(raw, "currentDirective"), "scene.currentDirective", 64, false);
    s.seconds_on_directive = nonneg_of(at(raw, "secondsOnDirective"), "scene.secondsOnDirective");
    s.recent_events = string_list(at(raw, "recentEvents"), "scene.recentEvents", 6, 180);
    return s;
}
AgentState agent_of(const json* node) {
    auto raw = need_obj(node ? *node : json(nullptr), "agent");
    AgentState a;
    a.id = id_of(at(raw, "id"), "agent.id");
    a.role = text_of(at(raw, "role"), "agent.role", 40, true);
    a.name = text_of(at(raw, "name"), "agent.name", 40, false);
    a.personality = text_of(at(raw, "personality"), "agent.personality", 280, false);
    a.goal = text_of(at(raw, "goal"), "agent.goal", 200, false);
    a.faction = text_of(at(raw, "faction"), "agent.faction", 40, false);
    a.activity = text_of(at(raw, "activity"), "agent.activity", 160, false);
    a.current_tactic = text_of(at(raw, "currentTactic"), "agent.currentTactic", 64, false);
    a.health = unit_of(at(raw, "health"), "agent.health");
    a.stamina = unit_of(at(raw, "stamina"), "agent.stamina");
    a.pos = vec_of(at(raw, "position"), "agent.position");
    if (auto id = at(raw, "currentTargetId")) a.current_target_id = id_of(id, "agent.currentTargetId");
    a.seconds_on_tactic = nonneg_of(at(raw, "secondsOnTactic"), "agent.secondsOnTactic");
    a.memory = string_list(at(raw, "memory"), "agent.memory", 4, 160);
    return a;
}
Entity entity_of(const json& node, const std::string& path, const std::string& fallback) {
    auto raw = need_obj(node, path);
    Entity e;
    e.id = id_of(at(raw, "id"), path + ".id");
    e.kind = fallback;
    if (auto kind = at(raw, "kind")) {
        if (!kind->is_string()) throw err_bad(400, path + ".kind 必须是 player、npc、creature、prop、hazard、interest");
        e.kind = kind->get<std::string>();
        if (e.kind != "player" && e.kind != "npc" && e.kind != "creature" && e.kind != "prop" && e.kind != "hazard" && e.kind != "interest")
            throw err_bad(400, path + ".kind 必须是 player、npc、creature、prop、hazard、interest");
    }
    if (auto rel = at(raw, "relation")) {
        e.relation = rel->is_string() ? rel->get<std::string>() : "";
        if (e.relation != "ally" && e.relation != "enemy" && e.relation != "neutral" && e.relation != "unknown")
            throw err_bad(400, path + ".relation 必须是 ally、enemy、neutral 或 unknown");
    }
    e.name = text_of(at(raw, "name"), path + ".name", 40, false);
    e.faction = text_of(at(raw, "faction"), path + ".faction", 40, false);
    e.activity = text_of(at(raw, "activity"), path + ".activity", 160, false);
    e.distance = nonneg_of(at(raw, "distance"), path + ".distance");
    e.health = unit_of(at(raw, "health"), path + ".health");
    if (auto vis = at(raw, "visible")) {
        if (!vis->is_boolean()) throw err_bad(400, path + ".visible 必须是布尔值");
        e.visible = vis->get<bool>();
    }
    e.pos = vec_of(at(raw, "position"), path + ".position");
    e.tags = string_list(at(raw, "tags"), path + ".tags", 4, 32);
    return e;
}
std::vector<Entity> nearby_of(const json* node) {
    if (!node) return {};
    if (!node->is_array()) throw err_bad(400, "nearby 必须是数组");
    if (node->size() > 12) throw err_bad(400, "nearby 最多 12 个，请在游戏侧先筛掉远处的实体");
    std::vector<Entity> out;
    for (size_t i = 0; i < node->size(); ++i) out.push_back(entity_of((*node)[i], "nearby[" + std::to_string(i) + "]", "npc"));
    return out;
}
Policy policy_of(const json* node) {
    if (!node) return resolve_policy(nullptr);
    return resolve_policy(&need_obj(*node, "policy"));
}
SenseReq sense_of(const json& input) {
    auto raw = need_obj(input, "请求体");
    SenseReq req;
    req.scene = scene_of(at(raw, "scene"));
    req.agent = agent_of(at(raw, "agent"));
    req.nearby = nearby_of(at(raw, "nearby"));
    if (auto player = at(raw, "player")) req.player = entity_of(*player, "player", "player");
    if (!at(raw, "tactics")) {
        auto tactics = tactics_for_role(req.agent.role);
        if (!tactics) throw err_bad(400, "角色 \"" + req.agent.role + "\" 没有预置战术。请传 tactics。预置角色：guard、civilian、predator、companion、ambient");
        req.tactics = *tactics;
    } else req.tactics = option_map(*at(raw, "tactics"), "tactics", 1, 24);
    req.policy = policy_of(at(raw, "policy"));
    return req;
}
std::vector<Presence> presences_of(const json* node) {
    if (!node) return {};
    if (!node->is_array()) throw err_bad(400, "presences 必须是数组");
    if (node->size() > 16) throw err_bad(400, "presences 最多 16 个");
    std::vector<Presence> out;
    for (size_t i = 0; i < node->size(); ++i) {
        auto raw = need_obj((*node)[i], "presences[" + std::to_string(i) + "]");
        Presence p;
        p.id = id_of(at(raw, "id"), "presences[" + std::to_string(i) + "].id");
        p.role = text_of(at(raw, "role"), "presences[" + std::to_string(i) + "].role", 40, true);
        p.activity = text_of(at(raw, "activity"), "presences[" + std::to_string(i) + "].activity", 160, false);
        p.faction = text_of(at(raw, "faction"), "presences[" + std::to_string(i) + "].faction", 40, false);
        p.health = unit_of(at(raw, "health"), "presences[" + std::to_string(i) + "].health");
        out.push_back(p);
    }
    return out;
}
WorldReq world_of(const json& input) {
    auto raw = need_obj(input, "world");
    WorldReq req;
    req.scene = scene_of(at(raw, "scene"));
    req.presences = presences_of(at(raw, "presences"));
    if (auto player = at(raw, "player")) {
        auto obj = need_obj(*player, "player");
        req.player = WorldPlayer{text_of(at(obj, "activity"), "player.activity", 160, false), text_of(at(obj, "dominance"), "player.dominance", 160, false), unit_of(at(obj, "health"), "player.health")};
    }
    req.directives = at(raw, "directives") ? option_map(*at(raw, "directives"), "directives", 1, 24) : world_directives();
    req.policy = policy_of(at(raw, "policy"));
    return req;
}
json scene_value(const Scene& s) {
    json out = {{"place", s.place}};
    if (!s.time_of_day.empty()) out["timeOfDay"] = s.time_of_day;
    if (!s.weather.empty()) out["weather"] = s.weather;
    if (!s.current_directive.empty()) out["currentDirective"] = s.current_directive;
    if (s.seconds_on_directive) out["secondsOnDirective"] = *s.seconds_on_directive;
    if (!s.recent_events.empty()) out["recentEvents"] = s.recent_events;
    return out;
}
void put(json& obj, const char* key, const std::string& value) { if (!value.empty()) obj[key] = value; }
double hypot2(double a, double b) { return std::sqrt(a * a + b * b); }
double measured(const AgentState& agent, const Entity& item) {
    if (item.distance) return *item.distance;
    if (!agent.pos || !item.pos) return 1e9;
    return hypot2(agent.pos->x - item.pos->x, hypot2(agent.pos->y - item.pos->y, agent.pos->z.value_or(0) - item.pos->z.value_or(0)));
}
std::string choice_key(const std::string& id, std::set<std::string>& used) {
    std::string raw;
    for (unsigned char ch : id) raw.push_back(std::isalnum(ch) || ch == '_' ? static_cast<char>(ch) : '_');
    size_t start = 0;
    while (start < raw.size() && !std::isalpha(static_cast<unsigned char>(raw[start]))) ++start;
    auto base = raw.substr(start, 40);
    if (base.empty() || base == "none") base = "entity";
    auto key = base;
    int n = 2;
    while (!used.insert(key).second) {
        key = base.substr(0, std::min<size_t>(36, base.size())) + "_" + std::to_string(n++);
    }
    return key;
}
std::string relation_of(const AgentState& agent, const Entity& item) {
    if (!item.relation.empty()) return item.relation;
    if (!agent.faction.empty() && agent.faction == item.faction) return "ally";
    return "unknown";
}
Roster to_entry(const AgentState& agent, const Entity& item, std::set<std::string>& used) {
    auto distance = measured(agent, item);
    Roster e;
    e.id = item.id;
    e.choice_key = choice_key(item.id, used);
    e.kind = item.kind;
    e.name = item.name.empty() ? item.id : item.name;
    e.faction = item.faction;
    e.relation = relation_of(agent, item);
    if (distance < 1e8) e.distance = std::round(distance * 10.0) / 10.0;
    e.visible = item.visible;
    e.health = item.health;
    e.activity = item.activity.substr(0, std::min<size_t>(120, item.activity.size()));
    e.tags.assign(item.tags.begin(), item.tags.begin() + std::min<size_t>(4, item.tags.size()));
    return e;
}
std::vector<Roster> build_roster(const AgentState& agent, const std::optional<Entity>& player, const std::vector<Entity>& nearby) {
    std::vector<Entity> rows;
    if (player && player->id != agent.id) rows.push_back(*player);
    std::vector<Entity> sorted;
    for (const auto& item : nearby) if (item.id != agent.id && (!player || item.id != player->id)) sorted.push_back(item);
    std::sort(sorted.begin(), sorted.end(), [&](const auto& a, const auto& b) { return measured(agent, a) < measured(agent, b); });
    rows.insert(rows.end(), sorted.begin(), sorted.begin() + std::min<size_t>(12, sorted.size()));
    if (rows.size() > 16) rows.resize(16);
    std::set<std::string> used{"none"};
    std::vector<Roster> out;
    for (const auto& item : rows) out.push_back(to_entry(agent, item, used));
    return out;
}
json public_entry(const Roster& entry) {
    json out = {{"id", entry.choice_key}, {"kind", entry.kind}, {"relation", entry.relation}};
    if (!entry.name.empty() && entry.name != entry.choice_key) out["name"] = entry.name;
    put(out, "faction", entry.faction);
    if (entry.distance) out["distance"] = *entry.distance;
    if (entry.visible) out["visible"] = *entry.visible;
    if (entry.health) out["health"] = *entry.health;
    put(out, "activity", entry.activity);
    if (!entry.tags.empty()) out["tags"] = entry.tags;
    return out;
}
struct Compacted { json state; std::vector<Roster> roster; bool has_player = false; };
Compacted compact_sense(SenseReq req) {
    if (auto prior = prior_for_role(req.agent.role)) {
        if (req.agent.personality.empty()) req.agent.personality = prior->first;
        if (req.agent.goal.empty()) req.agent.goal = prior->second;
    }
    auto roster = build_roster(req.agent, req.player, req.nearby);
    json state = json::object();
    json scene = json::object();
    put(scene, "place", req.scene.place);
    put(scene, "timeOfDay", req.scene.time_of_day);
    put(scene, "weather", req.scene.weather);
    if (!req.scene.recent_events.empty()) scene["recentEvents"] = req.scene.recent_events;
    if (!scene.empty()) state["scene"] = scene;
    json agent = {{"id", req.agent.id}, {"role", req.agent.role}};
    put(agent, "name", req.agent.name);
    put(agent, "personality", req.agent.personality);
    put(agent, "goal", req.agent.goal);
    put(agent, "faction", req.agent.faction);
    put(agent, "activity", req.agent.activity);
    put(agent, "currentTactic", req.agent.current_tactic);
    if (req.agent.health) agent["health"] = *req.agent.health;
    if (req.agent.stamina) agent["stamina"] = *req.agent.stamina;
    if (req.agent.current_target_id && !req.agent.current_target_id->empty()) agent["currentTargetId"] = *req.agent.current_target_id;
    if (req.agent.seconds_on_tactic) agent["secondsOnTactic"] = *req.agent.seconds_on_tactic;
    if (!req.agent.memory.empty()) agent["memory"] = req.agent.memory;
    state["agent"] = agent;
    bool has_player = false;
    json nearby = json::array();
    for (const auto& entry : roster) {
        if (req.player && entry.id == req.player->id) { state["player"] = public_entry(entry); has_player = true; }
        else nearby.push_back(public_entry(entry));
    }
    if (!nearby.empty()) state["nearby"] = nearby;
    return {state, roster, has_player};
}
json compact_world(const WorldReq& req) {
    json state = json::object();
    json scene = json::object();
    put(scene, "place", req.scene.place);
    put(scene, "timeOfDay", req.scene.time_of_day);
    put(scene, "weather", req.scene.weather);
    put(scene, "currentDirective", req.scene.current_directive);
    if (req.scene.seconds_on_directive) scene["secondsOnDirective"] = *req.scene.seconds_on_directive;
    if (!req.scene.recent_events.empty()) scene["recentEvents"] = req.scene.recent_events;
    if (!scene.empty()) state["scene"] = scene;
    if (req.player) {
        json player = json::object();
        put(player, "activity", req.player->activity);
        if (req.player->health) player["health"] = *req.player->health;
        put(player, "dominance", req.player->dominance);
        if (!player.empty()) state["player"] = player;
    }
    if (!req.presences.empty()) {
        json rows = json::array();
        for (const auto& item : req.presences) {
            json row = {{"id", item.id}, {"role", item.role}};
            put(row, "activity", item.activity);
            put(row, "faction", item.faction);
            if (item.health) row["health"] = *item.health;
            rows.push_back(row);
        }
        state["presences"] = rows;
    }
    return state;
}
std::string describe(const Roster& entry) {
    std::string head = !entry.name.empty() && entry.name != entry.id ? entry.name + " (" + entry.id + ")" : entry.id;
    std::string text = head + ", " + entry.kind + ", " + entry.relation + ", ";
    text += entry.distance ? std::to_string(*entry.distance) + "m" : "distance unknown";
    text += ", ";
    text += !entry.visible ? "visibility unknown" : (*entry.visible ? "visible" : "not visible");
    if (entry.health) text += ", health " + std::to_string(*entry.health);
    if (!entry.activity.empty()) text += ", " + entry.activity;
    if (!entry.tags.empty()) {
        text += ", tags ";
        for (size_t i = 0; i < entry.tags.size(); ++i) { if (i) text += ", "; text += entry.tags[i]; }
    }
    return text;
}
json agent_questions(const Pairs& tactics, const std::vector<Roster>& roster, const std::string& role, bool ask_player, bool ask_ally) {
    auto [instructions, criteria] = opening_question(role);
    json questions = {
        {"tactic", {{"type", "choice"}, {"instructions", "Which single tactic should this agent commit to now? Use `agent.role`, `agent.personality`, `agent.goal`, `agent.health`, `agent.currentTactic`, `agent.activity`, `scene`, `player`, and `nearby`. Choose hold when the current tactic still matches the moment. Do not invent a tactic."}, {"criteria", pairs_json(tactics)}}},
        {"threat", {{"type", "score"}, {"instructions", "How much danger is this agent in right now? Match `agent.health`, `player`, and `nearby` to a situation. Score this agent, not the whole scene."}, {"criteria", threat_levels()}}},
        {"interrupt", {{"type", "boolean"}, {"instructions", "Should this agent abort `agent.currentTactic` immediately? Use `agent.secondsOnTactic`, `agent.health`, `player`, and `nearby`."}, {"criteria", pairs_json({{"true", "The assumption behind the current tactic just broke: a new threat, a dying ally, or the target is gone."}, {"false", "The current tactic still fits, or there is no current tactic that needs aborting."}})}}},
        {"opening", {{"type", "boolean"}, {"instructions", instructions}, {"criteria", pairs_json(criteria)}}},
    };
    if (!roster.empty()) {
        json criteria = {{"none", "No specific entity. The moment is about the place or the self, not a lock-on."}};
        for (const auto& entry : roster) criteria[entry.choice_key] = describe(entry);
        questions["target"] = {{"type", "choice"}, {"instructions", "Which entity is the focus of this moment? Choose none when the agent should not lock onto anyone. The option id matches `id` on `player` or `nearby`."}, {"criteria", criteria}};
    }
    if (ask_player) questions["playerHostile"] = {{"type", "boolean"}, {"instructions", "Is `player` about to attack this agent or an ally, as opposed to passing by, talking, or leaving?"}, {"criteria", pairs_json({{"true", "A weapon is out, they are sprinting in, they just struck, or they are clearly hunting."}, {"false", "Sheathed, idle, talking, leaving, or moving past without a threat."}})}};
    if (ask_ally) questions["allyNeedsHelp"] = {{"type", "boolean"}, {"instructions", "Does an ally in `nearby` need this agent's help within the next few seconds?"}, {"criteria", pairs_json({{"true", "An ally is hurt, falling, cornered, or calling for help."}, {"false", "Allies are fine, or none of them need this agent."}})}};
    return questions;
}
json world_questions(const Pairs& directives, bool ask_player) {
    json questions = {
        {"directive", {{"type", "choice"}, {"instructions", "Which single beat should this place play now? Use `scene.currentDirective`, `scene.recentEvents`, `player`, and `presences`. Choose hold_atmosphere when the current beat still fits. Do not invent a beat."}, {"criteria", pairs_json(directives)}}},
        {"tension", {{"type", "score"}, {"instructions", "Where does the place sit right now, as a situation rather than a vague intensity? Use `scene`, `player`, and `presences`."}, {"criteria", tension_levels()}}},
    };
    if (ask_player) questions["overextended"] = {{"type", "boolean"}, {"instructions", "Is the player overextended: too deep, too hurt, or too committed, so the place could punish them?"}, {"criteria", pairs_json({{"true", "The player is deep in, badly hurt, surrounded, or cut off from an easy step back."}, {"false", "The player has room, health, or an obvious way to step back."}})}};
    return questions;
}
std::optional<double> read_unit(const json* node) {
    if (!node) return std::nullopt;
    auto n = as_float(*node);
    if (!n || !std::isfinite(*n) || *n < 0 || *n > 1) return std::nullopt;
    return n;
}
json read_distribution(const json* node) {
    if (!node || !node->is_object()) return nullptr;
    json out = json::object();
    for (auto it = node->begin(); it != node->end(); ++it) if (auto n = as_float(it.value()); n && std::isfinite(*n)) out[it.key()] = *n;
    return out.empty() ? json(nullptr) : out;
}
struct Choice { std::string choice; json probabilities = nullptr; std::optional<double> confidence; };
Choice require_choice(const json& answers, const std::string& id) {
    if (!answers.contains(id) || !answers[id].is_object() || !answers[id].contains("choice") || !answers[id]["choice"].is_string() || answers[id]["choice"].get<std::string>().empty()) {
        std::string keys;
        if (answers.is_object()) for (auto it = answers.begin(); it != answers.end(); ++it) { if (!keys.empty()) keys += ", "; keys += it.key(); }
        throw err_bad(502, "Jev 没有返回 " + id + "。答案键：" + keys);
    }
    Choice c;
    c.choice = answers[id]["choice"].get<std::string>();
    c.probabilities = read_distribution(answers[id].contains("probabilities") ? &answers[id]["probabilities"] : nullptr);
    c.confidence = read_unit(answers[id].contains("confidence") ? &answers[id]["confidence"] : nullptr);
    return c;
}
double require_score(const json& answers, const std::string& id) {
    if (!answers.contains(id) || !answers[id].is_object()) throw err_bad(502, "Jev 没有返回 " + id);
    auto score = as_float(answers[id].value("score", json()));
    if (!score || !std::isfinite(*score)) throw err_bad(502, "Jev 没有返回 " + id);
    return *score;
}
std::optional<double> read_probability(const json& answers, const char* id) {
    if (!answers.contains(id) || !answers[id].is_object()) return std::nullopt;
    if (auto p = read_unit(answers[id].contains("probability") ? &answers[id]["probability"] : nullptr)) return p;
    return read_unit(answers[id].contains("noul") ? &answers[id]["noul"] : nullptr);
}
std::optional<double> read_meta(const json& metadata, const std::string& id) {
    std::vector<const json*> buckets;
    if (metadata.contains("typesafe")) buckets.push_back(&metadata["typesafe"]);
    if (metadata.contains("typesafe-ai")) buckets.push_back(&metadata["typesafe-ai"]);
    if (metadata.contains("gateway") && metadata["gateway"].is_object() && metadata["gateway"].contains("typesafe")) buckets.push_back(&metadata["gateway"]["typesafe"]);
    for (auto bucket : buckets) {
        if (bucket && bucket->is_object() && bucket->contains("confidence") && (*bucket)["confidence"].is_object()) {
            if (auto value = read_unit((*bucket)["confidence"].contains(id) ? &(*bucket)["confidence"][id] : nullptr)) return value;
        }
    }
    return std::nullopt;
}
std::optional<double> margin_of(const json& probabilities) {
    if (!probabilities.is_object() || probabilities.empty()) return std::nullopt;
    std::vector<double> values;
    for (auto it = probabilities.begin(); it != probabilities.end(); ++it) if (auto n = as_float(it.value()); n && std::isfinite(*n)) values.push_back(*n);
    if (values.empty()) return std::nullopt;
    std::sort(values.begin(), values.end(), std::greater<>());
    double second = values.size() > 1 ? values[1] : 0;
    return std::round((values[0] - second) * 100.0) / 100.0;
}
std::pair<std::optional<double>, std::string> resolve_confidence(std::optional<double> answer, const json& metadata, const std::string& id, const json& probabilities) {
    if (answer) return {answer, "answer"};
    if (auto meta = read_meta(metadata, id)) return {meta, "typesafe"};
    if (auto margin = margin_of(probabilities)) return {margin, "margin"};
    return {std::nullopt, "none"};
}
std::string model_target(const json& answers, const std::vector<Roster>& roster) {
    try {
        auto target = require_choice(answers, "target");
        if (target.choice == "none") return "none";
        for (const auto& entry : roster) if (entry.choice_key == target.choice) return entry.id;
    } catch (const IntuitionError&) { return "none"; }
    return "none";
}
json null_or(const std::optional<std::string>& value) { return value ? json(*value) : json(nullptr); }
json null_or_num(const std::optional<double>& value) { return value ? json(*value) : json(nullptr); }
bool has_key(const Pairs& pairs, const std::string& key) { return std::any_of(pairs.begin(), pairs.end(), [&](const auto& item) { return item.first == key; }); }
long long elapsed_ms(std::chrono::steady_clock::time_point started) {
    return std::chrono::duration_cast<std::chrono::milliseconds>(std::chrono::steady_clock::now() - started).count();
}
json compose_agent(const SenseReq& req, const std::vector<Roster>& roster, bool has_player, bool ask_ally, const JudgeResult& result, std::chrono::steady_clock::time_point started) {
    auto tactic = require_choice(result.answers, "tactic");
    if (!has_key(req.tactics, tactic.choice)) throw err_bad(502, "Jev 返回了未声明的战术 " + tactic.choice);
    auto threat = require_score(result.answers, "threat");
    auto interrupt = classify_interrupt(read_probability(result.answers, "interrupt"), req.policy.interrupt_at);
    auto [confidence, source] = resolve_confidence(tactic.confidence, result.provider_metadata, "tactic", tactic.probabilities);
    std::optional<std::string> hold = has_key(req.tactics, kHoldTactic) ? std::optional<std::string>(kHoldTactic) : std::nullopt;
    auto decision = decide_disposition(req.agent.current_tactic, tactic.choice, confidence, interrupt, hold, req.policy.switch_confidence);
    auto model = model_target(result.answers, roster);
    std::vector<TargetCandidate> candidates;
    for (const auto& entry : roster) candidates.push_back({entry.id, entry.relation, entry.distance, entry.health, entry.kind});
    auto bound = bind_target(decision.executing, model, candidates);
    if ((decision.disposition == "continue" || decision.disposition == "hold") && tactic_needs_target(decision.executing) && req.agent.current_target_id && !req.agent.current_target_id->empty()) {
        for (const auto& entry : roster) if (entry.id == *req.agent.current_target_id) { bound = {*req.agent.current_target_id, "kept"}; break; }
    }
    auto suggested_name = hold && tactic.choice == *hold && !req.agent.current_tactic.empty() ? req.agent.current_tactic : tactic.choice;
    auto suggested = bind_target(suggested_name, model, candidates);
    json out = {
        {"schemaVersion", 1}, {"agentId", req.agent.id}, {"role", req.agent.role},
        {"disposition", decision.disposition}, {"tactic", decision.executing}, {"suggestedTactic", tactic.choice},
        {"targetId", null_or(bound.target_id)}, {"suggestedTargetId", null_or(suggested.target_id)}, {"targetSource", bound.target_source},
        {"because", decision.because}, {"interrupt", interrupt},
        {"opening", classify_boolean(read_probability(result.answers, "opening"), req.policy.uncertain_margin)},
        {"openingKind", opening_kind_for(req.agent.role)},
        {"playerHostile", has_player ? classify_boolean(read_probability(result.answers, "playerHostile"), req.policy.uncertain_margin) : Tri::False},
        {"allyNeedsHelp", ask_ally ? classify_boolean(read_probability(result.answers, "allyNeedsHelp"), req.policy.uncertain_margin) : Tri::False},
        {"threat", {{"score", score_band(threat, threat_levels()).score}, {"level", score_band(threat, threat_levels()).level}, {"label", score_band(threat, threat_levels()).label}}},
        {"confidence", null_or_num(confidence)}, {"confidenceSource", source}, {"probabilities", tactic.probabilities},
        {"modelId", result.model_id.empty() ? model_id() : result.model_id}, {"latencyMs", elapsed_ms(started)},
    };
    return out;
}
json compose_world(const WorldReq& req, const JudgeResult& result, std::chrono::steady_clock::time_point started) {
    auto directive = require_choice(result.answers, "directive");
    if (!has_key(req.directives, directive.choice)) throw err_bad(502, "Jev 返回了未声明的拍子 " + directive.choice);
    auto tension = require_score(result.answers, "tension");
    auto [confidence, source] = resolve_confidence(directive.confidence, result.provider_metadata, "directive", directive.probabilities);
    std::optional<std::string> hold = has_key(req.directives, kHoldDirective) ? std::optional<std::string>(kHoldDirective) : std::nullopt;
    auto decision = decide_disposition(req.scene.current_directive, directive.choice, confidence, Tri::False, hold, req.policy.switch_confidence);
    auto band = score_band(tension, tension_levels());
    return {
        {"schemaVersion", 1}, {"disposition", decision.disposition}, {"directive", decision.executing}, {"suggestedDirective", directive.choice},
        {"because", decision.because}, {"tension", {{"score", band.score}, {"level", band.level}, {"label", band.label}}},
        {"playerOverextended", req.player ? classify_boolean(read_probability(result.answers, "overextended"), req.policy.uncertain_margin) : Tri::False},
        {"confidence", null_or_num(confidence)}, {"confidenceSource", source}, {"probabilities", directive.probabilities},
        {"modelId", result.model_id.empty() ? model_id() : result.model_id}, {"latencyMs", elapsed_ms(started)},
    };
}
json incapacitated(const SenseReq& req, std::chrono::steady_clock::time_point started) {
    auto band = score_band(3, threat_levels());
    return {
        {"schemaVersion", 1}, {"agentId", req.agent.id}, {"role", req.agent.role}, {"disposition", "incapacitated"},
        {"tactic", "none"}, {"suggestedTactic", "none"}, {"targetId", nullptr}, {"suggestedTargetId", nullptr}, {"targetSource", "none"},
        {"because", "incapacitated"}, {"interrupt", Tri::False}, {"opening", Tri::False}, {"openingKind", opening_kind_for(req.agent.role)},
        {"playerHostile", Tri::False}, {"allyNeedsHelp", Tri::False}, {"threat", {{"score", band.score}, {"level", band.level}, {"label", band.label}}},
        {"confidence", nullptr}, {"confidenceSource", "none"}, {"probabilities", nullptr}, {"modelId", "skipped"}, {"latencyMs", elapsed_ms(started)},
    };
}
json sense_agent_req(SenseReq req, const Judge& judge) {
    auto started = std::chrono::steady_clock::now();
    if (req.agent.health && *req.agent.health <= 0) return incapacitated(req, started);
    auto compact = compact_sense(req);
    bool ask_ally = std::any_of(compact.roster.begin(), compact.roster.end(), [](const auto& e) { return e.relation == "ally"; });
    auto result = judge(compact.state, agent_questions(req.tactics, compact.roster, req.agent.role, compact.has_player, ask_ally));
    return compose_agent(req, compact.roster, compact.has_player, ask_ally, result, started);
}
json sense_world_req(const WorldReq& req, const Judge& judge) {
    auto started = std::chrono::steady_clock::now();
    auto result = judge(compact_world(req), world_questions(req.directives, req.player.has_value()));
    return compose_world(req, result, started);
}
}  // namespace

nlohmann::json sense_agent(const nlohmann::json& input, const Judge& judge) { return sense_agent_req(sense_of(input), judge); }
nlohmann::json sense_world(const nlohmann::json& input, const Judge& judge) { return sense_world_req(world_of(input), judge); }
nlohmann::json sense_tick(const nlohmann::json& input, const Judge& judge) {
    auto started = std::chrono::steady_clock::now();
    auto raw = need_obj(input, "请求体");
    auto scene = scene_of(at(raw, "scene"));
    if (raw.contains("agents") && !raw["agents"].is_null() && !raw["agents"].is_array()) throw err_bad(400, "agents 必须是数组");
    json agents_raw = raw.contains("agents") && raw["agents"].is_array() ? raw["agents"] : json::array();
    if (agents_raw.size() > 16) throw err_bad(400, "单次 tick 最多 16 个 agent");
    std::vector<SenseReq> agents;
    for (size_t i = 0; i < agents_raw.size(); ++i) {
        json obj = need_obj(agents_raw[i], "agents[" + std::to_string(i) + "]");
        obj["scene"] = scene_value(scene);
        agents.push_back(sense_of(obj));
    }
    std::optional<WorldReq> world;
    if (raw.contains("world") && raw["world"].is_boolean() && raw["world"].get<bool>()) world = world_of(json{{"scene", scene_value(scene)}});
    else if (raw.contains("world") && raw["world"].is_object()) {
        json obj = raw["world"];
        obj["scene"] = scene_value(scene);
        world = world_of(obj);
    } else if (raw.contains("world") && !raw["world"].is_null() && !raw["world"].is_boolean()) throw err_bad(400, "world 必须是对象或布尔值");
    if (agents.empty() && !world) throw err_bad(400, "tick 至少要有一个 agent，或把 world 设为 true");
    int concurrency = 4;
    if (auto c = at(raw, "concurrency")) {
        auto n = as_float(*c);
        if (!n || std::trunc(*n) != *n || *n < 1 || *n > 8) throw err_bad(400, "concurrency 必须是 1 到 8 的整数");
        concurrency = static_cast<int>(*n);
    }
    json agent_out = json::array();
    agent_out.get_ref<json::array_t&>().resize(agents.size(), nullptr);
    json world_out = nullptr;
    std::mutex mu;
    std::vector<std::thread> threads;
    auto run = [&](auto job) {
        threads.emplace_back([&, job] { job(); });
        if (static_cast<int>(threads.size()) >= concurrency) { threads.front().join(); threads.erase(threads.begin()); }
    };
    if (world) run([&] {
        json value;
        try { value = sense_world_req(*world, judge); }
        catch (const IntuitionError& err) { value = {{"error", err.what()}, {"status", err.status}}; }
        std::lock_guard lock(mu);
        world_out = value;
    });
    for (size_t i = 0; i < agents.size(); ++i) run([&, i] {
        json value;
        try { value = sense_agent_req(agents[i], judge); }
        catch (const IntuitionError& err) { value = {{"agentId", agents[i].agent.id}, {"error", err.what()}, {"status", err.status}}; }
        std::lock_guard lock(mu);
        agent_out[i] = value;
    });
    for (auto& thread : threads) thread.join();
    return {{"schemaVersion", 1}, {"scene", {{"place", scene.place}}}, {"world", world_out}, {"agents", agent_out}, {"latencyMs", elapsed_ms(started)}};
}

}  // namespace jev
