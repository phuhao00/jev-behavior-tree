using System.Globalization;
using System.Text.RegularExpressions;

namespace Jev;

internal static class Validate
{
    private static readonly Regex IdPattern = new(@"^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$", RegexOptions.Compiled);
    private static readonly Regex KeyPattern = new(@"^[A-Za-z][A-Za-z0-9_]{0,40}$", RegexOptions.Compiled);
    private static readonly HashSet<string> Kinds = ["player", "npc", "creature", "prop", "hazard", "interest"];
    private static readonly HashSet<string> Relations = ["ally", "enemy", "neutral", "unknown"];

    public static SenseReq Sense(JsonNode input)
    {
        var raw = Obj(input, "请求体");
        var scene = SceneOf(raw["scene"]);
        var agent = AgentOf(raw["agent"]);
        var nearby = NearbyOf(raw["nearby"]);
        Entity? player = null;
        if (raw["player"] is not null) player = EntityOf(raw["player"]!, "player", "player");
        List<(string Key, string Text)> tactics;
        if (raw["tactics"] is null)
        {
            tactics = Roles.TacticsForRole(agent.Role) ?? throw Errors.Bad(400, $"角色 \"{agent.Role}\" 没有预置战术。请传 tactics。预置角色：guard、civilian、predator、companion、ambient");
        }
        else tactics = OptionMap(raw["tactics"]!, "tactics", 1, 24);
        return new SenseReq { Scene = scene, Agent = agent, Nearby = nearby, Player = player, Tactics = tactics, Policy = PolicyOf(raw["policy"]) };
    }

    public static WorldReq World(JsonNode input)
    {
        var raw = Obj(input, "world");
        var scene = SceneOf(raw["scene"]);
        WorldPlayer? player = null;
        if (raw["player"] is JsonObject p)
        {
            player = new WorldPlayer
            {
                Activity = OptionalText(p["activity"], "player.activity", 160),
                Dominance = OptionalText(p["dominance"], "player.dominance", 160),
                Health = OptionalUnit(p["health"], "player.health"),
            };
        }
        else if (raw["player"] is not null) throw Errors.Bad(400, "player 必须是对象");
        var directives = raw["directives"] is null ? Roles.WorldDirectives() : OptionMap(raw["directives"]!, "directives", 1, 24);
        return new WorldReq { Scene = scene, Presences = PresencesOf(raw["presences"]), Player = player, Directives = directives, Policy = PolicyOf(raw["policy"]) };
    }

    public static TickReq Tick(JsonNode input)
    {
        var raw = Obj(input, "请求体");
        var scene = SceneOf(raw["scene"]);
        var agentsRaw = raw["agents"] switch
        {
            null => [],
            JsonArray list => list,
            _ => throw Errors.Bad(400, "agents 必须是数组"),
        };
        if (agentsRaw.Count > 16) throw Errors.Bad(400, "单次 tick 最多 16 个 agent");
        var agents = new List<SenseReq>();
        for (var i = 0; i < agentsRaw.Count; i++)
        {
            var obj = Obj(agentsRaw[i], $"agents[{i}]");
            obj["scene"] = SceneValue(scene);
            agents.Add(Sense(obj));
        }
        WorldReq? world = null;
        if (raw["world"] is JsonValue flag && flag.TryGetValue<bool>(out var on))
        {
            if (on) world = World(new JsonObject { ["scene"] = SceneValue(scene) });
        }
        else if (raw["world"] is not null)
        {
            var obj = Obj(raw["world"], "world");
            obj["scene"] = SceneValue(scene);
            world = World(obj);
        }
        if (agents.Count == 0 && world is null) throw Errors.Bad(400, "tick 至少要有一个 agent，或把 world 设为 true");
        var concurrency = 4;
        if (raw["concurrency"] is not null)
        {
            if (!JsonUtil.TryFloat(raw["concurrency"], out var n) || n != Math.Truncate(n) || n < 1 || n > 8)
                throw Errors.Bad(400, "concurrency 必须是 1 到 8 的整数");
            concurrency = (int)n;
        }
        return new TickReq { Scene = scene, Agents = agents, World = world, Concurrency = concurrency };
    }

    private static Scene SceneOf(JsonNode? input)
    {
        var raw = Obj(input, "scene");
        return new Scene
        {
            Place = RequireText(raw["place"], "scene.place", 200),
            TimeOfDay = OptionalText(raw["timeOfDay"], "scene.timeOfDay", 40),
            Weather = OptionalText(raw["weather"], "scene.weather", 80),
            CurrentDirective = OptionalText(raw["currentDirective"], "scene.currentDirective", 64),
            SecondsOnDirective = OptionalNonNegative(raw["secondsOnDirective"], "scene.secondsOnDirective"),
            RecentEvents = StringList(raw["recentEvents"], "scene.recentEvents", 6, 180),
        };
    }

    private static Agent AgentOf(JsonNode? input)
    {
        var raw = Obj(input, "agent");
        return new Agent
        {
            Id = RequireId(raw["id"], "agent.id"),
            Role = RequireText(raw["role"], "agent.role", 40),
            Name = OptionalText(raw["name"], "agent.name", 40),
            Personality = OptionalText(raw["personality"], "agent.personality", 280),
            Goal = OptionalText(raw["goal"], "agent.goal", 200),
            Faction = OptionalText(raw["faction"], "agent.faction", 40),
            Activity = OptionalText(raw["activity"], "agent.activity", 160),
            CurrentTactic = OptionalText(raw["currentTactic"], "agent.currentTactic", 64),
            Health = OptionalUnit(raw["health"], "agent.health"),
            Stamina = OptionalUnit(raw["stamina"], "agent.stamina"),
            Pos = OptionalVec(raw["position"], "agent.position"),
            CurrentTargetId = OptionalId(raw["currentTargetId"], "agent.currentTargetId"),
            SecondsOnTactic = OptionalNonNegative(raw["secondsOnTactic"], "agent.secondsOnTactic"),
            Memory = StringList(raw["memory"], "agent.memory", 4, 160),
        };
    }

    private static List<Entity> NearbyOf(JsonNode? input)
    {
        if (input is null) return [];
        if (input is not JsonArray list) throw Errors.Bad(400, "nearby 必须是数组");
        if (list.Count > 12) throw Errors.Bad(400, "nearby 最多 12 个，请在游戏侧先筛掉远处的实体");
        return list.Select((item, i) => EntityOf(item!, $"nearby[{i}]", "npc")).ToList();
    }

    private static Entity EntityOf(JsonNode input, string path, string fallbackKind)
    {
        var raw = Obj(input, path);
        var kind = fallbackKind;
        if (raw["kind"] is not null)
        {
            kind = raw["kind"]?.GetValue<string>() ?? "";
            if (!Kinds.Contains(kind)) throw Errors.Bad(400, $"{path}.kind 必须是 player、npc、creature、prop、hazard、interest");
        }
        var relation = "";
        if (raw["relation"] is not null)
        {
            relation = raw["relation"]?.GetValue<string>() ?? "";
            if (!Relations.Contains(relation)) throw Errors.Bad(400, $"{path}.relation 必须是 ally、enemy、neutral 或 unknown");
        }
        return new Entity
        {
            Id = RequireId(raw["id"], path + ".id"),
            Kind = kind,
            Relation = relation,
            Name = OptionalText(raw["name"], path + ".name", 40),
            Faction = OptionalText(raw["faction"], path + ".faction", 40),
            Activity = OptionalText(raw["activity"], path + ".activity", 160),
            Distance = OptionalNonNegative(raw["distance"], path + ".distance"),
            Health = OptionalUnit(raw["health"], path + ".health"),
            Visible = raw["visible"] switch
            {
                null => null,
                JsonValue v when v.TryGetValue<bool>(out var b) => b,
                _ => throw Errors.Bad(400, path + ".visible 必须是布尔值"),
            },
            Pos = OptionalVec(raw["position"], path + ".position"),
            Tags = StringList(raw["tags"], path + ".tags", 4, 32),
        };
    }

    private static List<Presence> PresencesOf(JsonNode? input)
    {
        if (input is null) return [];
        if (input is not JsonArray list) throw Errors.Bad(400, "presences 必须是数组");
        if (list.Count > 16) throw Errors.Bad(400, "presences 最多 16 个");
        var outList = new List<Presence>();
        for (var i = 0; i < list.Count; i++)
        {
            var raw = Obj(list[i], $"presences[{i}]");
            outList.Add(new Presence
            {
                Id = RequireId(raw["id"], $"presences[{i}].id"),
                Role = RequireText(raw["role"], $"presences[{i}].role", 40),
                Activity = OptionalText(raw["activity"], $"presences[{i}].activity", 160),
                Faction = OptionalText(raw["faction"], $"presences[{i}].faction", 40),
                Health = OptionalUnit(raw["health"], $"presences[{i}].health"),
            });
        }
        return outList;
    }

    private static List<(string Key, string Text)> OptionMap(JsonNode input, string path, int min, int max)
    {
        var raw = Obj(input, path);
        if (raw.Count < min || raw.Count > max) throw Errors.Bad(400, $"{path} 需要 {min} 到 {max} 个选项");
        var outList = new List<(string, string)>();
        foreach (var (key, value) in raw)
        {
            if (!KeyPattern.IsMatch(key)) throw Errors.Bad(400, $"{path}.{key} 的 id 需要以字母开头，只能含英文、数字和下划线");
            var text = value?.GetValue<string>()?.Trim() ?? "";
            if (text.Length == 0) throw Errors.Bad(400, $"{path}.{key} 需要一段情境描述，不能为空");
            if (text.Length > 500) throw Errors.Bad(400, $"{path}.{key} 的描述超过 500 字");
            outList.Add((key, text));
        }
        return outList;
    }

    private static Policy PolicyOf(JsonNode? input)
    {
        if (input is null) return PolicyEngine.Resolve(null);
        return PolicyEngine.Resolve(Obj(input, "policy"));
    }

    private static JsonObject SceneValue(Scene scene)
    {
        var outMap = new JsonObject { ["place"] = scene.Place };
        Compact.Put(outMap, "timeOfDay", scene.TimeOfDay);
        Compact.Put(outMap, "weather", scene.Weather);
        Compact.Put(outMap, "currentDirective", scene.CurrentDirective);
        if (scene.SecondsOnDirective is double n) outMap["secondsOnDirective"] = n;
        if (scene.RecentEvents.Count > 0) outMap["recentEvents"] = new JsonArray(scene.RecentEvents.Select(item => (JsonNode)item).ToArray());
        return outMap;
    }

    private static JsonObject Obj(JsonNode? input, string path) =>
        input as JsonObject ?? throw Errors.Bad(400, path + " 必须是对象");

    private static string RequireId(JsonNode? input, string path)
    {
        var text = input?.GetValue<string>() ?? "";
        if (!IdPattern.IsMatch(text)) throw Errors.Bad(400, path + " 需要 1 到 64 位的英文、数字、_ . : -");
        return text;
    }

    private static string? OptionalId(JsonNode? input, string path) => input is null ? null : RequireId(input, path);

    private static string RequireText(JsonNode? input, string path, int max)
    {
        var text = (input?.GetValue<string>() ?? "").Trim();
        if (text.Length == 0) throw Errors.Bad(400, path + " 不能为空");
        if (text.Length > max) throw Errors.Bad(400, path + " 超过 " + max + " 字");
        return text;
    }

    private static string OptionalText(JsonNode? input, string path, int max) => input is null ? "" : RequireText(input, path, max);

    private static double? OptionalUnit(JsonNode? input, string path) => input is null ? null : RequireUnit(input, path);

    private static double RequireUnit(JsonNode input, string path)
    {
        var value = RequireFinite(input, path);
        if (value < 0 || value > 1) throw Errors.Bad(400, path + " 必须在 0 到 1 之间");
        return value;
    }

    private static double? OptionalNonNegative(JsonNode? input, string path) => input is null ? null : RequireNonNegative(input, path);

    private static double RequireNonNegative(JsonNode input, string path)
    {
        var value = RequireFinite(input, path);
        if (value < 0) throw Errors.Bad(400, path + " 不能是负数");
        return value;
    }

    private static double RequireFinite(JsonNode? input, string path)
    {
        if (!JsonUtil.TryFloat(input, out var value) || !double.IsFinite(value)) throw Errors.Bad(400, path + " 必须是有限数字");
        return value;
    }

    private static Vec3? OptionalVec(JsonNode? input, string path) => input is null ? null : VecOf(input, path);

    private static Vec3 VecOf(JsonNode input, string path)
    {
        var raw = Obj(input, path);
        double? z = raw["z"] is null ? null : RequireFinite(raw["z"], path + ".z");
        return new Vec3 { X = RequireFinite(raw["x"], path + ".x"), Y = RequireFinite(raw["y"], path + ".y"), Z = z };
    }

    private static List<string> StringList(JsonNode? input, string path, int max, int chars)
    {
        if (input is null) return [];
        if (input is not JsonArray list) throw Errors.Bad(400, path + " 必须是字符串数组");
        var start = Math.Max(0, list.Count - max);
        var outList = new List<string>();
        for (var i = start; i < list.Count; i++) outList.Add(RequireText(list[i], $"{path}[{i}]", chars));
        return outList;
    }
}
