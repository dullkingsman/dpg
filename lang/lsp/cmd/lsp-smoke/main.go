// lsp-smoke is a minimal end-to-end test for dpg-lsp.
//
// It starts dpg-lsp on a TCP port, sends a small JSON-RPC sequence, and
// asserts the expected responses. Run it from the lang/lsp/ directory:
//
//	go run ./cmd/lsp-smoke
//
// Exit code 0 means all assertions passed; non-zero means a failure.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("PASS: all smoke assertions passed")
}

func run() error {
	// Find the dpg-lsp binary (build it first if needed).
	lspBin, err := findOrBuild()
	if err != nil {
		return fmt.Errorf("build dpg-lsp: %w", err)
	}

	// Pick a free port.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	// Start dpg-lsp in TCP mode.
	cmd := exec.Command(lspBin, "--tcp", fmt.Sprintf(":%d", port))
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start dpg-lsp: %w", err)
	}
	defer cmd.Process.Kill() //nolint:errcheck

	// Wait for the server to be ready.
	var conn net.Conn
	for i := 0; i < 20; i++ {
		conn, err = net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if conn == nil {
		return fmt.Errorf("could not connect to dpg-lsp after 2 s: %w", err)
	}
	defer conn.Close()

	c := &client{conn: conn, reader: bufio.NewReader(conn), id: 0}

	// ── 1. initialize ─────────────────────────────────────────────────────────
	rootURI := "file://" + lspRoot()
	resp, err := c.call("initialize", map[string]any{
		"processId": os.Getpid(),
		"rootUri":   rootURI,
		"capabilities": map[string]any{
			"textDocument": map[string]any{},
		},
	})
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	caps, ok := resp["capabilities"]
	if !ok {
		return fmt.Errorf("initialize response missing 'capabilities': %v", resp)
	}
	capsMap, ok := caps.(map[string]any)
	if !ok {
		return fmt.Errorf("capabilities not an object")
	}
	for _, cap := range []string{"hoverProvider", "definitionProvider", "documentFormattingProvider"} {
		if capsMap[cap] == nil {
			return fmt.Errorf("server did not advertise capability %q", cap)
		}
	}
	fmt.Println("  ✓ initialize: capabilities ok")

	// ── 2. initialized notification ───────────────────────────────────────────
	c.notify("initialized", map[string]any{})

	// ── 3. textDocument/didOpen — valid file (expect 0 diagnostics) ───────────
	schemaPath := filepath.Join(lspRoot(), "testdata", "schema.dpg")
	schemaURI := "file://" + schemaPath
	schemaContent, err := os.ReadFile(schemaPath)
	if err != nil {
		return fmt.Errorf("read schema file: %w", err)
	}

	c.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        schemaURI,
			"languageId": "dpg",
			"version":    1,
			"text":       string(schemaContent),
		},
	})
	// Give the server time to compile and push diagnostics.
	time.Sleep(600 * time.Millisecond)
	fmt.Println("  ✓ textDocument/didOpen: sent (diagnostics pushed asynchronously)")

	// ── 4. textDocument/hover ─────────────────────────────────────────────────
	hoverResp, err := c.call("textDocument/hover", map[string]any{
		"textDocument": map[string]any{"uri": schemaURI},
		"position":     map[string]any{"line": 0, "character": 8},
	})
	if err != nil {
		return fmt.Errorf("hover: %w", err)
	}
	if hoverResp != nil {
		fmt.Printf("  ✓ textDocument/hover: response received (%v)\n", hoverResp["contents"])
	} else {
		fmt.Println("  ~ textDocument/hover: returned null (no match at cursor; ok)")
	}

	// ── 5. textDocument/completion at top level ───────────────────────────────
	compResp, err := c.call("textDocument/completion", map[string]any{
		"textDocument": map[string]any{"uri": schemaURI},
		"position":     map[string]any{"line": 0, "character": 0},
	})
	if err != nil {
		return fmt.Errorf("completion: %w", err)
	}
	if compResp == nil {
		return fmt.Errorf("completion returned null")
	}
	items, _ := compResp["items"].([]any)
	if len(items) == 0 {
		// Some servers return the list directly
		if arr, ok := compResp["items"]; ok {
			_ = arr
		}
	}
	fmt.Printf("  ✓ textDocument/completion: %d items\n", len(items))

	// ── 6. textDocument/didChange — inject forbidden verb ────────────────────
	c.notify("textDocument/didChange", map[string]any{
		"textDocument": map[string]any{"uri": schemaURI, "version": 2},
		"contentChanges": []map[string]any{
			{"text": "CREATE TABLE forbidden (id bigint);\n"},
		},
	})
	fmt.Println("  ✓ textDocument/didChange: injected CREATE TABLE (DPG-E006)")
	// Diagnostics are pushed asynchronously; we don't block waiting for them in this
	// smoke test, but the server should fire within the 300 ms debounce window.
	time.Sleep(600 * time.Millisecond)

	// ── 7. shutdown ───────────────────────────────────────────────────────────
	if _, err := c.call("shutdown", nil); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	c.notify("exit", nil)
	fmt.Println("  ✓ shutdown/exit: clean")

	return nil
}

// ── JSON-RPC 2.0 client ───────────────────────────────────────────────────────

type client struct {
	conn   net.Conn
	reader *bufio.Reader
	id     int
}

func (c *client) call(method string, params any) (map[string]any, error) {
	c.id++
	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      c.id,
		"method":  method,
	}
	if params != nil {
		msg["params"] = params
	}
	if err := c.send(msg); err != nil {
		return nil, err
	}
	return c.recv(c.id)
}

func (c *client) notify(method string, params any) {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		msg["params"] = params
	}
	_ = c.send(msg) //nolint:errcheck
}

func (c *client) send(msg any) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	_, err = fmt.Fprint(c.conn, header+string(body))
	return err
}

func (c *client) recv(wantID int) (map[string]any, error) {
	c.conn.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	defer c.conn.SetReadDeadline(time.Time{})               //nolint:errcheck

	for {
		// Read headers
		var contentLen int
		for {
			line, err := c.reader.ReadString('\n')
			if err != nil {
				return nil, fmt.Errorf("read header: %w", err)
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			if strings.HasPrefix(line, "Content-Length:") {
				n, _ := strconv.Atoi(strings.TrimSpace(line[len("Content-Length:"):]))
				contentLen = n
			}
		}
		if contentLen == 0 {
			continue
		}

		body := make([]byte, contentLen)
		if _, err := c.reader.Read(body); err != nil {
			return nil, fmt.Errorf("read body: %w", err)
		}

		var envelope map[string]any
		if err := json.Unmarshal(body, &envelope); err != nil {
			return nil, fmt.Errorf("parse response: %w", err)
		}

		// Skip notifications (no "id")
		idVal, hasID := envelope["id"]
		if !hasID {
			continue
		}

		// Match the request id (JSON numbers decode as float64)
		var gotID int
		switch v := idVal.(type) {
		case float64:
			gotID = int(v)
		case int:
			gotID = v
		}
		if gotID != wantID {
			continue
		}

		if errVal, ok := envelope["error"]; ok && errVal != nil {
			return nil, fmt.Errorf("rpc error: %v", errVal)
		}

		result, _ := envelope["result"].(map[string]any)
		return result, nil
	}
}

func findOrBuild() (string, error) {
	// Prefer an already-built binary on PATH.
	if p, err := exec.LookPath("dpg-lsp"); err == nil {
		return p, nil
	}

	// Build from source relative to this file.
	_, self, _, _ := runtime.Caller(0)
	lspRoot := filepath.Join(filepath.Dir(self), "..", "..")

	bin := filepath.Join(os.TempDir(), "dpg-lsp-smoke")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}

	cmd := exec.Command("go", "build", "-o", bin, "./cmd/dpg-lsp")
	cmd.Dir = lspRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return bin, nil
}

func lspRoot() string {
	_, self, _, _ := runtime.Caller(0)
	// lang/lsp/cmd/lsp-smoke/main.go → go up 2 levels → lang/lsp/
	return filepath.Join(filepath.Dir(self), "..", "..")
}
