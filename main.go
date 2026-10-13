// Command mcpgo-sse-hang reproduces random tool-call hangs in the
// mark3labs/mcp-go streamable HTTP client against MCP servers that answer
// with text/event-stream (e.g. gitea-mcp >= 1.7.0, built on the official go-sdk).
//
// Each iteration runs in a fresh child process (initialize + one tools/call),
// so a hung call cannot affect later iterations. A child that has not finished
// within -timeout counts as hung; it receives SIGQUIT, which makes the Go
// runtime print all goroutine stacks before exiting.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

const childEnv = "MCPGO_SSE_HANG_CHILD"

func main() {
	url := flag.String("url", "http://127.0.0.1:8080/mcp", "MCP streamable HTTP endpoint")
	tool := flag.String("tool", "get_gitea_mcp_server_version", "tool to call (must take no arguments)")
	iterations := flag.Int("n", 200, "number of fresh connections")
	timeout := flag.Duration("timeout", 5*time.Second, "a call slower than this counts as hung")
	dumpDir := flag.String("dump", "", "directory for goroutine dumps of hung calls (optional)")
	flag.Parse()

	if os.Getenv(childEnv) == "1" {
		if err := callOnce(*url, *tool); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		return
	}

	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if *dumpDir != "" {
		if err := os.MkdirAll(*dumpDir, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}

	fmt.Printf("mcp-go %s, %d iterations against %s\n", mcpGoVersion(), *iterations, *url)
	hangs, failures := 0, 0
	for i := 1; i <= *iterations; i++ {
		hung, out, err := runChild(self, *url, *tool, *timeout)
		switch {
		case hung:
			hangs++
			fmt.Printf("iteration %d: HANG (no result after %s)\n", i, *timeout)
			if *dumpDir != "" {
				_ = os.WriteFile(filepath.Join(*dumpDir, fmt.Sprintf("hang-%03d.txt", hangs)), out, 0o644)
			}
		case err != nil:
			failures++
			fmt.Printf("iteration %d: error: %s\n", i, bytes.TrimSpace(out))
		}
	}
	fmt.Printf("RESULT: hangs=%d/%d errors=%d\n", hangs, *iterations, failures)
	if hangs > 0 {
		os.Exit(1)
	}
}

func runChild(self, url, tool string, timeout time.Duration) (bool, []byte, error) {
	cmd := exec.Command(self, "-url", url, "-tool", tool)
	cmd.Env = append(os.Environ(), childEnv+"=1")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		return false, nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return false, out.Bytes(), err
	case <-time.After(timeout):
		_ = cmd.Process.Signal(syscall.SIGQUIT)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		return true, out.Bytes(), nil
	}
}

// callOnce deliberately has no deadline, so a hang stays visible instead of
// being turned into a timeout error.
func callOnce(url, tool string) error {
	tr, err := transport.NewStreamableHTTP(url)
	if err != nil {
		return err
	}
	c := client.NewClient(tr)
	defer c.Close()
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		return err
	}
	if _, err := c.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{
		ProtocolVersion: "2025-06-18",
		ClientInfo:      mcp.Implementation{Name: "mcpgo-sse-hang", Version: "1"},
	}}); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	res, err := c.CallTool(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Name: tool, Arguments: map[string]any{}}})
	if err != nil {
		return fmt.Errorf("tools/call: %w", err)
	}
	if res.IsError {
		return fmt.Errorf("tools/call returned isError")
	}
	return nil
}

func mcpGoVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, dep := range info.Deps {
		if dep.Path == "github.com/mark3labs/mcp-go" {
			if dep.Replace != nil {
				return dep.Version + " => " + dep.Replace.Path
			}
			return dep.Version
		}
	}
	return "unknown"
}
