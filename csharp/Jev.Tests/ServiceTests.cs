namespace Jev;

public class ServiceTests
{
    [Fact]
    public void LowConfidenceHolds()
    {
        var got = PolicyEngine.DecideDisposition("patrol", "engage", 0.4, Tri.False, "hold", 0.72);
        Assert.Equal("hold", got.Disposition);
        Assert.Equal("patrol", got.Executing);
        Assert.Equal("hysteresis", got.Because);
    }

    [Fact]
    public void InterruptSwitches()
    {
        var got = PolicyEngine.DecideDisposition("patrol", "flee", 0.4, Tri.True, "hold", 0.72);
        Assert.Equal("flee", got.Executing);
        Assert.Equal("interrupt", got.Because);
    }

    [Fact]
    public void HoldExpands()
    {
        var got = PolicyEngine.DecideDisposition("patrol", "hold", 0.95, Tri.True, "hold", 0.72);
        Assert.Equal("patrol", got.Executing);
        Assert.Equal("still-fitting", got.Because);
    }

    [Fact]
    public void BindSkipsPatrol() => Assert.Equal("none", PolicyEngine.BindTarget("patrol", "player", []).TargetSource);

    [Fact]
    public void AssistWeakestAlly()
    {
        var got = PolicyEngine.BindTarget("assist", "none",
        [
            new TargetCandidate { Id = "wolf", Relation = "enemy", Distance = 2, Health = 1, Kind = "creature" },
            new TargetCandidate { Id = "mira", Relation = "ally", Distance = 6, Health = 0.2, Kind = "npc" },
            new TargetCandidate { Id = "squire", Relation = "ally", Distance = 3, Health = 0.9, Kind = "npc" },
        ]);
        Assert.Equal("mira", got.TargetId);
        Assert.Equal("geometric-fallback", got.TargetSource);
    }

    [Fact]
    public void DeadAgentSkipsJudge()
    {
        var called = false;
        var impulse = Engine.SenseAgent(Guard(0, "patrol", "wolf"), (_, _) =>
        {
            called = true;
            throw Errors.Bad(500, "should not be called");
        });
        Assert.False(called);
        Assert.Equal("incapacitated", impulse.Disposition);
        Assert.Equal("none", impulse.Tactic);
    }

    [Fact]
    public void LowConfidenceHoldsPatrol()
    {
        var impulse = Engine.SenseAgent(Guard(1, "patrol", "wolf"), Answered("engage", 0.2, 0.1, "player"));
        Assert.Equal("engage", impulse.SuggestedTactic);
        Assert.Equal("patrol", impulse.Tactic);
        Assert.Equal("hold", impulse.Disposition);
        Assert.Equal("hysteresis", impulse.Because);
        Assert.Null(impulse.TargetId);
        Assert.Equal(Tri.True, impulse.PlayerHostile);
        Assert.Equal("typesafe", impulse.ConfidenceSource);
    }

    [Fact]
    public void HighConfidenceLocksModelTarget()
    {
        var impulse = Engine.SenseAgent(Guard(1, "patrol", null), Answered("engage", 0.9, 0.1, "player"));
        Assert.Equal("engage", impulse.Tactic);
        Assert.Equal("confident", impulse.Because);
        Assert.Equal("model", impulse.TargetSource);
        Assert.Equal("player", impulse.TargetId);
    }

    [Fact]
    public void ContinueKeepsCurrentTarget()
    {
        var impulse = Engine.SenseAgent(Guard(1, "engage", "wolf"), Answered("engage", 0.95, 0.1, "player"));
        Assert.Equal("continue", impulse.Disposition);
        Assert.Equal("kept", impulse.TargetSource);
        Assert.Equal("wolf", impulse.TargetId);
        Assert.Equal("player", impulse.SuggestedTargetId);
    }

    [Fact]
    public void WorldHoldsAtmosphere()
    {
        var world = Engine.SenseWorld(JsonNode.Parse("""
        {"scene":{"place":"chapel","currentDirective":"hold_atmosphere","secondsOnDirective":10},"player":{"activity":"walking","health":1,"dominance":"passing"}}
        """)!, (_, _) => new JudgeResult
        {
            Answers = (JsonObject)JsonNode.Parse("""
            {"directive":{"type":"choice","choice":"ambush_now","probabilities":{"ambush_now":0.55,"hold_atmosphere":0.45}},"tension":{"type":"score","score":1.1},"overextended":{"type":"boolean","probability":0.2}}
            """)!,
            ProviderMetadata = (JsonObject)JsonNode.Parse("""{"typesafe":{"confidence":{"directive":0.3}}}""")!,
            ModelId = "typesafe-ai/jev",
        });
        Assert.Equal("hold_atmosphere", world.Directive);
        Assert.Equal("ambush_now", world.SuggestedDirective);
        Assert.Equal("hold", world.Disposition);
        Assert.Equal(Tri.False, world.PlayerOverextended);
    }

    private static JsonObject Guard(double health, string tactic, string? target)
    {
        var agent = new JsonObject
        {
            ["id"] = "rook",
            ["role"] = "guard",
            ["health"] = health,
            ["currentTactic"] = tactic,
            ["secondsOnTactic"] = 4,
        };
        if (target is not null) agent["currentTargetId"] = target;
        return new JsonObject
        {
            ["scene"] = new JsonObject { ["place"] = "test yard" },
            ["agent"] = agent,
            ["player"] = JsonNode.Parse("""{"id":"player","kind":"player","distance":4,"visible":true,"health":1,"relation":"neutral","activity":"sprinting with a blade"}"""),
            ["nearby"] = JsonNode.Parse("""[{"id":"wolf","kind":"creature","distance":9,"health":0.8,"relation":"enemy","activity":"circling"},{"id":"mira","kind":"npc","faction":"chapel","distance":3,"health":0.2,"relation":"ally","activity":"on the ground"}]"""),
        };
    }

    private static Judge Answered(string tactic, double confidence, double interrupt, string target) => (_, _) => new JudgeResult
    {
        Answers = new JsonObject
        {
            ["tactic"] = new JsonObject { ["type"] = "choice", ["choice"] = tactic, ["probabilities"] = new JsonObject { [tactic] = 0.8, ["hold"] = 0.2 } },
            ["target"] = new JsonObject { ["type"] = "choice", ["choice"] = target, ["probabilities"] = new JsonObject { [target] = 0.9, ["none"] = 0.1 } },
            ["threat"] = new JsonObject { ["type"] = "score", ["score"] = 2.2 },
            ["interrupt"] = new JsonObject { ["type"] = "boolean", ["probability"] = interrupt },
            ["opening"] = new JsonObject { ["type"] = "boolean", ["probability"] = 0.2 },
            ["playerHostile"] = new JsonObject { ["type"] = "boolean", ["probability"] = 0.91 },
            ["allyNeedsHelp"] = new JsonObject { ["type"] = "boolean", ["probability"] = 0.2 },
        },
        ProviderMetadata = new JsonObject { ["typesafe"] = new JsonObject { ["confidence"] = new JsonObject { ["tactic"] = confidence, ["threat"] = 0.8 } } },
        ModelId = "typesafe-ai/jev",
    };
}
