using System.Text.RegularExpressions;

namespace Jev;

public delegate JudgeResult Judge(JsonNode state, JsonNode questions);

public sealed class JudgeResult
{
    public JsonObject Answers { get; init; } = new();
    public JsonObject ProviderMetadata { get; init; } = new();
    public string ModelId { get; init; } = "";
    public TokenUsage? Usage { get; init; }
}

public sealed class TokenUsage
{
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)]
    public double? InputTokens { get; set; }
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)]
    public double? OutputTokens { get; set; }
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)]
    public double? TotalTokens { get; set; }
}

public sealed class Impulse
{
    public int SchemaVersion { get; set; } = 1;
    public string AgentId { get; set; } = "";
    public string Role { get; set; } = "";
    public string Disposition { get; set; } = "";
    public string Tactic { get; set; } = "";
    public string SuggestedTactic { get; set; } = "";
    public string? TargetId { get; set; }
    public string? SuggestedTargetId { get; set; }
    public string TargetSource { get; set; } = "none";
    public string Because { get; set; } = "";
    public Tri Interrupt { get; set; }
    public Tri Opening { get; set; }
    public string OpeningKind { get; set; } = "generic";
    public Tri PlayerHostile { get; set; }
    public Tri AllyNeedsHelp { get; set; }
    public ScoreRead Threat { get; set; } = new();
    public double? Confidence { get; set; }
    public string ConfidenceSource { get; set; } = "none";
    public JsonObject? Probabilities { get; set; }
    public string ModelId { get; set; } = "";
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)]
    public TokenUsage? Usage { get; set; }
    public long LatencyMs { get; set; }
}

public sealed class WorldImpulse
{
    public int SchemaVersion { get; set; } = 1;
    public string Disposition { get; set; } = "";
    public string Directive { get; set; } = "";
    public string SuggestedDirective { get; set; } = "";
    public string Because { get; set; } = "";
    public ScoreRead Tension { get; set; } = new();
    public Tri PlayerOverextended { get; set; }
    public double? Confidence { get; set; }
    public string ConfidenceSource { get; set; } = "none";
    public JsonObject? Probabilities { get; set; }
    public string ModelId { get; set; } = "";
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)]
    public TokenUsage? Usage { get; set; }
    public long LatencyMs { get; set; }
}

public static class Engine
{
    public static Impulse SenseAgent(JsonNode input, Judge judge) => SenseAgentReq(Validate.Sense(input), judge);

    public static WorldImpulse SenseWorld(JsonNode input, Judge judge) => SenseWorldReq(Validate.World(input), judge);

    public static JsonObject SenseTick(JsonNode input, Judge judge)
    {
        var started = DateTime.UtcNow;
        var req = Validate.Tick(input);
        var agents = new JsonNode?[req.Agents.Count];
        JsonNode? world = null;
        var jobs = new List<Action>();
        if (req.World is { } worldReq)
            jobs.Add(() =>
            {
                try { world = JsonSerializer.SerializeToNode(SenseWorldReq(worldReq, judge), JsonUtil.Options); }
                catch (IntuitionException err) { world = new JsonObject { ["error"] = err.Message, ["status"] = err.Status }; }
            });
        for (var i = 0; i < req.Agents.Count; i++)
        {
            var index = i;
            var agent = req.Agents[i];
            jobs.Add(() =>
            {
                try { agents[index] = JsonSerializer.SerializeToNode(SenseAgentReq(agent, judge), JsonUtil.Options); }
                catch (IntuitionException err)
                {
                    agents[index] = new JsonObject { ["agentId"] = agent.Agent.Id, ["error"] = err.Message, ["status"] = err.Status };
                }
            });
        }
        Parallel.ForEach(jobs, new ParallelOptions { MaxDegreeOfParallelism = req.Concurrency }, job => job());
        return new JsonObject
        {
            ["schemaVersion"] = 1,
            ["scene"] = new JsonObject { ["place"] = req.Scene.Place },
            ["world"] = world,
            ["agents"] = new JsonArray(agents),
            ["latencyMs"] = (long)(DateTime.UtcNow - started).TotalMilliseconds,
        };
    }

    private static Impulse SenseAgentReq(SenseReq req, Judge judge)
    {
        var started = DateTime.UtcNow;
        if (req.Agent.Health is <= 0) return Incapacitated(req, started);
        var (state, roster, hasPlayer) = Compact.Sense(req);
        var askAlly = roster.Any(entry => entry.Relation == "ally");
        var result = judge(state, Questions.Agent(req.Tactics, roster, req.Agent.Role, hasPlayer, askAlly));
        return Compose.Agent(req, roster, hasPlayer, askAlly, result, started);
    }

    private static WorldImpulse SenseWorldReq(WorldReq req, Judge judge)
    {
        var started = DateTime.UtcNow;
        var result = judge(Compact.World(req), Questions.World(req.Directives, req.Player is not null));
        return Compose.World(req, result, started);
    }

    private static Impulse Incapacitated(SenseReq req, DateTime started) => new()
    {
        AgentId = req.Agent.Id,
        Role = req.Agent.Role,
        Disposition = "incapacitated",
        Tactic = "none",
        SuggestedTactic = "none",
        Because = "incapacitated",
        OpeningKind = Roles.OpeningKindFor(req.Agent.Role),
        Threat = PolicyEngine.ScoreBand(3, Roles.ThreatLevels()),
        ModelId = "skipped",
        LatencyMs = Elapsed(started),
    };

    private static long Elapsed(DateTime started) => (long)(DateTime.UtcNow - started).TotalMilliseconds;

    internal static long Ms(DateTime started) => Elapsed(started);
}

internal sealed class Vec3
{
    public double X, Y;
    public double? Z;
}

internal sealed class Entity
{
    public string Id = "", Kind = "", Name = "", Faction = "", Relation = "", Activity = "";
    public double? Distance, Health;
    public bool? Visible;
    public List<string> Tags = [];
    public Vec3? Pos;
}

internal sealed class Scene
{
    public string Place = "", TimeOfDay = "", Weather = "", CurrentDirective = "";
    public double? SecondsOnDirective;
    public List<string> RecentEvents = [];
}

internal sealed class Agent
{
    public string Id = "", Role = "", Name = "", Personality = "", Goal = "", Faction = "", Activity = "", CurrentTactic = "";
    public double? Health, Stamina, SecondsOnTactic;
    public string? CurrentTargetId;
    public List<string> Memory = [];
    public Vec3? Pos;
}

internal sealed class Presence
{
    public string Id = "", Role = "", Activity = "", Faction = "";
    public double? Health;
}

internal sealed class WorldPlayer
{
    public string Activity = "", Dominance = "";
    public double? Health;
}

internal sealed class SenseReq
{
    public Scene Scene = new();
    public Agent Agent = new();
    public List<Entity> Nearby = [];
    public Entity? Player;
    public List<(string Key, string Text)> Tactics = [];
    public Policy Policy = new();
}

internal sealed class WorldReq
{
    public Scene Scene = new();
    public List<Presence> Presences = [];
    public WorldPlayer? Player;
    public List<(string Key, string Text)> Directives = [];
    public Policy Policy = new();
}

internal sealed class TickReq
{
    public Scene Scene = new();
    public List<SenseReq> Agents = [];
    public WorldReq? World;
    public int Concurrency = 4;
}

internal sealed class RosterEntry
{
    public string Id = "", ChoiceKey = "", Kind = "", Name = "", Faction = "", Relation = "", Activity = "";
    public double? Distance, Health;
    public bool? Visible;
    public List<string> Tags = [];
}

internal sealed class ChoiceAnswer
{
    public string Choice = "";
    public JsonObject? Probabilities;
    public double? Confidence;
}

internal static class Compose
{
    public static Impulse Agent(SenseReq req, List<RosterEntry> roster, bool hasPlayer, bool askAlly, JudgeResult result, DateTime started)
    {
        var tactic = Answers.RequireChoice(result.Answers, "tactic");
        if (req.Tactics.All(item => item.Key != tactic.Choice))
            throw Errors.Bad(502, "Jev 返回了未声明的战术 " + tactic.Choice);
        var threat = Answers.RequireScore(result.Answers, "threat");
        var interrupt = PolicyEngine.ClassifyInterrupt(Answers.ReadProbability(result.Answers, "interrupt"), req.Policy.InterruptAt);
        var (confidence, source) = Answers.ResolveConfidence(tactic.Confidence, result.ProviderMetadata, "tactic", tactic.Probabilities);
        var holdKey = req.Tactics.Any(item => item.Key == Roles.HoldTactic) ? Roles.HoldTactic : null;
        var decision = PolicyEngine.DecideDisposition(req.Agent.CurrentTactic, tactic.Choice, confidence, interrupt, holdKey, req.Policy.SwitchConfidence);
        var modelTarget = Answers.ModelTargetId(result.Answers, roster);
        var candidates = roster.Select(entry => new TargetCandidate { Id = entry.Id, Relation = entry.Relation, Distance = entry.Distance, Health = entry.Health, Kind = entry.Kind }).ToList();
        var bound = PolicyEngine.BindTarget(decision.Executing, modelTarget, candidates);
        var kept = KeepCurrent(decision.Disposition, decision.Executing, req.Agent.CurrentTargetId, roster);
        if (kept is not null) bound = kept;
        var suggestedName = holdKey == tactic.Choice && req.Agent.CurrentTactic.Length > 0 ? req.Agent.CurrentTactic : tactic.Choice;
        var suggested = PolicyEngine.BindTarget(suggestedName, modelTarget, candidates);
        return new Impulse
        {
            AgentId = req.Agent.Id,
            Role = req.Agent.Role,
            Disposition = decision.Disposition,
            Tactic = decision.Executing,
            SuggestedTactic = tactic.Choice,
            TargetId = bound.TargetId,
            SuggestedTargetId = suggested.TargetId,
            TargetSource = bound.TargetSource,
            Because = decision.Because,
            Interrupt = interrupt,
            Opening = PolicyEngine.ClassifyBoolean(Answers.ReadProbability(result.Answers, "opening"), req.Policy.UncertainMargin),
            OpeningKind = Roles.OpeningKindFor(req.Agent.Role),
            PlayerHostile = hasPlayer ? PolicyEngine.ClassifyBoolean(Answers.ReadProbability(result.Answers, "playerHostile"), req.Policy.UncertainMargin) : Tri.False,
            AllyNeedsHelp = askAlly ? PolicyEngine.ClassifyBoolean(Answers.ReadProbability(result.Answers, "allyNeedsHelp"), req.Policy.UncertainMargin) : Tri.False,
            Threat = PolicyEngine.ScoreBand(threat, Roles.ThreatLevels()),
            Confidence = confidence,
            ConfidenceSource = source,
            Probabilities = tactic.Probabilities,
            ModelId = result.ModelId.Length > 0 ? result.ModelId : Gateway.ModelId(),
            Usage = result.Usage,
            LatencyMs = Engine.Ms(started),
        };
    }

    public static WorldImpulse World(WorldReq req, JudgeResult result, DateTime started)
    {
        var directive = Answers.RequireChoice(result.Answers, "directive");
        if (req.Directives.All(item => item.Key != directive.Choice))
            throw Errors.Bad(502, "Jev 返回了未声明的拍子 " + directive.Choice);
        var tension = Answers.RequireScore(result.Answers, "tension");
        var (confidence, source) = Answers.ResolveConfidence(directive.Confidence, result.ProviderMetadata, "directive", directive.Probabilities);
        var holdKey = req.Directives.Any(item => item.Key == Roles.HoldDirective) ? Roles.HoldDirective : null;
        var decision = PolicyEngine.DecideDisposition(req.Scene.CurrentDirective, directive.Choice, confidence, Tri.False, holdKey, req.Policy.SwitchConfidence);
        return new WorldImpulse
        {
            Disposition = decision.Disposition,
            Directive = decision.Executing,
            SuggestedDirective = directive.Choice,
            Because = decision.Because,
            Tension = PolicyEngine.ScoreBand(tension, Roles.TensionLevels()),
            PlayerOverextended = req.Player is null ? Tri.False : PolicyEngine.ClassifyBoolean(Answers.ReadProbability(result.Answers, "overextended"), req.Policy.UncertainMargin),
            Confidence = confidence,
            ConfidenceSource = source,
            Probabilities = directive.Probabilities,
            ModelId = result.ModelId.Length > 0 ? result.ModelId : Gateway.ModelId(),
            Usage = result.Usage,
            LatencyMs = Engine.Ms(started),
        };
    }

    private static Bound? KeepCurrent(string disposition, string tactic, string? current, List<RosterEntry> roster)
    {
        if (disposition is not ("continue" or "hold") || !PolicyEngine.TacticNeedsTarget(tactic) || string.IsNullOrEmpty(current)) return null;
        return roster.Any(entry => entry.Id == current) ? new Bound { TargetId = current, TargetSource = "kept" } : null;
    }
}

internal static class Answers
{
    public static ChoiceAnswer RequireChoice(JsonObject answers, string id)
    {
        var raw = answers[id] as JsonObject;
        var choice = raw?["choice"]?.GetValue<string>() ?? "";
        if (choice.Length == 0)
            throw Errors.Bad(502, $"Jev 没有返回 {id}。答案键：{string.Join(", ", answers.Select(pair => pair.Key))}");
        return new ChoiceAnswer { Choice = choice, Probabilities = ReadDistribution(raw?["probabilities"]), Confidence = ReadUnit(raw?["confidence"]) };
    }

    public static double RequireScore(JsonObject answers, string id)
    {
        if (answers[id] is JsonObject raw && JsonUtil.TryFloat(raw["score"], out var score) && !double.IsNaN(score)) return score;
        throw Errors.Bad(502, "Jev 没有返回 " + id);
    }

    public static double? ReadProbability(JsonObject answers, string id)
    {
        if (answers[id] is not JsonObject raw) return null;
        return ReadUnit(raw["probability"]) ?? ReadUnit(raw["noul"]);
    }

    public static (double? Confidence, string Source) ResolveConfidence(double? answer, JsonObject metadata, string id, JsonObject? probabilities)
    {
        if (answer is not null) return (answer, "answer");
        var meta = ReadMeta(metadata, id);
        if (meta is not null) return (meta, "typesafe");
        var margin = Margin(probabilities);
        return margin is null ? (null, "none") : (margin, "margin");
    }

    private static double? ReadUnit(JsonNode? node)
    {
        if (!JsonUtil.TryFloat(node, out var n) || double.IsNaN(n) || n < 0 || n > 1) return null;
        return n;
    }

    private static JsonObject? ReadDistribution(JsonNode? node)
    {
        if (node is not JsonObject raw) return null;
        var outMap = new JsonObject();
        foreach (var (key, item) in raw)
            if (JsonUtil.TryFloat(item, out var n) && !double.IsNaN(n)) outMap[key] = n;
        return outMap.Count == 0 ? null : outMap;
    }

    private static double? ReadMeta(JsonObject metadata, string id)
    {
        var buckets = new List<JsonNode?>();
        if (metadata.TryGetPropertyValue("typesafe", out var a)) buckets.Add(a);
        if (metadata.TryGetPropertyValue("typesafe-ai", out var b)) buckets.Add(b);
        if (metadata["gateway"] is JsonObject gateway && gateway.TryGetPropertyValue("typesafe", out var c)) buckets.Add(c);
        foreach (var bucket in buckets)
        {
            if (bucket?["confidence"] is JsonObject conf && ReadUnit(conf[id]) is double value) return value;
        }
        return null;
    }

    private static double? Margin(JsonObject? probabilities)
    {
        if (probabilities is null || probabilities.Count == 0) return null;
        var values = probabilities.Select(pair => JsonUtil.TryFloat(pair.Value, out var n) ? n : double.NaN).Where(double.IsFinite).OrderByDescending(n => n).ToList();
        if (values.Count == 0) return null;
        var second = values.Count > 1 ? values[1] : 0;
        return Math.Round((values[0] - second) * 100) / 100;
    }

    public static string ModelTargetId(JsonObject answers, List<RosterEntry> roster)
    {
        ChoiceAnswer target;
        try { target = RequireChoice(answers, "target"); }
        catch (IntuitionException) { return "none"; }
        if (target.Choice == "none") return "none";
        return roster.FirstOrDefault(entry => entry.ChoiceKey == target.Choice)?.Id ?? "none";
    }
}
