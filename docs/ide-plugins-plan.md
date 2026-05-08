# IDE Plugins — Git Submodule Setup Plan

This document specifies the full setup for the four IDE/editor plugin repositories as Git
submodules under `ide-plugins/` in the `dpg` monorepo, plus the standalone `tree-sitter-dpg`
grammar that all plugins share.

---

## 1. Repository Map

| Submodule path | GitHub repo | Description |
|---|---|---|
| `ide-plugins/tree-sitter-dpg` | `dullkingsman/tree-sitter-dpg` | Tree-sitter grammar for `.dpg` files |
| `ide-plugins/dpg-lsp` | `dullkingsman/dpg-lsp` | Language Server Protocol server |
| `ide-plugins/nvim-dpg` | `dullkingsman/nvim-dpg` | Neovim plugin |
| `ide-plugins/vscode-dpg` | `dullkingsman/vscode-dpg` | VS Code extension |
| `ide-plugins/intellij-dpg` | `dullkingsman/intellij-dpg` | JetBrains plugin |

---

## 2. Pre-conditions

Before any submodule can be added:

- Each repository must exist on GitHub (create via `gh repo create --public`).
- Each repository must have at least one commit so the submodule `git clone` succeeds.
  A single `README.md` commit is sufficient.
- The `ide-plugins/` directory must exist in the `dpg` monorepo (created when the first
  submodule is added — Git creates it automatically).

---

## 3. Step-by-step: Adding the Submodules

Run these commands from the root of the `dpg` monorepo.

### 3.1 Create the placeholder repositories (one-time)

```bash
gh repo create dullkingsman/tree-sitter-dpg --public --description "Tree-sitter grammar for .dpg source files"
gh repo create dullkingsman/dpg-lsp          --public --description "Language Server for .dpg (DPG / Declarative PG)"
gh repo create dullkingsman/nvim-dpg         --public --description "Neovim plugin for DPG (.dpg files)"
gh repo create dullkingsman/vscode-dpg       --public --description "VS Code extension for DPG (.dpg files)"
gh repo create dullkingsman/intellij-dpg     --public --description "JetBrains plugin for DPG (.dpg files)"
```

### 3.2 Seed each repo with an initial commit

GitHub's `--add-readme` flag does this automatically:

```bash
gh repo create dullkingsman/tree-sitter-dpg --public --add-readme ...
# or push a local init commit:
git init /tmp/seed && cd /tmp/seed
echo "# tree-sitter-dpg" > README.md
git add . && git commit -m "chore: initial commit"
git remote add origin git@github.com:dullkingsman/tree-sitter-dpg.git
git push -u origin main
```

Repeat for each repo.

### 3.3 Register the submodules

```bash
cd /path/to/dpg   # monorepo root

git submodule add git@github.com:dullkingsman/tree-sitter-dpg.git  ide-plugins/tree-sitter-dpg
git submodule add git@github.com:dullkingsman/dpg-lsp.git           ide-plugins/dpg-lsp
git submodule add git@github.com:dullkingsman/nvim-dpg.git          ide-plugins/nvim-dpg
git submodule add git@github.com:dullkingsman/vscode-dpg.git        ide-plugins/vscode-dpg
git submodule add git@github.com:dullkingsman/intellij-dpg.git      ide-plugins/intellij-dpg
```

Each `git submodule add` command:
- Clones the remote into `ide-plugins/<name>/`
- Appends an entry to `.gitmodules`
- Stages a gitlink (submodule pointer commit) under `ide-plugins/<name>`

### 3.4 Commit the submodule registration

```bash
git add .gitmodules ide-plugins/
git commit -m "chore: add ide-plugins submodules (tree-sitter-dpg, dpg-lsp, nvim-dpg, vscode-dpg, intellij-dpg)"
```

The resulting `.gitmodules` will look like:

```ini
[submodule "ide-plugins/tree-sitter-dpg"]
    path = ide-plugins/tree-sitter-dpg
    url  = git@github.com:dullkingsman/tree-sitter-dpg.git
    branch = main

[submodule "ide-plugins/dpg-lsp"]
    path = ide-plugins/dpg-lsp
    url  = git@github.com:dullkingsman/dpg-lsp.git
    branch = main

[submodule "ide-plugins/nvim-dpg"]
    path = ide-plugins/nvim-dpg
    url  = git@github.com:dullkingsman/nvim-dpg.git
    branch = main

[submodule "ide-plugins/vscode-dpg"]
    path = ide-plugins/vscode-dpg
    url  = git@github.com:dullkingsman/vscode-dpg.git
    branch = main

[submodule "ide-plugins/intellij-dpg"]
    path = ide-plugins/intellij-dpg
    url  = git@github.com:dullkingsman/intellij-dpg.git
    branch = main
```

---

## 4. Cloning the Monorepo (contributor workflow)

Anyone cloning `dpg` who wants the submodules populated:

```bash
# Option A — clone with submodules in one step
git clone --recurse-submodules git@github.com:dullkingsman/dpg.git

# Option B — existing clone, populate afterwards
git submodule update --init --recursive
```

To work on only one plugin:

```bash
git submodule update --init ide-plugins/nvim-dpg
cd ide-plugins/nvim-dpg
# ... work, commit, push to dullkingsman/nvim-dpg as normal ...
```

---

## 5. Updating the Pinned Commit

Each submodule is pinned to a specific commit SHA in the `dpg` monorepo. After a plugin
repo gets new commits that the monorepo should reference:

```bash
cd ide-plugins/nvim-dpg
git pull origin main        # advance to latest
cd ../..
git add ide-plugins/nvim-dpg
git commit -m "chore(nvim-dpg): pin to v0.2.0"
```

The CI job in `dpg` should use `--recurse-submodules` (or `actions/checkout` with
`submodules: recursive`) so the pinned commit is always checked out in CI:

```yaml
# .github/workflows/ci.yml
- uses: actions/checkout@v4
  with:
    submodules: recursive
```

---

## 6. Per-Submodule Scaffold Specification

### 6.1 `tree-sitter-dpg`

**Purpose**: Tree-sitter grammar for syntax highlighting, text objects, and structural queries
on `.dpg` files. Used by Neovim (via `nvim-treesitter`), Helix, Zed, and GitHub. Does NOT
depend on the Go pipeline.

**Initial file tree**:

```
tree-sitter-dpg/
├── grammar.js                   # Grammar definition
├── src/
│   ├── parser.c                 # Generated by tree-sitter generate (do not edit)
│   └── tree_sitter/
│       └── parser.h             # tree-sitter runtime header (vendored)
├── queries/
│   ├── highlights.scm           # Syntax highlighting queries
│   ├── locals.scm               # Scope/local variable queries
│   └── injections.scm           # Language injections (function bodies)
├── test/
│   └── corpus/
│       ├── tables.txt           # Corpus test: TABLE declarations
│       ├── views.txt            # Corpus test: VIEW declarations
│       ├── functions.txt        # Corpus test: FUNCTION declarations
│       ├── types.txt            # Corpus test: ENUM / DOMAIN / TYPE
│       └── schemas.txt          # Corpus test: SCHEMA blocks
├── bindings/
│   ├── node/
│   │   ├── index.js
│   │   └── package.json
│   └── python/
│       ├── tree_sitter_dpg/
│       │   └── __init__.py
│       └── setup.py
├── package.json
├── .github/
│   └── workflows/
│       └── ci.yml               # npx tree-sitter test on push/PR
└── README.md
```

**`package.json` key fields**:

```json
{
  "name": "tree-sitter-dpg",
  "version": "0.1.0",
  "description": "Tree-sitter grammar for DPG (.dpg) source files",
  "main": "bindings/node",
  "keywords": ["tree-sitter", "dpg", "postgresql", "schema"],
  "scripts": {
    "build":    "npx tree-sitter generate",
    "test":     "npx tree-sitter test",
    "highlight": "npx tree-sitter highlight"
  },
  "tree-sitter": [{ "scope": "source.dpg" }]
}
```

**Grammar design notes** (see ROADMAP §3 for full spec):

- External scanner (`src/scanner.c`) handles dollar-quoted strings (`$$...$$`,
  `$tag$...$tag$`) and nested block comments (`/* /* */ */`) — both require stateful
  token matching that cannot be expressed in grammar.js rules alone.
- Top-level rule: `source_file → repeat(_declaration)`
- `_declaration` covers all DPG object types (table, view, function, enum, domain, schema
  block, extension, sequence, role, etc.)
- Complex SQL expressions (CHECK predicates, DEFAULT expressions, FK action clauses) are
  treated as opaque `raw_sql` leaf nodes in v1 and refined incrementally.
- Highlight queries map DPG keywords to standard `@keyword`, `@type`, `@function`,
  `@property`, `@string`, `@comment`, `@number`, `@operator` captures.
- Injection queries inject the appropriate grammar into dollar-quoted function bodies
  based on the `LANGUAGE` attribute (`plpgsql`, `sql`, `python`, etc.).

**CI** (`.github/workflows/ci.yml`):

```yaml
name: CI
on: [push, pull_request]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with: { node-version: '20' }
      - run: npm ci
      - run: npm test     # npx tree-sitter test
```

---

### 6.2 `dpg-lsp`

**Purpose**: Language Server Protocol server for `.dpg` files. Provides diagnostics, hover,
go-to-definition, completions, and document formatting by driving the DPG compiler
(`pkg/dpg`) directly.

**Dependencies**:
- `github.com/dullkingsman/dpg` — the public API from `pkg/dpg`
- `github.com/tliron/glsp` — Go JSON-RPC 2.0 LSP framework

**Initial file tree**:

```
dpg-lsp/
├── cmd/
│   └── dpg-lsp/
│       └── main.go              # Entry point; --stdio / --tcp flags
├── internal/
│   ├── server/
│   │   └── server.go            # glsp handler registration; capability negotiation
│   ├── workspace/
│   │   ├── project.go           # dpg.Discover(); cluster/database resolution
│   │   └── document.go          # Open-document cache; debounced recompile trigger
│   └── analysis/
│       ├── diagnostics.go       # compiler errors + linter → LSP Diagnostic
│       ├── hover.go             # IR object lookup at cursor → MarkupContent
│       ├── definition.go        # go-to-definition via obj.Pos()
│       └── completion.go        # context-sensitive completions from IR
├── go.mod                       # module github.com/dullkingsman/dpg-lsp
├── go.sum
├── .github/
│   └── workflows/
│       ├── ci.yml               # go test / go vet on push
│       └── release.yml          # cross-platform binaries on tag
└── README.md
```

**`go.mod`**:

```
module github.com/dullkingsman/dpg-lsp

go 1.25.6

require (
    github.com/dullkingsman/dpg v0.2.0
    github.com/tliron/glsp      v0.2.2
)
```

**Architecture notes**:

- Communication mode: `--stdio` (default) for editor integration, `--tcp :PORT` for
  debugging. Controlled by a flag in `main.go`.
- Workspace model: on `initialize`, call `dpg.Discover(rootUri)` to resolve all clusters
  and databases. Maintain an IR cache: `map[string][]dpg.IRObject` keyed by
  `"clusterName/databaseName"`.
- Recompile trigger: on `textDocument/didOpen` and `textDocument/didChange`, debounce
  300 ms, then call `dpg.Compile(db.SourceFiles, db.Dir)`. Unsaved edits are written to a
  temp file before compiling and cleaned up afterwards.
- `SourcePos` to LSP `Range`: `{line: pos.Line - 1, character: pos.Col - 1}` (LSP uses
  0-based lines/columns; DPG uses 1-based).
- Document formatting (`textDocument/formatting`): shell-out to `dpg fmt` on the document
  file and return the delta as `TextEdit[]`.

**LSP features** (implemented in order of value):

| Priority | Feature | Handler |
|---|---|---|
| 1 | Diagnostics | `textDocument/publishDiagnostics` |
| 2 | Hover | `textDocument/hover` |
| 3 | Go-to-definition | `textDocument/definition` |
| 4 | Completions | `textDocument/completion` |
| 5 | Document formatting | `textDocument/formatting` |

**CI** (`.github/workflows/ci.yml`):

```yaml
name: CI
on:
  push:    { branches: [master] }
  pull_request: { branches: [master] }
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version-file: go.mod, cache: true }
      - run: go vet ./...
      - run: go test ./...
```

**Release** (`.github/workflows/release.yml`): mirrors `dpg`'s release workflow —
cross-compiles for `linux/amd64`, `linux/arm64`, `darwin/arm64`, `windows/amd64`; publishes
to GitHub Releases on `v*.*.*` tags.

---

### 6.3 `nvim-dpg`

**Purpose**: Neovim plugin. Registers the `.dpg` filetype, configures
`nvim-treesitter` with the `tree-sitter-dpg` grammar, provides an `nvim-lspconfig`
entry for `dpg-lsp`, and optionally runs `dpg fmt` on save.

**Dependencies**:
- `nvim-treesitter/nvim-treesitter` (optional, for advanced highlighting)
- `neovim/nvim-lspconfig` (recommended, for LSP setup)
- `dpg-lsp` binary on `$PATH`

**Initial file tree**:

```
nvim-dpg/
├── lua/
│   └── dpg/
│       ├── init.lua             # setup(opts) entry point
│       ├── lsp.lua              # nvim-lspconfig entry for dpg_ls
│       ├── treesitter.lua       # nvim-treesitter parser registration
│       └── fmt.lua              # format-on-save autocmd (BufWritePre)
├── ftdetect/
│   └── dpg.vim                  # autocmd BufRead,BufNewFile *.dpg set ft=dpg
├── ftplugin/
│   └── dpg.vim                  # filetype-specific settings (commentstring, etc.)
├── queries/
│   └── dpg/
│       ├── highlights.scm       # symlinked / copied from tree-sitter-dpg
│       ├── locals.scm
│       └── injections.scm
├── .github/
│   └── workflows/
│       └── ci.yml               # luacheck / stylua lint on push
└── README.md
```

**`lua/dpg/init.lua`** — public API:

```lua
local M = {}

---@class DpgOpts
---@field fmt_on_save boolean|nil   -- default: true
---@field lsp         boolean|nil   -- default: true (requires nvim-lspconfig)
---@field treesitter  boolean|nil   -- default: true (requires nvim-treesitter)

function M.setup(opts)
    opts = vim.tbl_deep_extend("force", {
        fmt_on_save = true,
        lsp         = true,
        treesitter  = true,
    }, opts or {})

    if opts.treesitter then require("dpg.treesitter").setup() end
    if opts.lsp        then require("dpg.lsp").setup() end
    if opts.fmt_on_save then require("dpg.fmt").setup() end
end

return M
```

**`lua/dpg/lsp.lua`** — wires up `dpg-lsp`:

```lua
-- Registers a ready-to-use lspconfig entry.
-- Users who already manage their LSP config can skip setup() and configure
-- directly via lspconfig.dpg_ls.setup({}).
local function setup()
    local ok, lspconfig = pcall(require, "lspconfig")
    if not ok then return end

    local configs = require("lspconfig.configs")
    if not configs.dpg_ls then
        configs.dpg_ls = {
            default_config = {
                cmd        = { "dpg-lsp", "--stdio" },
                filetypes  = { "dpg" },
                root_dir   = lspconfig.util.root_pattern("dpg.toml"),
                settings   = {},
            },
        }
    end
    lspconfig.dpg_ls.setup({})
end

return { setup = setup }
```

**`lua/dpg/treesitter.lua`** — registers the parser:

```lua
local function setup()
    local ok, parsers = pcall(require, "nvim-treesitter.parsers")
    if not ok then return end

    parsers.get_parser_configs().dpg = {
        install_info = {
            url           = "https://github.com/dullkingsman/tree-sitter-dpg",
            files         = { "src/parser.c", "src/scanner.c" },
            branch        = "main",
            generate_requires_npm = false,
        },
        filetype = "dpg",
    }
end

return { setup = setup }
```

**`lua/dpg/fmt.lua`** — format on save:

```lua
local function setup()
    vim.api.nvim_create_autocmd("BufWritePre", {
        pattern  = "*.dpg",
        callback = function(ev)
            local path = vim.api.nvim_buf_get_name(ev.buf)
            if path == "" then return end
            -- Format in-place; reload buffer on success.
            local result = vim.system({ "dpg", "fmt", path }, { text = true }):wait()
            if result.code == 0 then
                vim.cmd("edit!")   -- reload file from disk
            end
        end,
    })
end

return { setup = setup }
```

**Installation** (lazy.nvim):

```lua
{
    "dullkingsman/nvim-dpg",
    ft = "dpg",
    dependencies = {
        "nvim-treesitter/nvim-treesitter",   -- optional but recommended
        "neovim/nvim-lspconfig",             -- optional but recommended
    },
    opts = {
        fmt_on_save = true,
        lsp         = true,
        treesitter  = true,
    },
}
```

**CI** (`.github/workflows/ci.yml`):

```yaml
name: CI
on: [push, pull_request]
jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: JohnnyMorganz/stylua-action@v4
        with: { args: "--check lua/" }
      - uses: lunarmodules/luacheck-action@v1
        with: { args: "lua/" }
```

---

### 6.4 `vscode-dpg`

**Purpose**: VS Code extension. Provides `.dpg` syntax highlighting via a TextMate grammar
(interim, ships first), LSP client for `dpg-lsp`, and a format-on-save provider.

**Dependencies**:
- `vscode-languageclient` (npm) — LSP client
- `dpg-lsp` binary (bundled or required on `$PATH`)

**Initial file tree**:

```
vscode-dpg/
├── src/
│   ├── extension.ts             # activate(); registers LSP client + formatter
│   └── client.ts                # LanguageClient setup
├── syntaxes/
│   └── dpg.tmLanguage.json      # TextMate grammar (generated from grammar.js or hand-written)
├── language-configuration.json  # Comment toggling, bracket matching, indentation rules
├── package.json                 # Extension manifest
├── tsconfig.json
├── .vscodeignore
├── .github/
│   └── workflows/
│       ├── ci.yml               # npm test + tsc --noEmit on push
│       └── release.yml          # vsce publish on tag push
└── README.md
```

**`package.json`** (key `contributes` section):

```json
{
  "name": "vscode-dpg",
  "displayName": "DPG — Declarative PG",
  "description": "Language support for .dpg source files",
  "version": "0.1.0",
  "engines": { "vscode": "^1.85.0" },
  "categories": ["Programming Languages", "Formatters"],
  "activationEvents": ["onLanguage:dpg"],
  "contributes": {
    "languages": [{
      "id": "dpg",
      "aliases": ["DPG", "Declarative PG"],
      "extensions": [".dpg"],
      "configuration": "./language-configuration.json"
    }],
    "grammars": [{
      "language": "dpg",
      "scopeName": "source.dpg",
      "path": "./syntaxes/dpg.tmLanguage.json"
    }],
    "configuration": {
      "title": "DPG",
      "properties": {
        "dpg.lsp.enabled":        { "type": "boolean", "default": true },
        "dpg.lsp.path":           { "type": "string",  "default": "dpg-lsp" },
        "dpg.fmt.onSave":         { "type": "boolean", "default": true },
        "dpg.fmt.path":           { "type": "string",  "default": "dpg" }
      }
    }
  },
  "main": "./out/extension.js",
  "scripts": {
    "compile": "tsc -p ./",
    "watch":   "tsc -watch -p ./",
    "test":    "vscode-test",
    "package": "vsce package"
  },
  "dependencies": {
    "vscode-languageclient": "^9.0.1"
  },
  "devDependencies": {
    "@types/vscode":         "^1.85.0",
    "@vscode/test-cli":      "^0.0.4",
    "@vscode/test-electron": "^2.3.8",
    "@vscode/vsce":          "^2.24.0",
    "typescript":            "^5.3.2"
  }
}
```

**`src/extension.ts`**:

```typescript
import * as vscode from "vscode";
import { LanguageClient, ServerOptions, TransportKind } from "vscode-languageclient/node";

let client: LanguageClient | undefined;

export function activate(ctx: vscode.ExtensionContext) {
    const cfg = vscode.workspace.getConfiguration("dpg");

    // LSP client
    if (cfg.get<boolean>("lsp.enabled", true)) {
        const serverPath = cfg.get<string>("lsp.path", "dpg-lsp");
        const serverOpts: ServerOptions = {
            command: serverPath,
            args: ["--stdio"],
            transport: TransportKind.stdio,
        };
        client = new LanguageClient("dpg-lsp", "DPG Language Server", serverOpts, {
            documentSelector: [{ scheme: "file", language: "dpg" }],
        });
        ctx.subscriptions.push(client.start());
    }

    // Format on save
    if (cfg.get<boolean>("fmt.onSave", true)) {
        const fmtPath = cfg.get<string>("fmt.path", "dpg");
        ctx.subscriptions.push(
            vscode.workspace.onWillSaveTextDocument(e => {
                if (e.document.languageId !== "dpg") return;
                e.waitUntil(formatDocument(fmtPath, e.document));
            })
        );
    }
}

async function formatDocument(fmtPath: string, doc: vscode.TextDocument): Promise<vscode.TextEdit[]> {
    // Shell-out to `dpg fmt --diff` and convert output to TextEdits.
    // In practice, replaces the full document content via a single TextEdit.
    const { execFile } = require("child_process");
    return new Promise(resolve => {
        execFile(fmtPath, ["fmt", doc.uri.fsPath], (err: Error|null, stdout: string) => {
            if (err) { resolve([]); return; }
            // Re-read the formatted file and replace document range.
            const formatted = require("fs").readFileSync(doc.uri.fsPath, "utf8");
            const full = new vscode.Range(
                doc.positionAt(0),
                doc.positionAt(doc.getText().length)
            );
            resolve([vscode.TextEdit.replace(full, formatted)]);
        });
    });
}

export function deactivate() {
    return client?.stop();
}
```

**TextMate grammar** (`syntaxes/dpg.tmLanguage.json`): ships a manually-curated TextMate
grammar initially. Once `tree-sitter-dpg` is stable, replace with the tree-sitter grammar
via the `vscode-tree-sitter` mechanism or VS Code's native tree-sitter support (1.91+).

**CI/Release**:

- CI: `npm ci && tsc --noEmit && npm test`
- Release: `vsce package` + publish to VS Code Marketplace on `v*.*.*` tags via
  `vsce publish --pat $VSCE_PAT` in GitHub Actions.

---

### 6.5 `intellij-dpg`

**Purpose**: JetBrains plugin. Registers `.dpg` as a file type and uses the JetBrains
built-in LSP client (available since IntelliJ 2023.1) to connect to `dpg-lsp`.

**Build system**: Gradle + `gradle-intellij-plugin`

**Initial file tree**:

```
intellij-dpg/
├── src/
│   └── main/
│       ├── kotlin/
│       │   └── com/dullkingsman/dpg/
│       │       ├── DpgFileType.kt       # FileType registration
│       │       ├── DpgLanguage.kt       # Language singleton
│       │       ├── DpgIcons.kt          # Icon loading
│       │       └── DpgLspServerDescriptor.kt  # LSP server descriptor
│       └── resources/
│           ├── META-INF/
│           │   └── plugin.xml           # Plugin descriptor
│           └── icons/
│               └── dpg.svg             # File type icon
├── build.gradle.kts
├── settings.gradle.kts
├── gradle/
│   └── wrapper/
│       └── gradle-wrapper.properties
├── .github/
│   └── workflows/
│       ├── ci.yml                       # gradle build on push
│       └── release.yml                  # publish to JetBrains Marketplace on tag
└── README.md
```

**`plugin.xml`** (key entries):

```xml
<idea-plugin>
  <id>com.dullkingsman.dpg</id>
  <name>DPG — Declarative PG</name>
  <version>0.1.0</version>
  <vendor>dullkingsman</vendor>
  <description>Language support for .dpg (DPG / Declarative PG) source files.</description>

  <depends>com.intellij.modules.platform</depends>

  <extensions defaultExtensionNs="com.intellij">
    <!-- File type -->
    <fileType name="DPG" implementationClass="com.dullkingsman.dpg.DpgFileType"
              language="DPG" extensions="dpg" />
    <!-- LSP client (IntelliJ 2023.1+) -->
    <platform.lsp.serverSupportProvider
        implementation="com.dullkingsman.dpg.DpgLspServerDescriptor"/>
  </extensions>
</idea-plugin>
```

**`DpgLspServerDescriptor.kt`**:

```kotlin
package com.dullkingsman.dpg

import com.intellij.openapi.project.Project
import com.intellij.openapi.vfs.VirtualFile
import com.intellij.platform.lsp.api.LspServerSupportProvider
import com.intellij.platform.lsp.api.LspServerDescriptor
import com.intellij.platform.lsp.api.ProjectWideLspServerDescriptor

class DpgLspServerDescriptor(project: Project) :
    ProjectWideLspServerDescriptor(project, "DPG Language Server") {

    override fun isSupportedFile(file: VirtualFile) = file.extension == "dpg"

    override fun createCommandLine() = com.intellij.execution.configurations
        .GeneralCommandLine("dpg-lsp", "--stdio")
}

class DpgLspServerSupportProvider : LspServerSupportProvider {
    override fun fileOpened(project: Project, file: VirtualFile, serverStarter: LspServerSupportProvider.LspServerStarter) {
        if (file.extension == "dpg") {
            serverStarter.ensureServerStarted(DpgLspServerDescriptor(project))
        }
    }
}
```

**`build.gradle.kts`** (key sections):

```kotlin
plugins {
    id("org.jetbrains.intellij") version "1.17.2"
    kotlin("jvm") version "1.9.22"
}

intellij {
    version.set("2023.1")          // minimum supported IntelliJ version
    type.set("IC")                  // IntelliJ Community
    plugins.set(listOf())
}

tasks.patchPluginXml {
    sinceBuild.set("231")           // 2023.1
    untilBuild.set("")              // no upper bound
}

tasks.publishPlugin {
    token.set(System.getenv("JETBRAINS_TOKEN"))
}
```

**CI** (`.github/workflows/ci.yml`):

```yaml
name: CI
on: [push, pull_request]
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-java@v4
        with: { distribution: temurin, java-version: '17' }
      - uses: gradle/actions/setup-gradle@v3
      - run: ./gradlew build verifyPlugin
```

**Release** (`.github/workflows/release.yml`): `./gradlew publishPlugin` on `v*.*.*` tag push,
using `JETBRAINS_TOKEN` repository secret.

---

## 7. Cross-Submodule Dependencies

```
dpg (monorepo)
  └── pkg/dpg          ← public Go API used by dpg-lsp
      ↑
ide-plugins/dpg-lsp    ← imports github.com/dullkingsman/dpg
      ↑
ide-plugins/nvim-dpg       (shells out to dpg-lsp binary)
ide-plugins/vscode-dpg     (spawns dpg-lsp as child process)
ide-plugins/intellij-dpg   (spawns dpg-lsp via JetBrains LSP client)

ide-plugins/tree-sitter-dpg (standalone; no Go deps)
      ↑
ide-plugins/nvim-dpg       (registers tree-sitter-dpg parser with nvim-treesitter)
ide-plugins/vscode-dpg     (uses grammar for TextMate/tree-sitter highlighting)
```

- `dpg-lsp` gets `pkg/dpg` via normal Go module resolution (`go get` / `go.mod`).
  No path replace directives needed after `pkg/dpg` is tagged.
- Editor plugins get `dpg-lsp` as a binary on `$PATH`; they do not import its Go source.
- `tree-sitter-dpg` is consumed by editors as a git URL in their tree-sitter parser configs
  (nvim-treesitter's `install_info.url`, VS Code's tree-sitter grammar source).

---

## 8. Release Sequencing

```
1. dpg v0.2.0 tag       — pkg/dpg stable, dpg fmt shipped
2. tree-sitter-dpg v0.1.0 — grammar skeleton; basic TABLE/FUNCTION/ENUM coverage
3. dpg-lsp v0.1.0        — diagnostics + hover; imports dpg v0.2.0
4. nvim-dpg v0.1.0       — filetype + treesitter + lspconfig entry
5. vscode-dpg v0.1.0     — TextMate grammar + LSP client
6. intellij-dpg v0.1.0   — file type + LSP client
```

Steps 2 and 3 can proceed in parallel once step 1 is tagged.
Steps 4, 5, 6 depend on step 3 (dpg-lsp binary available).

---

## 9. Updating `.github/workflows/ci.yml` in `dpg`

Add submodule checkout so CI validates the pinned submodule state:

```yaml
- uses: actions/checkout@v4
  with:
    submodules: recursive
```

No other changes to the `dpg` CI are needed — the plugin repos each have their own CI.
