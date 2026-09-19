using System.Text.RegularExpressions;

namespace Jev;

public sealed class IntuitionException : Exception
{
    public int Status { get; }

    public IntuitionException(int status, string message) : base(message) => Status = status;
}

public static class Errors
{
    public static IntuitionException Bad(int status, string message) => new(status, message);

    public static string Redact(string message)
    {
        var text = Vck.Replace(message, "[redacted]");
        text = Bearer.Replace(text, "Bearer [redacted]");
        return text.Length <= 500 ? text : text[..500];
    }

    private static readonly Regex Vck = new(@"vck_[A-Za-z0-9_-]+", RegexOptions.Compiled);
    private static readonly Regex Bearer = new(@"Bearer\s+\S+", RegexOptions.Compiled | RegexOptions.IgnoreCase);
}
