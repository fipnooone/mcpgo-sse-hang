# mcpgo-sse-hang

A minimal reproduction of random tool-call hangs in the
[`mark3labs/mcp-go`](https://github.com/mark3labs/mcp-go) streamable HTTP
client against MCP servers that answer with `text/event-stream`.
gitea-mcp 1.7.0 and 1.8.1 are such servers: they use the official
`modelcontextprotocol/go-sdk` in stateless mode.

It contains no Bifrost code. It is the client library that Bifrost pins
(`mcp-go v0.43.2`) on its own.

## What it does

Each iteration runs in a fresh child process. The child creates a new mcp-go
client, sends `initialize` and then one `tools/call`, with no deadline. If the
child has not finished within `-timeout` (default 5 s), the iteration counts as
**hung**. The child then receives `SIGQUIT`, which makes the Go runtime print
all goroutine stacks, and the stacks are saved when `-dump` is set.

## Requirements

- Go 1.24 or newer
- Docker with Compose (port 127.0.0.1:8080 must be free)
- Linux or macOS (stack dumps use `SIGQUIT`)

## Usage

```sh
docker compose up -d        # gitea-mcp 1.8.1 on 127.0.0.1:8080, no Gitea needed
go run . -n 200             # mcp-go v0.43.2, as pinned in go.mod
```

The output ends with a summary line, for example:

```text
RESULT: hangs=23/200 errors=0
```

The exit code is 1 if any iteration hung.

Compare with the release that contains mark3labs/mcp-go#769:

```sh
go get github.com/mark3labs/mcp-go@v0.47.0
go mod tidy
go run . -n 200             # expect hangs=0
```

Save the goroutine stacks of hung calls:

```sh
go run . -n 200 -dump ./dumps
```

Clean up with `docker compose down`.

### Flags

| Flag | Default | Meaning |
|---|---|---|
| `-url` | `http://127.0.0.1:8080/mcp` | MCP endpoint |
| `-tool` | `get_gitea_mcp_server_version` | Tool to call (must take no arguments). This one is answered by gitea-mcp itself, so no Gitea instance is needed. |
| `-n` | `200` | Iterations, one fresh connection each |
| `-timeout` | `5s` | A call slower than this counts as hung |
| `-dump` | (empty) | Directory for goroutine dumps of hung calls |

## What we observed

This program, as shipped (local Docker, plain HTTP/1.1, gitea-mcp 1.8.1,
`get_gitea_mcp_server_version`, 200 iterations):

| mcp-go | hung |
|---|---|
| v0.43.2 | 20 / 200 |
| v0.47.0 | 0 / 200 |

A wider comparison made with an earlier in-process version of this client,
calling `get_me` against a real Gitea 28.1.0 (200 iterations per row):

| mcp-go | hung |
|---|---|
| v0.43.2 | 23 / 200 |
| v0.46.0 | 16 / 200 |
| v0.47.0 | 0 / 200 |
| v1.2.1 | 0 / 200 |
| v0.46.0 + only the cancel-before-`Body.Close()` change from #769 | 0 / 200 |
| v0.46.0 + only the `readSSE` change from v0.47.0 | 27 / 200 |

With gitea-mcp 1.6.0, which answers with `application/json`, v0.46.0 hung
0 / 200 times.

In every captured hang, the `readSSE` goroutine has already read the response
body to its end and is stuck in `net/http`'s end-of-body handling.
`SendRequest` waits on it, usually in the deferred
`resp.Body.Close()`.
