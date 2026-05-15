package server

import (
	"os"
	"os/exec"

	"github.com/dullkingsman/dpg-lsp/internal/analysis"
	"github.com/dullkingsman/dpg-lsp/internal/workspace"
	"github.com/tliron/commonlog"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
	glspserver "github.com/tliron/glsp/server"
)

const serverName = "dpg-lsp"
const serverVersion = "0.1.0"

// RunStdio starts the LSP server over stdin/stdout.
func RunStdio() error {
	commonlog.Configure(1, nil)
	srv := newServer()
	return srv.RunStdio()
}

// RunTCP starts the LSP server on a TCP address (for debugging).
func RunTCP(addr string) error {
	commonlog.Configure(1, nil)
	srv := newServer()
	return srv.RunTCP(addr)
}

func newServer() *glspserver.Server {
	handler := protocol.Handler{}
	ws := workspace.New()

	handler.Initialize = func(ctx *glsp.Context, params *protocol.InitializeParams) (any, error) {
		if params.RootURI != nil {
			ws.SetRoot(uriToPath(*params.RootURI))
		} else if params.RootPath != nil {
			ws.SetRoot(*params.RootPath)
		}

		syncKind := protocol.TextDocumentSyncKindIncremental
		completionOpts := protocol.CompletionOptions{
			TriggerCharacters: []string{".", " "},
		}
		return protocol.InitializeResult{
			Capabilities: protocol.ServerCapabilities{
				TextDocumentSync:           &syncKind,
				HoverProvider:              true,
				DefinitionProvider:         true,
				CompletionProvider:         &completionOpts,
				DocumentFormattingProvider: true,
			},
			ServerInfo: &protocol.InitializeResultServerInfo{
				Name:    serverName,
				Version: &[]string{serverVersion}[0],
			},
		}, nil
	}

	handler.Initialized = func(ctx *glsp.Context, params *protocol.InitializedParams) error {
		go ws.Discover()
		return nil
	}

	handler.Shutdown = func(ctx *glsp.Context) error { return nil }

	handler.TextDocumentDidOpen = func(ctx *glsp.Context, params *protocol.DidOpenTextDocumentParams) error {
		path := uriToPath(string(params.TextDocument.URI))
		ws.OpenDocument(path, params.TextDocument.Text)
		go publishDiagnostics(ctx, ws, path)
		return nil
	}

	handler.TextDocumentDidChange = func(ctx *glsp.Context, params *protocol.DidChangeTextDocumentParams) error {
		path := uriToPath(string(params.TextDocument.URI))
		for _, change := range params.ContentChanges {
			if c, ok := change.(protocol.TextDocumentContentChangeEventWhole); ok {
				ws.UpdateDocument(path, c.Text)
			}
		}
		go publishDiagnostics(ctx, ws, path)
		return nil
	}

	handler.TextDocumentDidClose = func(ctx *glsp.Context, params *protocol.DidCloseTextDocumentParams) error {
		path := uriToPath(string(params.TextDocument.URI))
		ws.CloseDocument(path)
		// Clear diagnostics in the client's panel for this file.
		ctx.Notify(protocol.ServerTextDocumentPublishDiagnostics, protocol.PublishDiagnosticsParams{
			URI:         params.TextDocument.URI,
			Diagnostics: []protocol.Diagnostic{},
		})
		return nil
	}

	handler.TextDocumentHover = func(ctx *glsp.Context, params *protocol.HoverParams) (*protocol.Hover, error) {
		path := uriToPath(string(params.TextDocument.URI))
		return analysis.Hover(ws, path, params.Position), nil
	}

	handler.TextDocumentDefinition = func(ctx *glsp.Context, params *protocol.DefinitionParams) (any, error) {
		path := uriToPath(string(params.TextDocument.URI))
		loc := analysis.Definition(ws, path, params.Position)
		if loc == nil {
			return nil, nil
		}
		return loc, nil
	}

	handler.TextDocumentCompletion = func(ctx *glsp.Context, params *protocol.CompletionParams) (any, error) {
		path := uriToPath(string(params.TextDocument.URI))
		items := analysis.Completion(ws, path, params.Position)
		return protocol.CompletionList{IsIncomplete: false, Items: items}, nil
	}

	handler.TextDocumentFormatting = func(ctx *glsp.Context, params *protocol.DocumentFormattingParams) ([]protocol.TextEdit, error) {
		path := uriToPath(string(params.TextDocument.URI))
		return formatDocument(path)
	}

	return glspserver.NewServer(&handler, serverName, false)
}

// publishDiagnostics runs dpg validate and pushes results to the client.
func publishDiagnostics(ctx *glsp.Context, ws *workspace.Workspace, path string) {
	diags := analysis.Diagnostics(ws, path)
	params := protocol.PublishDiagnosticsParams{
		URI:         protocol.DocumentUri(pathToURI(path)),
		Diagnostics: diags,
	}
	ctx.Notify(protocol.ServerTextDocumentPublishDiagnostics, params)
}

// formatDocument shells out to `dpg fmt <path>` and returns a replace-all TextEdit.
func formatDocument(path string) ([]protocol.TextEdit, error) {
	before, err := readFile(path)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command("dpg", "fmt", path)
	if err := cmd.Run(); err != nil {
		return nil, nil // fmt failed — return no edits silently
	}

	after, err := readFile(path)
	if err != nil {
		return nil, err
	}
	if before == after {
		return nil, nil
	}

	lines := countLines(before)
	lastLineLen := lastLineLength(before)
	fullRange := protocol.Range{
		Start: protocol.Position{Line: 0, Character: 0},
		End:   protocol.Position{Line: uint32(lines), Character: uint32(lastLineLen)},
	}
	return []protocol.TextEdit{{Range: fullRange, NewText: after}}, nil
}

func uriToPath(uri string) string {
	if len(uri) > 7 && uri[:7] == "file://" {
		return uri[7:]
	}
	return uri
}

func pathToURI(path string) string {
	if len(path) > 0 && path[0] == '/' {
		return "file://" + path
	}
	return "file:///" + path
}

func readFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func countLines(s string) int {
	n := 0
	for _, c := range s {
		if c == '\n' {
			n++
		}
	}
	return n
}

func lastLineLength(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '\n' {
			return len(s) - i - 1
		}
	}
	return len(s)
}
