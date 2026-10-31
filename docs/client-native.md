# Native client — WinUI 3 (C#) + Go backend as a DLL

**Status:** specified, not started. This is the active UI plan.

**Decision record:** ADR-011 in `decisions.md`.

**Goal.** Replace the Wails webview UI with a real WinUI 3 app that looks and
behaves like a stock Windows 11 app (Mica, NavigationView, ThemeResource
colours, native text/scroll/focus/IME), **without** rewriting the client's
networking, discovery, SSH, or config code.

**Shape.** One process, one executable:

```
stackman-desktop.exe          C# / .NET 9 / WinUI 3   <-- the host, owns the UI thread
stackman-backend.dll          Go, -buildmode=c-shared <-- App, Discovery, AgentConnection, keys
resources.pri                 WinUI framework resource index (required)
Microsoft.WindowsAppRuntime*  Windows App SDK, self-contained deployment
```

C# calls into Go through three exported C functions. Payloads are UTF-8 JSON
strings. Nothing but `char*` crosses the ABI.

---

## 0. Ground rules (read before writing code)

1. **Only `char*` JSON crosses the boundary.** No Go structs, slices, or
   `string` in exported signatures. No cgo pointer sharing. This is the single
   most common way to break a Go DLL.
2. **`recover()` in every exported function.** An escaping Go panic terminates
   the .NET process. This is not negotiable.
3. **No `log.Fatal`, `os.Exit`, or `panic` in library reach.** `client/` must
   compile into a library without terminating the host. Grep for these before
   finishing; route through the logger instead.
4. **Never touch WinUI from Go.** Go code runs on its own threads. Events are
   queued and handed to C#, which marshals to the UI thread itself.
5. **No `cwd` assumptions.** The WinUI process may start anywhere. All paths
   are absolute and come from C#.
6. **The Wails client keeps working.** Do not delete or rewrite
   `main_wails.go`, `frontend/`, or the `wails` build tag. The Wails skin is the
   fallback until the native one reaches parity.
7. **`buildmode=c-shared` needs cgo.** That means a C compiler on the build
   machine. Normal `go build ./...` must stay cgo-free (see build tags below).

---

## 1. Go side

### 1.1 Files

| File | Build tag | Package | Purpose |
|---|---|---|---|
| `client/ipc.go` | *(none)* | `main` | Dispatcher, method table, event queue, `Emitter` impl. **Pure Go, unit-tested by `go test ./client/`.** |
| `client/ipc_test.go` | *(none)* | `main` | Tests for the dispatcher and queue. |
| `client/backend_cshared.go` | `windows && cshared` | `main` | The four `//export` functions. The **only** file with `import "C"`. |

### 1.2 Build-tag changes to existing files

`client/main.go` currently starts with `//go:build !wails`. Change it to:

```go
//go:build !wails && !cshared
```

Otherwise `-tags cshared` compiles two `func main` in package `main` and fails.
`main_wails.go` keeps `//go:build wails`. The three tags are mutually
exclusive — never pass `wails cshared` together.

### 1.3 ABI (exactly these four symbols)

```c
// generated into stackman-backend.h by cgo
char* SM_Init(const char* configPath);   // returns {"ok":true,"result":{...}}
char* SM_Call(const char* requestJSON);  // returns {"ok":true|false,...}
char* SM_PollEvents(int timeoutMs);      // returns {"events":[...]} (never null)
void  SM_Free(char* p);                  // free every pointer the above returned
```

Rules:

- Every returned pointer is allocated with `C.CString` and must be released by
  `SM_Free`. C# copies with `Marshal.PtrToStringUTF8` **then** frees.
- **Never return NULL.** On panic, return a JSON error envelope.
- `SM_Init` is idempotent; call once at startup. It must not depend on `main()`
  having run — do all setup inside it.
- `timeoutMs` is clamped to `[0, 30000]`; `0` means "drain what is there now".

### 1.4 Envelopes

Request:

```json
{"method": "GitPull", "params": {"agent_id": "vps-1", "stack": "hotel"}}
```

Response:

```json
{"ok": true,  "result": <any JSON>}
{"ok": false, "error": {"kind": "app_error", "message": "agent: ...", "code": 0}}
```

`kind` is one of `bad_request`, `unknown_method`, `app_error`, `panic`.

`SM_Init` result:

```json
{"ok": true, "result": {"version": "0.1.0", "api_version": 1, "config_path": "C:\\Users\\...\\client.toml"}}
```

Events:

```json
{"events": [{"event": "stream", "data": {"jsonrpc": "2.0", "method": "...", "params": {...}}}]}
{"events": []}
```

The only event the backend emits today is `stream`, carrying an
`api.Notification` verbatim from `App.pump`. Preserve the name and shape so the
payloads match what the Svelte UI already consumes.

### 1.5 Method table

Method names are the **exported `App` method names, verbatim**. Parameter keys
are the snake_case form of the Go parameter names. Any mismatch is a bug.

| `method` | `params` | `result` |
|---|---|---|
| `ListAgents` | — | `[]AgentInfo` (client type, below) |
| `AgentInfo` | `agent_id` | `api.AgentInfo` |
| `ListStacks` | `agent_id` | `[]api.StackSummary` |
| `StackServices` | `agent_id`, `stack` | `[]api.ServicePolicy` |
| `GitStatus` | `agent_id`, `stack` | `string` |
| `GitCheck` | `agent_id`, `stack` | `api.GitCheck` |
| `GitPull` | `agent_id`, `stack` | `api.GitPullResult` |
| `ContainersUp` | `agent_id`, `stack`, `opts` | `null` |
| `ContainersDown` | `agent_id`, `stack`, `opts` | `null` |
| `ContainersRestart` | `agent_id`, `stack`, `services` | `null` |
| `ContainersBuild` | `agent_id`, `stack`, `services` | `null` |
| `ContainersPs` | `agent_id`, `stack` | `[]api.ContainerStatus` |
| `ContainersLogs` | `agent_id`, `stack`, `service`, `follow` | `null` |
| `ListCommands` | `agent_id`, `stack` | `[]api.CommandInfo` |
| `Jobs` | `agent_id` | `[]api.Job` |
| `RunCommand` | `agent_id`, `stack`, `command`, `params` | `null` |
| `ConfigPath` | — | `string` |
| `Close` | — | `null` |

`opts` is `client.UpOptions` / `client.DownOptions` from `app.go`. `services` is
`[]string` (empty = every managed service). `params` is `map[string]string`.
`follow` is a bool.

`client.AgentInfo` (in `app.go`, not `pkg/api`) is the frontend-facing agent
entry; mirror it in C#, not the wire type.

### 1.6 Dispatcher sketch (`client/ipc.go`, no cgo)

```go
type ipcRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type ipcError struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
	Code    int    `json:"code,omitempty"`
}

type ipcResponse struct {
	OK     bool      `json:"ok"`
	Result any       `json:"result,omitempty"`
	Error  *ipcError `json:"error,omitempty"`
}

type Dispatcher struct {
	app    *App
	events *eventQueue
}

func NewDispatcher(app *App) *Dispatcher
func (d *Dispatcher) Call(req []byte) (resp []byte)  // never panics, always valid JSON
func (d *Dispatcher) PollEvents(timeout time.Duration) []byte
func (d *Dispatcher) Emit(event string, data any)    // implements Emitter
```

- `Call` decodes, dispatches via a `map[string]func(json.RawMessage) (any, error)`,
  and marshals. Wrap the body in `defer func(){ if r := recover(); ... }()` and
  emit a `panic` envelope from the recovery path.
- Unknown method → `unknown_method`. Malformed JSON → `bad_request`. Handler
  error → `app_error` with `err.Error()` as the message (add `*api.Error` code
  if the error unwraps to one).
- `Emit` must not block and must not call back into C#. It pushes onto the queue.

### 1.7 Event queue

Use a buffered channel, not a condvar — simpler to get right:

```go
const eventBufferSize = 4096

type eventQueue struct {
	ch      chan ipcEvent
	dropped atomic.Int64
}
```

- `push`: non-blocking send; on full, increment `dropped` (do **not** block, do
  not grow unbounded — log floods must not OOM the app).
- `PollEvents`: greedily drain up to some cap, then return. If `dropped > 0`,
  prepend `{"event":"dropped","data":{"count":N}}` and reset. On timeout with
  nothing, return `{"events":[]}`.
- C# must tolerate `dropped` events by showing a "… output truncated" line.

### 1.8 cgo file sketch (`client/backend_cshared.go`)

```go
//go:build windows && cshared

package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"sync"
	"time"
	"unsafe"
)

var (
	dispMu sync.Mutex
	disp   *Dispatcher
)

//export SM_Init
func SM_Init(configPath *C.char) *C.char { ... }

//export SM_Call
func SM_Call(req *C.char) *C.char { ... }

//export SM_PollEvents
func SM_PollEvents(timeoutMs C.int) *C.char { ... }

//export SM_Free
func SM_Free(p *C.char) { C.free(unsafe.Pointer(p)) }

func main() {} // required by -buildmode=c-shared; never called
```

`SM_Free` frees with `C.free`, which matches `C.CString`'s allocation from
`malloc` — that is why the `#include <stdlib.h>` block is there. Do not use
`unsafe.Pointer` arithmetic.

### 1.9 Build

Requires cgo, therefore a C toolchain. On Windows:

```powershell
scoop install mingw            # or: winget install MSYS2.MSYS2 + pacman -S mingw-w64-x86_64-gcc
$env:CGO_ENABLED = "1"
go env -w CC=gcc
```

```bash
cd client
go build -buildmode=c-shared -trimpath -ldflags "-s -w" -tags cshared \
  -o ../desktop/StackMan/Native/stackman-backend.dll .
```

This writes `stackman-backend.dll` and `stackman-backend.h`. The header is a
build artifact; do not commit it.

**Verify the DLL has no surprise dependencies** before writing any XAML:

```powershell
dumpbin /dependents desktop\StackMan\Native\stackman-backend.dll
```

(`dumpbin` comes with Visual Studio. Without it use
`objdump -p stackman-backend.dll | Select-String "DLL Name"` from mingw, or the
`Dependencies` GUI tool.)

Expected: `KERNEL32.dll`, `WS2_32.dll`, the UCRT, and nothing else. If a
`libwinpthread-1.dll` or `libgcc_s_*.dll` appears, copy it next to the exe or
rebuild with `-ldflags "-s -w -extldflags -static"` and re-check.

### 1.10 Go-side acceptance (M0/M1)

- `go test ./client/` passes with tests covering: happy path, unknown method,
  malformed JSON, handler error, panic recovery, queue drop-oldest + `dropped`
  counter.
- `go build ./...`, `go vet ./...` still pass **without** cgo.
- A throwaway `dotnet run` console app loads the DLL, calls `SM_Init` then
  `SM_Call("{\"method\":\"ListAgents\",\"params\":{}}")`, prints the JSON, and
  exits 0. Delete the app afterwards; it is a smoke test, not a deliverable.

---

## 2. C# side

### 2.1 Layout

```
desktop/
├── StackMan.sln
└── StackMan/
    ├── StackMan.csproj
    ├── app.manifest                 # PerMonitorV2 DPI, comctl32 v6
    ├── App.xaml / App.xaml.cs
    ├── MainWindow.xaml / .cs        # NavigationView shell + Mica
    ├── Backend/
    │   ├── NativeMethods.cs         # the four DllImports
    │   ├── BackendClient.cs         # async JSON-RPC wrapper + SM_Free handling
    │   ├── EventPump.cs             # long-poll loop → UI thread
    │   └── Models.cs                # records mirroring pkg/api + client.AgentInfo
    ├── Views/
    │   ├── AgentsPage.xaml/.cs
    │   ├── StackListPage.xaml/.cs
    │   └── StackDetailPage.xaml/.cs
    ├── Controls/
    │   └── OutputPane.xaml/.cs      # read-only log viewer
    └── Native/
        └── stackman-backend.dll     # build output, copied by MSBuild target
```

`StackMan.csproj`: `net9.0-windows10.0.19041.0`, `UseWinUI=true`,
`WindowsPackageType=None` (unpackaged), `WindowsAppSDKSelfContained=true`,
`ApplicationManifest=app.manifest`, `Platforms=x64`.

### 2.2 P/Invoke

```csharp
internal static class NativeMethods
{
    private const string Dll = "stackman-backend";

    [DllImport(Dll, CallingConvention = CallingConvention.Cdecl)]
    internal static extern IntPtr SM_Init(string configPath);

    [DllImport(Dll, CallingConvention = CallingConvention.Cdecl)]
    internal static extern IntPtr SM_Call(string requestJson);

    [DllImport(Dll, CallingConvention = CallingConvention.Cdecl)]
    internal static extern IntPtr SM_PollEvents(int timeoutMs);

    [DllImport(Dll, CallingConvention = CallingConvention.Cdecl)]
    internal static extern void SM_Free(IntPtr p);
}
```

Take-and-free helper, used by everything:

```csharp
private static string TakeAndFree(IntPtr p)
{
    if (p == IntPtr.Zero) throw new InvalidOperationException("backend returned null");
    try { return Marshal.PtrToStringUTF8(p) ?? ""; }
    finally { NativeMethods.SM_Free(p); }
}
```

Declare the *parameters* as UTF-8 explicitly, or non-ASCII stack names will be
mangled:

```csharp
[DllImport(Dll, CallingConvention = CallingConvention.Cdecl)]
internal static extern IntPtr SM_Call([MarshalAs(UnmanagedType.LPUTF8Str)] string requestJson);
```

and copy results out with `Marshal.PtrToStringUTF8`.

### 2.3 Threading rules (this is where it will go wrong)

- **Every** call goes through one method:

  ```csharp
  public Task<T> CallAsync<T>(string method, object? parameters = null, CancellationToken ct = default)
  ```

  Implemented with `Task.Factory.StartNew(..., TaskCreationOptions.LongRunning)`
  — **not** `Task.Run`. `GitPull` and `ContainersBuild` block for minutes and
  would starve the thread pool.
- Never call `SM_Call` from the UI thread.
- `EventPump` runs one single loop:

  ```csharp
  while (!ct.IsCancellationRequested)
  {
      var json = await Task.Factory.StartNew(
          () => TakeAndFree(NativeMethods.SM_PollEvents(5000)),
          ct, TaskCreationOptions.LongRunning, TaskScheduler.Default);
      foreach (var evt in Parse(json))
          _queue.TryEnqueue(() => RaiseEvent(evt));   // DispatcherQueue → UI thread
  }
  ```

  Rearm immediately after `TryEnqueue`; do not await UI work inside the loop.
- JSON: `System.Text.Json` with `JsonNamingPolicy.SnakeCaseLower` and a
  `JsonSerializerOptions { PropertyNameCaseInsensitive = true }`. Verify field
  names against `pkg/api/api.go` before trusting this.

### 2.4 UI structure

`MainWindow`:
- `SystemBackdrop = new MicaBackdrop()`
- `ExtendsContentIntoTitleBar = true; SetTitleBar(AppTitleBar);`
- `NavigationView` in `Left` mode, `IsSettingsVisible="False"`,
  `IsBackButtonVisible="Collapsed"`, menu items **Agents** and **Jobs**,
  content is a `Frame`.
- Pages: `AgentsPage` → `StackListPage(agentId)` → `StackDetailPage(agentId, stack)`.

`StackDetailPage` contents, in order:
1. Header card: stack name, branch, `▲ahead ▼behind`, running-job pill.
2. Command bar: `Check`, `Pull` (disabled until `Check` succeeded and
   `safe_to_pull`), `Up ▸` as a `MenuFlyout` of services + "all", `Down`,
   `Restart`, `Build`, `Ps`, `Commands ▾`.
3. Services list with checkboxes (empty selection = all managed services).
4. `InfoBar` for check results and errors (`Severity=Warning|Error`).
5. `OutputPane` at the bottom, `GridSplitter` optional.

Styling rules to make it read as stock Windows 11:
- Only `ThemeResource` brushes — never hard-coded hex:
  `CardBackgroundFillColorDefaultBrush`, `CardStrokeColorDefaultBrush`,
  `LayerFillColorDefaultBrush`, `DividerStrokeColorDefaultBrush`,
  `TextFillColorPrimaryBrush`, `TextFillColorSecondaryBrush`,
  `AccentFillColorDefaultBrush`, `SystemFillColorCriticalBrush`,
  `SystemFillColorCautionBrush`, `SystemFillColorSuccessBrush`.
- Cards: `Border` + `CornerRadius="8"` + card background + 1px card stroke.
  There is no built-in `SettingsCard`; use
  **`CommunityToolkit.WinUI.Controls.SettingsControls`** if you want one.
- Icons: `FontIcon` with `Segoe Fluent Icons` glyphs, `&#xE7xx;` range.
- Mono font for output: `Cascadia Mono`, fall back to `Consolas`.
- Confirmations: `ContentDialog`. Transient status: `InfoBar`. Running work:
  `ProgressRing` in the header card only.
- Every icon-only button gets `AutomationProperties.Name`.
- Verify in light, dark, and high contrast, and at 100% / 150% scaling.

### 2.5 MSBuild wiring

Add to `StackMan.csproj` so `dotnet build` produces the DLL too:

```xml
<Target Name="BuildGoBackend" BeforeTargets="Build">
  <Exec Command="go build -buildmode=c-shared -trimpath -ldflags &quot;-s -w&quot; -tags cshared -o &quot;$(MSBuildProjectDirectory)\Native\stackman-backend.dll&quot; ."
        WorkingDirectory="$(MSBuildProjectDirectory)\..\..\client" />
</Target>
```

Add `Native\stackman-backend.dll` to the output:
`<None Include="Native\stackman-backend.dll"><CopyToOutputDirectory>PreserveNewest</CopyToOutputDirectory></None>`

### 2.6 Packaging reality (do not fight this)

WinUI 3 **cannot** be a single-file exe: `resources.pri` and the Windows App SDK
binaries must exist on disk. `PublishSingleFile` is unsupported for WinUI 3.
Ship a folder:

```
dist/stackman-desktop/
├── stackman-desktop.exe
├── stackman-backend.dll
├── resources.pri
├── *.dll            (WindowsAppRuntime, Microsoft.UI.Xaml, ...)
└── *.winmd / *.pri
```

`WindowsAppSDKSelfContained=true` avoids requiring the runtime to be installed;
expect ~80–120 MB. Framework-dependent is ~5 MB but requires the WinAppSDK
runtime on the machine. Pick self-contained for a tool you drop on your own
laptop.

---

## 3. Milestones

Do them in order. Do not start a milestone before the previous acceptance line
passes.

| # | Work | Acceptance |
|---|---|---|
| M0 | `client/ipc.go` + tests, no cgo | `go test ./client/` green with the five cases in §1.10; `go build ./...` still cgo-free |
| M1 | `backend_cshared.go`, cgo build, smoke console app | DLL builds; `dumpbin /dependents` clean; console app prints `ListAgents` JSON and exits 0 |
| M2 | WinUI 3 project skeleton | Unpackaged window with Mica, NavigationView, two empty pages; `dotnet run` works |
| M3 | `BackendClient` + `EventPump` + Agents page | Agents page lists real agents discovered over WARP; no UI-thread blocking (window still draggable while listing) |
| M4 | Stack list + stack detail + output pane | `GitCheck` renders changed files + `safe_to_pull`; `ContainersBuild` streams output live into the pane |
| M5 | Jobs pill, dialogs, InfoBar, a11y/theming pass | A build started in the app survives closing the window; visible on relaunch; light/dark/HC + 150% verified |
| M6 | `make desktop` + packaging | One folder, documented, launches on a clean machine with no .NET/WinAppSDK prompt |

Then, and only then, decide whether the Wails skin is deleted or kept.

---

## 4. Known risks

| Risk | Mitigation |
|---|---|
| Escaping Go panic kills the app | `recover()` in every export; tests assert the envelope |
| cgo toolchain friction on Windows | Pin the gcc path in `go env`; document `scoop install mingw`; build the DLL in CI too |
| Go DLL pulls in mingw runtime DLLs | `dumpbin /dependents` gate in M1; `-extldflags -static` fallback |
| Two runtimes, one SEH chain (Go + .NET) | Accept; expect odd dumps from Go-side faults. Log at the boundary |
| Thread-pool starvation from blocking calls | `TaskCreationOptions.LongRunning` everywhere |
| Unbounded event growth on log floods | Bounded queue + `dropped` event, both tested |
| Delve debugging the DLL is awkward | Debug the boundary with logs; the dispatcher is testable without cgo, so put logic there |
| Model drifts from `pkg/api` shapes | Mirror DTOs by hand from `pkg/api/api.go`; a build-time JSON round-trip test is a nice-to-have |

---

## 5. Copy-paste prompt for the next session

> Read `docs/client-native.md` in full, plus `docs/client.md` §4–5,
> `client/app.go`, and `docs/decisions.md` ADR-011.
>
> Implement **milestone M0 only**: add `client/ipc.go` and
> `client/ipc_test.go` exactly as specified in §1.6–1.7, with the method table
> from §1.5. Do not add cgo, do not touch `main_wails.go`, do not create any C#
> files yet. Keep `go build ./...`, `go vet ./...`, `go test ./...` green with
> cgo disabled.
>
> Rules: only JSON `[]byte` in and out of `Dispatcher`; `Call` must never
> panic; the event queue is a bounded channel that drops-oldest and counts
> drops; method names match `App`'s exported method names verbatim.
>
> When done, report which of the five acceptance cases in §1.10 are covered by
> tests.
