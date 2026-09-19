namespace Jev;

public static class Gateway
{
    public const string Url = "https://ai-gateway.vercel.sh/v4/ai/evaluation-model";

    public static string ModelId()
    {
        var id = Environment.GetEnvironmentVariable("JEV_MODEL")?.Trim();
        return string.IsNullOrEmpty(id) ? "typesafe-ai/jev" : id;
    }

    public static JudgeResult Judge(JsonNode state, JsonNode questions)
    {
        var key = Environment.GetEnvironmentVariable("AI_GATEWAY_API_KEY")?.Trim() ?? "";
        if (key.Length == 0) throw Errors.Bad(500, "缺少环境变量 AI_GATEWAY_API_KEY");
        var timeout = PositiveInt("JEV_TIMEOUT_MS", 20_000);
        var retries = NonNegativeInt("JEV_MAX_RETRIES", 1);
        var body = new JsonObject
        {
            ["state"] = state.DeepClone(),
            ["questions"] = questions.DeepClone(),
            ["providerOptions"] = new JsonObject { ["gateway"] = new JsonObject { ["zeroDataRetention"] = true } },
        };
        Exception? last = Errors.Bad(502, "Jev 调用失败");
        for (var attempt = 0; attempt <= retries; attempt++)
        {
            try { return Post(key, body, timeout); }
            catch (IntuitionException err)
            {
                last = err;
                var retry = err.Status is 408 or 429 or >= 500 and < 600;
                if (err.Status is 401 or 403 or 504) retry = err.Status == 504;
                if (!retry || attempt == retries) throw;
            }
        }
        throw last;
    }

    private static JudgeResult Post(string key, JsonObject body, int timeoutMs)
    {
        using var client = new HttpClient { Timeout = TimeSpan.FromMilliseconds(timeoutMs) };
        using var request = new HttpRequestMessage(HttpMethod.Post, Url);
        request.Headers.TryAddWithoutValidation("Authorization", "Bearer " + key);
        request.Headers.TryAddWithoutValidation("ai-model-id", ModelId());
        request.Headers.TryAddWithoutValidation("ai-evaluation-model-specification-version", "4");
        request.Headers.TryAddWithoutValidation("ai-gateway-protocol-version", "0.0.1");
        request.Headers.TryAddWithoutValidation("ai-gateway-auth-method", "api-key");
        request.Content = new StringContent(body.ToJsonString(), System.Text.Encoding.UTF8, "application/json");
        HttpResponseMessage response;
        try { response = client.Send(request); }
        catch (TaskCanceledException) { throw Errors.Bad(504, $"Jev 调用超时（{timeoutMs}ms）"); }
        catch (Exception err) { throw Errors.Bad(502, Errors.Redact(err.Message)); }
        using (response)
        {
            var payload = response.Content.ReadAsStringAsync().GetAwaiter().GetResult();
            var status = (int)response.StatusCode;
            if (status == 401) throw Errors.Bad(401, "AI Gateway 拒绝了这个 key。请检查 AI_GATEWAY_API_KEY 是否仍有效。");
            if (!response.IsSuccessStatusCode)
            {
                if (payload.Contains("credit card", StringComparison.OrdinalIgnoreCase))
                    throw Errors.Bad(403, "Gateway key 是有效的，但这个 Vercel 账号还没有绑定信用卡，AI Gateway 拒绝了调用。到 Vercel 的 AI 页面加上卡并解锁免费额度后再试。");
                var mapped = status is >= 400 and < 600 ? status : 502;
                var raw = payload.Trim().Length == 0 ? "Jev 调用失败" : Errors.Redact(payload.Trim());
                throw Errors.Bad(mapped, raw);
            }
            var decoded = JsonNode.Parse(payload) as JsonObject ?? throw Errors.Bad(502, "Jev 返回不是合法 JSON");
            if (decoded["answers"] is not JsonObject answers) throw Errors.Bad(502, "Jev 返回里没有 answers");
            var meta = decoded["providerMetadata"] as JsonObject ?? new JsonObject();
            return new JudgeResult { Answers = answers, ProviderMetadata = meta, ModelId = ModelId(), Usage = ReadUsage(decoded["usage"]) };
        }
    }

    private static TokenUsage? ReadUsage(JsonNode? node)
    {
        if (node is not JsonObject raw) return null;
        var input = Count(raw["inputTokens"]) ?? Count(raw["promptTokens"]);
        var output = Count(raw["outputTokens"]) ?? Count(raw["completionTokens"]);
        var total = Count(raw["totalTokens"]) ?? (input is double a && output is double b ? a + b : null);
        if (input is null && output is null && total is null) return null;
        return new TokenUsage { InputTokens = input, OutputTokens = output, TotalTokens = total };
    }

    private static double? Count(JsonNode? node) => JsonUtil.TryFloat(node, out var n) && double.IsFinite(n) ? n : null;

    private static int PositiveInt(string name, int fallback)
    {
        var raw = Environment.GetEnvironmentVariable(name);
        return int.TryParse(raw, out var n) && n > 0 ? n : fallback;
    }

    private static int NonNegativeInt(string name, int fallback)
    {
        var raw = Environment.GetEnvironmentVariable(name);
        return int.TryParse(raw, out var n) && n >= 0 ? n : fallback;
    }
}
