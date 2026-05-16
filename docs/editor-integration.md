# Editor Integration

DPG ships native plugins for VS Code, Neovim, Helix, and JetBrains IDEs. Each plugin provides:

- **Syntax highlighting** — via the DPG tree-sitter grammar
- **Diagnostics, hover, go-to-definition, completions** — powered by `dpg-lsp`
- **Format on save** — runs `dpg fmt` before each write

---

## Prerequisites

### dpg

`dpg` must be on your `PATH`. See [Installation](./installation.md).

### dpg-lsp

`dpg-lsp` is the language server that powers diagnostics and completions. Download the pre-built binary for your platform from the [GitHub Releases page](https://github.com/dullkingsman/dpg/releases) and place it somewhere on your `PATH`.

**Linux / macOS:**

```bash
# Replace <version> and <platform> (e.g. v0.2.0, linux-amd64 or darwin-arm64)
curl -L https://github.com/dullkingsman/dpg/releases/download/<version>/dpg-lsp-<platform>.tar.gz \
  | tar xz -C /usr/local/bin
```

**Windows:**

Download `dpg-lsp-windows-amd64.zip` from the releases page, extract `dpg-lsp.exe`, and add its directory to your `PATH`.

Verify both are available:

```bash
dpg --version
dpg-lsp --version
```

---

## VS Code

The official VS Code extension provides syntax highlighting, LSP integration, and format-on-save. It is published on the VS Code Marketplace as **`dullkingsman.vscode-dpg`**.

### Install

**From the Marketplace:**

```
ext install dullkingsman.vscode-dpg
```

Or open the Extensions panel, search for **"DPG Declarative PG"**, and click Install.

**From the command line:**

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

Add to your workspace or user `settings.json`:

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
| `dpg.fmt.path` | `"dpg"` | Path to the `dpg` binary used for formatting |

If `dpg` or `dpg-lsp` are not on your system `PATH`, set the full path in the corresponding `*.path` setting.

---

## Neovim

The Neovim plugin lives in `editors/nvim/` of the DPG repository. It requires **Neovim 0.10+** and optionally:

- [`nvim-lspconfig`](https://github.com/neovim/nvim-lspconfig) — for LSP support
- [`nvim-treesitter`](https://github.com/nvim-treesitter/nvim-treesitter) — for syntax highlighting

### Install

**Via lazy.nvim (recommended):**

Clone the repo once (or use an existing clone), then point lazy at the `editors/nvim` subdirectory:

```lua
-- In your lazy.nvim plugin spec:
{
  dir = vim.fn.stdpath("data") .. "/dpg/editors/nvim",
  name = "dpg.nvim",
  config = function()
    require("dpg").setup()
  end,
}
```

Clone the repo to the expected path:

```bash
git clone https://github.com/dullkingsman/dpg \
  "$(nvim --headless -c 'echo stdpath("data")' -c qa 2>&1)/dpg"
```

**Manual (no plugin manager):**

```bash
# Clone anywhere
git clone https://github.com/dullkingsman/dpg ~/.local/share/dpg

# Add editors/nvim to the runtime path in init.lua
vim.opt.rtp:prepend(vim.fn.expand("~/.local/share/dpg/editors/nvim"))
require("dpg").setup()
```

### Setup

Call `require("dpg").setup()` with any options you want to override:

```lua
require("dpg").setup({
  fmt_on_save = true,   -- run dpg fmt before every write
  lsp         = true,   -- start dpg-lsp for open .dpg files
  treesitter  = true,   -- register the tree-sitter grammar
})
```

All three options default to `true`. To disable LSP (e.g., you only want highlighting):

```lua
require("dpg").setup({ lsp = false })
```

### LSP configuration

When `lsp = true`, the plugin registers a `dpg_ls` server in `nvim-lspconfig` automatically. You can pass any `nvim-lspconfig` options through `setup`:

```lua
require("dpg").setup({
  lsp = true,
  -- Extra opts forwarded to lspconfig.dpg_ls.setup():
  on_attach = function(client, bufnr)
    -- your keybindings here
  end,
  capabilities = require("cmp_nvim_lsp").default_capabilities(),
})
```

The language server root is detected by the nearest `dpg.toml` file.

### Tree-sitter grammar

When `treesitter = true`, the plugin registers the DPG parser. Install it once via `:TSInstall dpg`. Highlighting queries are bundled with the plugin.

---

## Helix

Helix integrates with dpg-lsp and the tree-sitter grammar natively via `languages.toml`.

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

# Requires dpg-lsp on $PATH — download from https://github.com/dullkingsman/dpg/releases
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

Helix will then highlight `.dpg` files with the tree-sitter grammar and format them on save via `dpg fmt --stdin`.

### Verify

Open a `.dpg` file. The status bar should show `dpg` as the language. Run `:log-open` to check for any LSP startup errors.

---

## JetBrains IDEs

The JetBrains plugin works with IntelliJ IDEA, GoLand, DataGrip, PyCharm, and any other JetBrains IDE 2023.1 or later. It provides:

- Syntax highlighting and `.dpg` file type recognition (all editions)
- LSP-powered diagnostics, hover, and completions (IntelliJ IDEA Ultimate 2023.2+ only)

### Install from the Marketplace

1. Open **Settings → Plugins → Marketplace**.
2. Search for **"DPG Declarative PG"**.
3. Click **Install** and restart the IDE.

Or install from the command line using the JetBrains toolbox:

```bash
# IntelliJ IDEA example
idea installPlugin com.dullkingsman.dpg
```

### Install from disk (VSIX / JAR)

Build the plugin locally:

```bash
cd editors/idea
./gradlew buildPlugin
# produces build/distributions/dpg-*.zip
```

Then in the IDE: **Settings → Plugins → ⚙ → Install Plugin from Disk…** and select the `.zip`.

### LSP support (Ultimate only)

LSP features (diagnostics, hover, go-to-definition, completions) require:

- **IntelliJ IDEA Ultimate 2023.2** or later (the bundled LSP plugin is only in Ultimate)
- `dpg-lsp` on `$PATH`

In Community Edition the plugin still registers the DPG file type and provides syntax highlighting; LSP extensions are silently skipped.

If `dpg-lsp` is not on your system `PATH`, configure it under **Settings → Languages & Frameworks → DPG → Language Server path**.

---

## Format on Save — any editor

For editors not listed above, trigger `dpg fmt` manually or via a custom hook:

```sh
dpg fmt path/to/schema.dpg   # format one file
dpg fmt schemas/             # format all .dpg files under a directory
dpg fmt                      # format all source files in the project
```

### CI gate

Add `dpg fmt --check` to your CI pipeline to block unformatted files:

```yaml
# GitHub Actions example
- name: Check DPG formatting
  run: dpg fmt --check
```

`--check` exits non-zero if any file would be reformatted without writing changes.
