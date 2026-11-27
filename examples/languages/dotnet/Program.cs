// The story of the chapter "In a program of another language", in C#,
// in the same process (docs/manual/other-languages.md). .NET calls the
// library of C with P/Invoke; nothing else is needed. Run it in a folder
// with invoice.json and nothing else:
//
//     FADODB_LIBRARY=/path/to/libfadodb-core.so dotnet run --project /path/to/books.csproj
using System.Runtime.InteropServices;
using System.Text.Json.Nodes;

// The library is the file FADODB_LIBRARY names, or the one the system
// finds under its name.
NativeLibrary.SetDllImportResolver(typeof(Native).Assembly, (name, assembly, path) =>
    name == Native.Library && Environment.GetEnvironmentVariable("FADODB_LIBRARY") is { Length: > 0 } file
        ? NativeLibrary.Load(file)
        : IntPtr.Zero);

var invoice = JsonNode.Parse(File.ReadAllText("invoice.json"));
using var db = new Database("books.fado");
var dan = new JsonObject { ["id"] = "dan", ["kind"] = "person", ["system_groups"] = new JsonArray("designers") };
var ann = new JsonObject { ["id"] = "ann", ["kind"] = "person", ["groups"] = new JsonArray("sales") };
var bob = new JsonObject { ["id"] = "bob", ["kind"] = "person", ["groups"] = new JsonArray("accounting") };

db.Exec(dan, "define_class", new JsonObject { ["definition"] = invoice });
Console.WriteLine("dan defines the class Invoice");

var made = db.Exec(ann, "create", new JsonObject
{
    ["class"] = "Invoice", ["id"] = "FV/2026/11/0042",
    ["values"] = new JsonObject { ["number"] = "FV/2026/11/0042" },
})["document"]!;
Console.WriteLine($"ann makes {made["id"]}: {made["state"]}");

JsonObject Book() => new() { ["class"] = "Invoice", ["id"] = "FV/2026/11/0042", ["transition"] = "book" };
try
{
    db.Exec(bob, "transition", Book());
}
catch (Refused refused)
{
    Console.WriteLine($"bob books it: refused, {refused.Code}");
}

db.Exec(bob, "set", new JsonObject
{
    ["class"] = "Invoice", ["id"] = "FV/2026/11/0042",
    ["values"] = new JsonObject { ["account"] = "400-01" },
});
Console.WriteLine("bob gives the account 400-01");

var booked = db.Exec(bob, "transition", Book())["document"]!;
Console.WriteLine($"bob books it: {booked["state"]}");

// The five functions of fadodb.h this program uses. Text is UTF-8; an
// answer is kept as a pointer, so that it is given back with fado_free.
static class Native
{
    public const string Library = "fadodb-core";

    [DllImport(Library)]
    public static extern ulong fado_open([MarshalAs(UnmanagedType.LPUTF8Str)] string path,
        [MarshalAs(UnmanagedType.LPUTF8Str)] string options, out IntPtr refusal);

    [DllImport(Library)]
    public static extern IntPtr fado_exec(ulong db, [MarshalAs(UnmanagedType.LPUTF8Str)] string caller,
        [MarshalAs(UnmanagedType.LPUTF8Str)] string command, [MarshalAs(UnmanagedType.LPUTF8Str)] string obj);

    [DllImport(Library)]
    public static extern int fado_close(ulong db, IntPtr refusal);

    [DllImport(Library)]
    public static extern void fado_free(IntPtr answer);
}

// A database the library opened, closed when it is disposed.
sealed class Database : IDisposable
{
    readonly ulong n;

    public Database(string path)
    {
        n = Native.fado_open(path, "{}", out var refusal);
        if (n == 0)
        {
            throw new InvalidOperationException(Take(refusal));
        }
    }

    // Runs a command as the caller; a refusal is thrown with its code.
    public JsonObject Exec(JsonObject caller, string command, JsonObject obj)
    {
        var answer = JsonNode.Parse(Take(Native.fado_exec(n, caller.ToJsonString(), command, obj.ToJsonString())))!.AsObject();
        if (answer["error"] is JsonObject error)
        {
            throw new Refused((string)error["code"]!, (string)error["message"]!);
        }
        return answer;
    }

    static string Take(IntPtr answer)
    {
        try
        {
            return Marshal.PtrToStringUTF8(answer) ?? "";
        }
        finally
        {
            Native.fado_free(answer);
        }
    }

    public void Dispose() => Native.fado_close(n, IntPtr.Zero);
}

sealed class Refused(string code, string message) : Exception(message)
{
    public string Code { get; } = code;
}
