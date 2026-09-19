namespace Jev;

internal static class Compact
{
    public static (JsonObject State, List<RosterEntry> Roster, bool HasPlayer) Sense(SenseReq req)
    {
        var agent = req.Agent;
        if (Roles.PriorForRole(agent.Role) is { } prior)
        {
            if (agent.Personality.Length == 0) agent.Personality = prior.Personality;
            if (agent.Goal.Length == 0) agent.Goal = prior.Goal;
        }
        var roster = BuildRoster(agent, req.Player, req.Nearby);
        var state = new JsonObject();
        var scene = OmitScene(req.Scene, false);
        if (scene.Count > 0) state["scene"] = scene;
        state["agent"] = OmitAgent(agent);
        var hasPlayer = false;
        var nearby = new JsonArray();
        foreach (var entry in roster)
        {
            if (req.Player is not null && entry.Id == req.Player.Id)
            {
                state["player"] = PublicEntry(entry);
                hasPlayer = true;
            }
            else nearby.Add(PublicEntry(entry));
        }
        if (nearby.Count > 0) state["nearby"] = nearby;
        return (state, roster, hasPlayer);
    }

    public static JsonObject World(WorldReq req)
    {
        var state = new JsonObject();
        var scene = OmitScene(req.Scene, true);
        if (scene.Count > 0) state["scene"] = scene;
        if (req.Player is { } player)
        {
            var row = new JsonObject();
            Put(row, "activity", player.Activity);
            if (player.Health is double health) row["health"] = health;
            Put(row, "dominance", player.Dominance);
            if (row.Count > 0) state["player"] = row;
        }
        if (req.Presences.Count > 0)
        {
            var rows = new JsonArray();
            foreach (var item in req.Presences)
            {
                var row = new JsonObject { ["id"] = item.Id, ["role"] = item.Role };
                Put(row, "activity", item.Activity);
                Put(row, "faction", item.Faction);
                if (item.Health is double health) row["health"] = health;
                rows.Add(row);
            }
            state["presences"] = rows;
        }
        return state;
    }

    private static JsonObject OmitScene(Scene scene, bool world)
    {
        var outMap = new JsonObject();
        Put(outMap, "place", scene.Place);
        Put(outMap, "timeOfDay", scene.TimeOfDay);
        Put(outMap, "weather", scene.Weather);
        if (world)
        {
            Put(outMap, "currentDirective", scene.CurrentDirective);
            if (scene.SecondsOnDirective is double n) outMap["secondsOnDirective"] = n;
        }
        if (scene.RecentEvents.Count > 0) outMap["recentEvents"] = new JsonArray(scene.RecentEvents.Select(item => (JsonNode)item).ToArray());
        return outMap;
    }

    private static JsonObject OmitAgent(Agent agent)
    {
        var outMap = new JsonObject { ["id"] = agent.Id, ["role"] = agent.Role };
        Put(outMap, "name", agent.Name);
        Put(outMap, "personality", agent.Personality);
        Put(outMap, "goal", agent.Goal);
        Put(outMap, "faction", agent.Faction);
        Put(outMap, "activity", agent.Activity);
        Put(outMap, "currentTactic", agent.CurrentTactic);
        if (agent.Health is double health) outMap["health"] = health;
        if (agent.Stamina is double stamina) outMap["stamina"] = stamina;
        if (!string.IsNullOrEmpty(agent.CurrentTargetId)) outMap["currentTargetId"] = agent.CurrentTargetId;
        if (agent.SecondsOnTactic is double seconds) outMap["secondsOnTactic"] = seconds;
        if (agent.Memory.Count > 0) outMap["memory"] = new JsonArray(agent.Memory.Select(item => (JsonNode)item).ToArray());
        return outMap;
    }

    public static void Put(JsonObject map, string key, string value)
    {
        if (value.Length > 0) map[key] = value;
    }

    private static List<RosterEntry> BuildRoster(Agent agent, Entity? player, List<Entity> nearby)
    {
        var rows = new List<Entity>();
        if (player is not null && player.Id != agent.Id) rows.Add(player);
        rows.AddRange(nearby.Where(item => item.Id != agent.Id && (player is null || item.Id != player.Id)).OrderBy(item => Measured(agent, item)).Take(12));
        var used = new HashSet<string> { "none" };
        return rows.Take(16).Select(item => ToEntry(agent, item, used)).ToList();
    }

    private static RosterEntry ToEntry(Agent agent, Entity item, HashSet<string> used)
    {
        var distance = Measured(agent, item);
        var activity = item.Activity;
        if (activity.Length > 120) activity = activity[..120];
        return new RosterEntry
        {
            Id = item.Id,
            ChoiceKey = ChoiceKey(item.Id, used),
            Kind = item.Kind,
            Name = item.Name.Length == 0 ? item.Id : item.Name,
            Faction = item.Faction,
            Relation = RelationOf(agent, item),
            Distance = distance < 1e8 ? Math.Round(distance * 10) / 10 : null,
            Visible = item.Visible,
            Health = item.Health,
            Activity = activity,
            Tags = item.Tags.Take(4).ToList(),
        };
    }

    private static string RelationOf(Agent agent, Entity item)
    {
        if (item.Relation.Length > 0) return item.Relation;
        if (agent.Faction.Length > 0 && agent.Faction == item.Faction) return "ally";
        return "unknown";
    }

    private static double Measured(Agent agent, Entity item)
    {
        if (item.Distance is double distance) return distance;
        if (agent.Pos is null || item.Pos is null) return 1e9;
        var az = agent.Pos.Z ?? 0;
        var bz = item.Pos.Z ?? 0;
        return Hypot(agent.Pos.X - item.Pos.X, Hypot(agent.Pos.Y - item.Pos.Y, az - bz));
    }

    private static double Hypot(double a, double b) => Math.Sqrt(a * a + b * b);

    private static string ChoiceKey(string id, HashSet<string> used)
    {
        var raw = new string(id.Select(ch => char.IsAsciiLetterOrDigit(ch) || ch == '_' ? ch : '_').ToArray());
        var start = 0;
        while (start < raw.Length && !char.IsAsciiLetter(raw[start])) start++;
        var baseKey = raw[start..];
        if (baseKey.Length > 40) baseKey = baseKey[..40];
        if (baseKey.Length == 0 || baseKey == "none") baseKey = "entity";
        var key = baseKey;
        var n = 2;
        while (!used.Add(key))
        {
            var prefix = baseKey.Length > 36 ? baseKey[..36] : baseKey;
            key = $"{prefix}_{n++}";
        }
        return key;
    }

    private static JsonObject PublicEntry(RosterEntry entry)
    {
        var outMap = new JsonObject { ["id"] = entry.ChoiceKey, ["kind"] = entry.Kind, ["relation"] = entry.Relation };
        if (entry.Name.Length > 0 && entry.Name != entry.ChoiceKey) outMap["name"] = entry.Name;
        Put(outMap, "faction", entry.Faction);
        if (entry.Distance is double distance) outMap["distance"] = distance;
        if (entry.Visible is bool visible) outMap["visible"] = visible;
        if (entry.Health is double health) outMap["health"] = health;
        Put(outMap, "activity", entry.Activity);
        if (entry.Tags.Count > 0) outMap["tags"] = new JsonArray(entry.Tags.Select(tag => (JsonNode)tag).ToArray());
        return outMap;
    }
}

internal static class Questions
{
    public static JsonObject Agent(List<(string Key, string Text)> tactics, List<RosterEntry> roster, string role, bool askPlayer, bool askAlly)
    {
        var (instructions, criteria) = Roles.OpeningQuestion(role);
        var questions = new JsonObject
        {
            ["tactic"] = new JsonObject
            {
                ["type"] = "choice",
                ["instructions"] = "Which single tactic should this agent commit to now? Use `agent.role`, `agent.personality`, `agent.goal`, `agent.health`, `agent.currentTactic`, `agent.activity`, `scene`, `player`, and `nearby`. Choose hold when the current tactic still matches the moment. Do not invent a tactic.",
                ["criteria"] = Roles.ToObject(tactics),
            },
            ["threat"] = new JsonObject
            {
                ["type"] = "score",
                ["instructions"] = "How much danger is this agent in right now? Match `agent.health`, `player`, and `nearby` to a situation. Score this agent, not the whole scene.",
                ["criteria"] = new JsonArray(Roles.ThreatLevels().Select(item => (JsonNode)item).ToArray()),
            },
            ["interrupt"] = new JsonObject
            {
                ["type"] = "boolean",
                ["instructions"] = "Should this agent abort `agent.currentTactic` immediately? Use `agent.secondsOnTactic`, `agent.health`, `player`, and `nearby`.",
                ["criteria"] = Roles.ToObject([
                    ("true", "The assumption behind the current tactic just broke: a new threat, a dying ally, or the target is gone."),
                    ("false", "The current tactic still fits, or there is no current tactic that needs aborting."),
                ]),
            },
            ["opening"] = new JsonObject { ["type"] = "boolean", ["instructions"] = instructions, ["criteria"] = Roles.ToObject(criteria) },
        };
        if (roster.Count > 0)
        {
            var target = new JsonObject { ["none"] = "No specific entity. The moment is about the place or the self, not a lock-on." };
            foreach (var entry in roster) target[entry.ChoiceKey] = Describe(entry);
            questions["target"] = new JsonObject
            {
                ["type"] = "choice",
                ["instructions"] = "Which entity is the focus of this moment? Choose none when the agent should not lock onto anyone. The option id matches `id` on `player` or `nearby`.",
                ["criteria"] = target,
            };
        }
        if (askPlayer)
        {
            questions["playerHostile"] = new JsonObject
            {
                ["type"] = "boolean",
                ["instructions"] = "Is `player` about to attack this agent or an ally, as opposed to passing by, talking, or leaving?",
                ["criteria"] = Roles.ToObject([
                    ("true", "A weapon is out, they are sprinting in, they just struck, or they are clearly hunting."),
                    ("false", "Sheathed, idle, talking, leaving, or moving past without a threat."),
                ]),
            };
        }
        if (askAlly)
        {
            questions["allyNeedsHelp"] = new JsonObject
            {
                ["type"] = "boolean",
                ["instructions"] = "Does an ally in `nearby` need this agent's help within the next few seconds?",
                ["criteria"] = Roles.ToObject([
                    ("true", "An ally is hurt, falling, cornered, or calling for help."),
                    ("false", "Allies are fine, or none of them need this agent."),
                ]),
            };
        }
        return questions;
    }

    public static JsonObject World(List<(string Key, string Text)> directives, bool askPlayer)
    {
        var questions = new JsonObject
        {
            ["directive"] = new JsonObject
            {
                ["type"] = "choice",
                ["instructions"] = "Which single beat should this place play now? Use `scene.currentDirective`, `scene.recentEvents`, `player`, and `presences`. Choose hold_atmosphere when the current beat still fits. Do not invent a beat.",
                ["criteria"] = Roles.ToObject(directives),
            },
            ["tension"] = new JsonObject
            {
                ["type"] = "score",
                ["instructions"] = "Where does the place sit right now, as a situation rather than a vague intensity? Use `scene`, `player`, and `presences`.",
                ["criteria"] = new JsonArray(Roles.TensionLevels().Select(item => (JsonNode)item).ToArray()),
            },
        };
        if (askPlayer)
        {
            questions["overextended"] = new JsonObject
            {
                ["type"] = "boolean",
                ["instructions"] = "Is the player overextended: too deep, too hurt, or too committed, so the place could punish them?",
                ["criteria"] = Roles.ToObject([
                    ("true", "The player is deep in, badly hurt, surrounded, or cut off from an easy step back."),
                    ("false", "The player has room, health, or an obvious way to step back."),
                ]),
            };
        }
        return questions;
    }

    private static string Describe(RosterEntry entry)
    {
        var parts = new List<string> { entry.Name.Length > 0 && entry.Name != entry.Id ? $"{entry.Name} ({entry.Id})" : entry.Id, entry.Kind, entry.Relation };
        parts.Add(entry.Distance is double distance ? $"{distance}m" : "distance unknown");
        parts.Add(entry.Visible switch { true => "visible", false => "not visible", _ => "visibility unknown" });
        if (entry.Health is double health) parts.Add("health " + health);
        if (entry.Activity.Length > 0) parts.Add(entry.Activity);
        if (entry.Tags.Count > 0) parts.Add("tags " + string.Join(", ", entry.Tags));
        return string.Join(", ", parts);
    }
}
