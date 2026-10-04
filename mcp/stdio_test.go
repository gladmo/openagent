package mcp

// Port of packages/mcp/test/stdio.test.ts: the stdio transport against a
// re-execed fixture server (the Go form of the node fixture scripts).

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gladmo/openagent/jsonx"
)

// The fixture server runs when MCP_STDIO_FIXTURE=1: the test binary is
// re-execed with only TestStdioFixtureServer selected.
func TestStdioFixtureServer(t *testing.T) {
	if os.Getenv("MCP_STDIO_FIXTURE") != "1" || os.Getenv("MCP_STDIO_STUBBORN") == "1" {
		return
	}
	fmt.Fprintln(os.Stderr, "stdio fixture ready")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	writer := bufio.NewWriter(os.Stdout)
	for scanner.Scan() {
		value, err := jsonx.Parse(scanner.Text())
		if err != nil {
			continue
		}
		message, err := ParseJsonRpcMessage(value)
		if err != nil || message.Kind != KindRequest {
			continue
		}
		var result any
		switch message.Method() {
		case "initialize":
			result = jsonx.ObjFrom(
				"protocolVersion", "2025-06-18",
				"capabilities", jsonx.ObjFrom("tools", jsonx.NewObj()),
				"serverInfo", jsonx.ObjFrom("name", "stdio-fixture", "version", "1.0.0"),
			)
		case "tools/list":
			result = jsonx.ObjFrom("tools", []any{
				jsonx.ObjFrom("name", "echo", "inputSchema", jsonx.ObjFrom("type", "object")),
			})
		case "tools/call":
			text := ""
			if arguments, ok := message.ParamsObj().MustGet("arguments").(*jsonx.Obj); ok {
				text, _ = arguments.MustGet("text").(string)
			}
			result = jsonx.ObjFrom("content", []any{jsonx.ObjFrom("type", "text", "text", text)})
		case "ping":
			result = jsonx.NewObj()
		default:
			_, _ = writer.WriteString(jsonx.Stringify(NewErrorResponse(
				message.ID(), JSON_RPC_ERROR_CODES.MethodNotFound, "not found", nil).Raw) + "\n")
			_ = writer.Flush()
			continue
		}
		_, _ = writer.WriteString(jsonx.Stringify(NewSuccessResponse(message.ID(), result).Raw) + "\n")
		_ = writer.Flush()
	}
	os.Exit(0)
}

// initStubbornFixture answers initialize, ignores stdin EOF and SIGTERM,
// and keeps running until it is killed. It corresponds to the reference's
// stubborn-server.mjs; a separate grandchild is unnecessary because the
// group kill reaches the whole process group.
func initStubbornFixture() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	go func() {
		for range signals {
		}
	}()
	fmt.Fprintln(os.Stderr, "stubborn fixture ready")
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		writer := bufio.NewWriter(os.Stdout)
		for scanner.Scan() {
			value, err := jsonx.Parse(scanner.Text())
			if err != nil {
				continue
			}
			message, err := ParseJsonRpcMessage(value)
			if err != nil || message.Kind != KindRequest || message.Method() != "initialize" {
				continue
			}
			result := jsonx.ObjFrom(
				"protocolVersion", "2025-06-18",
				"capabilities", jsonx.NewObj(),
				"serverInfo", jsonx.ObjFrom("name", "stubborn-fixture", "version", "1.0.0"),
			)
			_, _ = writer.WriteString(jsonx.Stringify(NewSuccessResponse(message.ID(), result).Raw) + "\n")
			_ = writer.Flush()
		}
	}()
	for {
		time.Sleep(time.Second)
	}
}

func init() {
	if os.Getenv("MCP_STDIO_FIXTURE") == "1" && os.Getenv("MCP_STDIO_STUBBORN") == "1" {
		initStubbornFixture()
	}
}

func fixtureOptions(env map[string]string) StdioTransportOptions {
	for key, value := range map[string]string{"MCP_STDIO_FIXTURE": "1"} {
		if _, exists := env[key]; !exists {
			env[key] = value
		}
	}
	return StdioTransportOptions{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestStdioFixtureServer", "-test.v=false"},
		Env:     env,
	}
}

func TestStdioConnectsAndCapturesStderr(t *testing.T) {
	stderr := make(chan string, 16)
	options := fixtureOptions(map[string]string{})
	options.OnStderr = func(chunk string) { stderr <- chunk }
	transport := NewStdioTransport(options)
	client := NewMcpClient(McpClientOptions{Name: "stdio-test", Version: "1.0.0"})
	if _, err := client.Connect(transport); err != nil {
		t.Fatalf("connect: %v", err)
	}
	tools, err := client.ListTools(McpRequestOptions{})
	if err != nil || len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v %v", tools, err)
	}
	args, _ := jsonx.Parse(`{"text":"hello"}`)
	result, err := client.CallTool("echo", args.(*jsonx.Obj), McpRequestOptions{})
	if err != nil || len(result.Content) != 1 || result.Content[0].(TextContent).Text != "hello" {
		t.Fatalf("echo = %+v %v", result, err)
	}
	if transport.Pid() == 0 {
		t.Fatal("no pid")
	}
	select {
	case chunk := <-stderr:
		if !strings.Contains(chunk, "stdio fixture ready") {
			t.Fatalf("stderr chunk = %q", chunk)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no stderr captured")
	}
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(transport.Stderr(), "stdio fixture ready") {
		if time.Now().After(deadline) {
			t.Fatalf("buffered stderr = %q", transport.Stderr())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if client.ConnectionState() != ClientClosed {
		t.Fatalf("state = %s", client.ConnectionState())
	}
}

// TestStdioKillsStubbornServer: a server that ignores stdin closing and
// SIGTERM is killed by the SIGKILL that follows the close timeout.
func TestStdioKillsStubbornServer(t *testing.T) {
	if !useProcessGroups {
		t.Skip("process groups are a unix feature")
	}
	options := fixtureOptions(map[string]string{"MCP_STDIO_STUBBORN": "1"})
	options.CloseTimeout = 100 * time.Millisecond
	transport := NewStdioTransport(options)
	client := NewMcpClient(McpClientOptions{Name: "stdio-test", Version: "1.0.0"})
	if _, err := client.Connect(transport); err != nil {
		t.Fatalf("connect: %v", err)
	}
	pid := transport.Pid()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(transport.Stderr(), "stubborn fixture ready") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	startedAt := time.Now()
	if err := client.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if elapsed := time.Since(startedAt); elapsed > 5*time.Second {
		t.Fatalf("close took %s", elapsed)
	}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return // process is gone
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("stubborn server survived close")
}
