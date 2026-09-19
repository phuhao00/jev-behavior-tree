using System.Text;
using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Http;
using Microsoft.Extensions.Logging;

namespace Jev;

public static class Program
{
    public static void Main(string[] args)
    {
        Env.Load();
        if (string.IsNullOrWhiteSpace(Environment.GetEnvironmentVariable("AI_GATEWAY_API_KEY")))
        {
            Console.Error.WriteLine("缺少 AI_GATEWAY_API_KEY。复制 .env.example 为 .env 后填入 Vercel AI Gateway 的 key。");
            Environment.Exit(1);
        }
        var host = Environment.GetEnvironmentVariable("HOST")?.Trim();
        if (string.IsNullOrEmpty(host)) host = "127.0.0.1";
        var port = PositivePort(Environment.GetEnvironmentVariable("PORT"), 8790);
        var builder = WebApplication.CreateBuilder(args);
        builder.Logging.ClearProviders();
        builder.WebHost.UseUrls($"http://{host}:{port}");
        var app = builder.Build();
        app.MapMethods("/{**path}", ["OPTIONS"], () => Results.StatusCode(204));
        app.MapGet("/health", () => Results.Json(new
        {
            ok = true,
            service = "jev-intuition",
            language = "csharp",
            model = Gateway.ModelId(),
            endpoints = new[] { "/health", "/v1/impulse", "/v1/world", "/v1/tick" },
        }, JsonUtil.Options));
        app.MapPost("/v1/impulse", (HttpRequest request) => Handle(request, body => JsonSerializer.SerializeToNode(Engine.SenseAgent(body, Gateway.Judge), JsonUtil.Options)!));
        app.MapPost("/v1/world", (HttpRequest request) => Handle(request, body => JsonSerializer.SerializeToNode(Engine.SenseWorld(body, Gateway.Judge), JsonUtil.Options)!));
        app.MapPost("/v1/tick", (HttpRequest request) => Handle(request, body => Engine.SenseTick(body, Gateway.Judge)));
        app.MapFallback(() => Results.Json(new { error = "没有这个接口" }, JsonUtil.Options, statusCode: 404));
        Console.WriteLine($"Jev 直觉服务 (csharp)  http://{host}:{port}");
        Console.WriteLine($"模型 {Gateway.ModelId()}");
        Console.WriteLine("GET /health   POST /v1/impulse   POST /v1/world   POST /v1/tick");
        app.Run();
    }

    private static IResult Handle(HttpRequest request, Func<JsonNode, JsonNode> call)
    {
        try
        {
            using var reader = new StreamReader(request.Body, Encoding.UTF8);
            var text = reader.ReadToEndAsync().GetAwaiter().GetResult();
            if (Encoding.UTF8.GetByteCount(text) > 262144) return JsonError(413, "请求体超过 256KB");
            if (string.IsNullOrWhiteSpace(text)) return JsonError(400, "请求体为空");
            var body = JsonNode.Parse(text) ?? throw Errors.Bad(400, "请求体不是合法 JSON");
            return Results.Json(call(body), JsonUtil.Options);
        }
        catch (JsonException)
        {
            return JsonError(400, "请求体不是合法 JSON");
        }
        catch (IntuitionException err)
        {
            return JsonError(err.Status, Errors.Redact(err.Message));
        }
        catch (Exception err)
        {
            return JsonError(502, Errors.Redact(err.Message));
        }
    }

    private static IResult JsonError(int status, string message) =>
        Results.Json(new { error = message }, JsonUtil.Options, statusCode: status);

    private static int PositivePort(string? raw, int fallback) =>
        int.TryParse(raw, out var port) && port > 0 && port <= 65535 ? port : fallback;
}

public static class Env
{
    public static void Load()
    {
        foreach (var path in new[] { ".env", "../.env" }) LoadFile(path);
    }

    private static void LoadFile(string path)
    {
        if (!File.Exists(path)) return;
        var allowed = new HashSet<string> { "AI_GATEWAY_API_KEY", "JEV_MODEL", "JEV_TIMEOUT_MS", "JEV_MAX_RETRIES" };
        foreach (var line in File.ReadAllLines(path))
        {
            var text = line.Trim();
            if (text.Length == 0 || text.StartsWith('#')) continue;
            if (text.StartsWith("export ")) text = text["export ".Length..].Trim();
            var cut = text.IndexOf('=');
            if (cut <= 0) continue;
            var key = text[..cut].Trim();
            if (!allowed.Contains(key) || Environment.GetEnvironmentVariable(key) is not null) continue;
            var value = text[(cut + 1)..].Trim();
            if (value.Length >= 2 && ((value[0] == '"' && value[^1] == '"') || (value[0] == '\'' && value[^1] == '\'')))
                value = value[1..^1];
            Environment.SetEnvironmentVariable(key, value);
        }
    }
}
