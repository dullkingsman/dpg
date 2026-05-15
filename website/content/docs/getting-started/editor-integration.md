---
title: "Editor Integration"
description: "Install and configure DPG plugins for VS Code, Neovim, Helix, and JetBrains IDEs. Covers syntax highlighting, LSP, and format-on-save."
weight: 4
---

DPG ships native plugins for VS Code, Neovim, Helix, and JetBrains IDEs. Each plugin provides:

- **Syntax highlighting** — via the DPG tree-sitter grammar
- **Diagnostics, hover, go-to-definition, completions** — powered by `dpg-lsp`
- **Format on save** — runs `dpg fmt` before each write

---

## Prerequisites

### dpg

`dpg` must be on your `PATH`. See [Installation](./installation).

### dpg-lsp

`dpg-lsp` is the language server that powers diagnostics, hover, and completions. Install it once and all editors share it:

```bash
go install github.com/dullkingsman/dpg-lsp/cmd/dpg-lsp@latest
```

Ensure `$(go env GOPATH)/bin` is on your `PATH`:

```bash
# Add to ~/.bashrc or ~/.zshrc
export PATH="$(go env GOPATH)/bin:$PATH"
```

Verify both binaries are reachable:

```bash
dpg --version
dpg-lsp --version
```

---

## VS Code

The official VS Code extension provides syntax highlighting, LSP integration, and format-on-save. It is published on the VS Code Marketplace as **`dullkingsman.vscode-dpg`**.

### Install

**From the Marketplace (recommended):**

Open the Extensions panel (`Ctrl+Shift+X`), search for **"DPG Declarative PG"**, and click Install.

Or from the command line:

```bash
code --install-extension dullkingsman.vscode-dpg
```

**From source (VSIX):**

```bash
cd editors/vscode
npm install
npm run package        # produces vscode-dpg-*.vsix
code --install-extension vscode-dpg-*.vsix
```

### Configuration

Add any of the following to your workspace or user `settings.json`:

```json
{
  "dpg.lsp.enabled": true,
  "dpg.lsp.path": "dpg-lsp",
  "dpg.fmt.onSave": true,
  "dpg.fmt.path": "dpg"
}
```

| Setting | Default | Description |
|---|---|---|
| `dpg.lsp.enabled` | `true` | Enable the DPG language server |
| `dpg.lsp.path` | `"dpg-lsp"` | Path to the `dpg-lsp` binary |
| `dpg.fmt.onSave` | `true` | Run `dpg fmt` on save |
| `dpg.fmt.path` | `"dpg"` | Path to the `dpg` binary |

If `dpg` or `dpg-lsp` are not on your system `PATH`, set the full absolute path in the corresponding `*.path` setting.

---

## Neovim

The Neovim plugin lives in `editors/nvim/` of the DPG repository. It requires **Neovim 0.10+** and optionally:

- [`nvim-lspconfig`](https://github.com/neovim/nvim-lspconfig) — for LSP support
- [`nvim-treesitter`](https://github.com/nvim-treesitter/nvim-treesitter) — for syntax highlighting

### Install

**Via lazy.nvim (recommended):**

Clone the DPG repo, then point lazy.nvim at the `editors/nvim` subdirectory:

```bash
git clone https://github.com/dullkingsman/dpg \
  ~/.local/share/dpg
```

```lua
-- lazy.nvim plugin spec
{
  dir = vim.fn.expand("~/.local/share/dpg/editors/nvim"),
  name = "dpg.nvim",
  config = function()
    require("dpg").setup()
  end,
}
```

**Manual (no plugin manager):**

```lua
-- init.lua
vim.opt.rtp:prepend(vim.fn.expand("~/.local/share/dpg/editors/nvim"))
require("dpg").setup()
```

### Setup

```lua
require("dpg").setup({
  fmt_on_save = true,   -- run dpg fmt before every write
  lsp         = true,   -- start dpg-lsp for open .dpg files
  treesitter  = true,   -- register the tree-sitter grammar
})
```

All three options default to `true`. To use only syntax highlighting:

```lua
require("dpg").setup({ lsp = false, fmt_on_save = false })
```

### LSP configuration

When `lsp = true`, the plugin registers a `dpg_ls` server in `nvim-lspconfig`. Pass standard lspconfig options via `setup`:

```lua
require("dpg").setup({
  lsp = true,
  on_attach = function(client, bufnr)
    -- keybindings, e.g. vim.keymap.set("n", "gd", vim.lsp.buf.definition, ...)
  end,
  capabilities = require("cmp_nvim_lsp").default_capabilities(),
})
```

The server root is detected by the nearest `dpg.toml` file.

### Tree-sitter grammar

When `treesitter = true`, the plugin registers the DPG parser. Install it once inside Neovim:

```
:TSInstall dpg
```

Highlighting queries are bundled with the plugin — no extra steps needed.

---

## Helix

Helix integrates with dpg-lsp and the DPG tree-sitter grammar natively via `languages.toml`.

### Configure the language

Add the following to `~/.config/helix/languages.toml`:

```toml
[[language]]
name              = "dpg"
scope             = "source.dpg"
file-types        = ["dpg"]
comment-token     = "--"
block-comment-tokens = { start = "/*", end = "*/" }
auto-format       = true
indent            = { tab-width = 4, unit = "    " }

formatter = { command = "dpg", args = ["fmt", "--stdin"] }

language-servers = ["dpg-lsp"]

[language.grammar]
source = { git = "https://github.com/dullkingsman/tree-sitter-dpg", rev = "main" }

[language-server.dpg-lsp]
command = "dpg-lsp"
args    = ["--stdio"]
```

### Install the tree-sitter grammar

After adding the config, fetch and compile the grammar:

```bash
hx --grammar fetch
hx --grammar build
```

### Verify

Open a `.dpg` file. The status bar should show `dpg` as the language. Run `:log-open` to check for LSP startup errors.

---

## JetBrains IDEs

The JetBrains plugin works with IntelliJ IDEA, GoLand, DataGrip, PyCharm, and any other JetBrains IDE 2023.1 or later. It provides:

- Syntax highlighting and `.dpg` file type recognition (all editions)
- LSP-powered diagnostics, hover, and completions (**IntelliJ IDEA Ultimate 2023.2+ only**)

### Install from the Marketplace

1. Open **Settings → Plugins → Marketplace**.
2. Search for **"DPG Declarative PG"**.
3. Click **Install** and restart the IDE.

### Install from disk

Build the plugin locally:

```bash
cd editors/idea
./gradlew buildPlugin
# produces build/distributions/dpg-*.zip
```

Then: **Settings → Plugins → ⚙ → Install Plugin from Disk…** → select the `.zip`.

### LSP support

LSP features require:

- **IntelliJ IDEA Ultimate 2023.2** or later
- `dpg-lsp` on `$PATH`

In Community Edition the plugin registers the DPG file type and highlights syntax; LSP extensions are silently skipped.

If `dpg-lsp` is not on your system `PATH`, configure the full path under **Settings → Languages & Frameworks → DPG → Language Server path**.

---

## Format on Save — any editor

For editors not listed above, run `dpg fmt` manually:

```sh
dpg fmt path/to/schema.dpg   # format one file
dpg fmt schemas/             # format all .dpg files under a directory
dpg fmt                      # format all source files in the project
```

### CI gate

Add `dpg fmt --check` to your CI pipeline to block unformatted files:

```yaml
# GitHub Actions
- name: Check DPG formatting
  run: dpg fmt --check
```

`--check` exits non-zero if any file would be reformatted without writing changes.
