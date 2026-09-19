namespace Jev;

public enum Tri
{
    False,
    True,
    Uncertain,
}

public sealed class TriConverter : JsonConverter<Tri>
{
    public override Tri Read(ref Utf8JsonReader reader, Type typeToConvert, JsonSerializerOptions options) =>
        throw new NotSupportedException();

    public override void Write(Utf8JsonWriter writer, Tri value, JsonSerializerOptions options)
    {
        switch (value)
        {
            case Tri.True:
                writer.WriteBooleanValue(true);
                break;
            case Tri.False:
                writer.WriteBooleanValue(false);
                break;
            default:
                writer.WriteStringValue("uncertain");
                break;
        }
    }
}

public sealed class Policy
{
    public double SwitchConfidence { get; set; } = 0.72;
    public double InterruptAt { get; set; } = 0.75;
    public double UncertainMargin { get; set; } = 0.12;
}

public sealed record Decision(string Disposition, string Executing, string Because);

public sealed class TargetCandidate
{
    public string Id { get; set; } = "";
    public string Relation { get; set; } = "";
    public double? Distance { get; set; }
    public double? Health { get; set; }
    public string Kind { get; set; } = "";
}

public sealed class Bound
{
    public string? TargetId { get; set; }
    public string TargetSource { get; set; } = "none";
}

public sealed class ScoreRead
{
    public double Score { get; set; }
    public int Level { get; set; }
    public string Label { get; set; } = "";
}

public static class PolicyEngine
{
    public static Policy Resolve(JsonObject? input)
    {
        var policy = new Policy();
        if (input is null) return policy;
        foreach (var key in new[] { "switchConfidence", "interruptAt", "uncertainMargin" })
        {
            if (!input.TryGetPropertyValue(key, out var raw) || raw is null) continue;
            if (!JsonUtil.TryFloat(raw, out var value) || !double.IsFinite(value) || value < 0 || value > 1)
                throw Errors.Bad(400, $"policy.{key} 必须是 0 到 1 之间的数字");
            switch (key)
            {
                case "switchConfidence": policy.SwitchConfidence = value; break;
                case "interruptAt": policy.InterruptAt = value; break;
                default: policy.UncertainMargin = value; break;
            }
        }
        return policy;
    }

    public static Tri ClassifyBoolean(double? probability, double margin)
    {
        if (probability is not double p || !double.IsFinite(p)) return Tri.Uncertain;
        if (p >= 0.5 + margin) return Tri.True;
        if (p <= 0.5 - margin) return Tri.False;
        return Tri.Uncertain;
    }

    public static Tri ClassifyInterrupt(double? probability, double interruptAt)
    {
        if (probability is not double p || !double.IsFinite(p)) return Tri.Uncertain;
        if (p >= interruptAt) return Tri.True;
        if (p <= 1 - interruptAt) return Tri.False;
        return Tri.Uncertain;
    }

    public static Decision DecideDisposition(string current, string suggested, double? confidence, Tri interrupt, string? holdKey, double switchConfidence)
    {
        current = current.Trim();
        var expanded = holdKey == suggested && current.Length > 0 ? current : suggested;
        if (current.Length == 0) return new Decision("switch", expanded, "first-decision");
        if (expanded == current) return new Decision("continue", current, "still-fitting");
        if (interrupt == Tri.True) return new Decision("switch", expanded, "interrupt");
        if (confidence is null || confidence >= switchConfidence) return new Decision("switch", expanded, "confident");
        return new Decision("hold", current, "hysteresis");
    }

    public static bool TacticNeedsTarget(string tactic) => tactic is not ("hold" or "patrol" or "hide" or "flee");

    public static Bound BindTarget(string tactic, string modelTargetId, IReadOnlyList<TargetCandidate> roster)
    {
        if (!TacticNeedsTarget(tactic)) return new Bound { TargetSource = "none" };
        if (modelTargetId is not ("none" or "") && roster.Any(entry => entry.Id == modelTargetId))
            return new Bound { TargetId = modelTargetId, TargetSource = "model" };
        var fallback = FallbackTarget(tactic, roster);
        if (fallback is not null) return new Bound { TargetId = fallback, TargetSource = "geometric-fallback" };
        return new Bound { TargetSource = "none" };
    }

    public static ScoreRead ScoreBand(double score, IReadOnlyList<string> labels)
    {
        var max = Math.Max(0, labels.Count - 1);
        var clamped = Math.Clamp(score, 0, max);
        var level = (int)Math.Clamp(Math.Round(clamped), 0, max);
        return new ScoreRead
        {
            Score = Math.Round(clamped * 100) / 100,
            Level = level,
            Label = level < labels.Count ? labels[level] : "unknown",
        };
    }

    private static string? FallbackTarget(string tactic, IReadOnlyList<TargetCandidate> roster)
    {
        var ranked = roster.OrderBy(DistOrFar).ToList();
        if (tactic == "assist")
        {
            return ranked.Where(entry => entry.Relation == "ally")
                .OrderBy(entry => entry.Health ?? 1)
                .ThenBy(DistOrFar)
                .Select(entry => entry.Id)
                .FirstOrDefault();
        }
        if (tactic is "investigate" or "interact")
        {
            return ranked.FirstOrDefault(entry => entry.Kind is "interest" or "hazard" or "prop")?.Id
                ?? ranked.FirstOrDefault()?.Id;
        }
        return ranked.FirstOrDefault(entry => entry.Relation == "enemy")?.Id ?? ranked.FirstOrDefault()?.Id;
    }

    private static double DistOrFar(TargetCandidate item) => item.Distance ?? 1e9;
}

public static class JsonUtil
{
    public static readonly JsonSerializerOptions Options = CreateOptions();

    public static bool TryFloat(JsonNode? node, out double value)
    {
        value = 0;
        if (node is not JsonValue raw) return false;
        if (raw.TryGetValue<double>(out value)) return true;
        if (raw.TryGetValue<int>(out var i)) { value = i; return true; }
        if (raw.TryGetValue<long>(out var l)) { value = l; return true; }
        if (raw.TryGetValue<float>(out var f)) { value = f; return true; }
        return false;
    }

    private static JsonSerializerOptions CreateOptions()
    {
        var options = new JsonSerializerOptions
        {
            PropertyNamingPolicy = JsonNamingPolicy.CamelCase,
            Encoder = System.Text.Encodings.Web.JavaScriptEncoder.UnsafeRelaxedJsonEscaping,
        };
        options.Converters.Add(new TriConverter());
        return options;
    }
}
