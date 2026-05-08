---
title: "Editor Integration"
description: "Configure format-on-save for .dpg files in VS Code, Neovim, Helix, and JetBrains IDEs."
weight: 4
---

`dpg fmt` rewrites `.dpg` source files to canonical style. This guide shows how to trigger it automatically when you save a file in common editors.

The command to run on every save is:

```sh
dpg fmt <path-to-file>
```

---

## VS Code

Install the [**Run on Save**](https://marketplace.visualstudio.com/items?itemName=emeraldwalk.RunOnSave) extension (`emeraldwalk.RunOnSave`), then add the following to your `.vscode/settings.json`:

```json
{
  "emeraldwalk.runonsave": {
    "commands": [
      {
        "match": "\\.dpg$",
        "cmd": "dpg fmt ${file}"
      }
    ]
  }
}
```

After saving a `.dpg` file VS Code will run `dpg fmt` and reload the file from disk.

---

## Neovim

Add the following to your `init.lua` (or the equivalent `autocmd` block in `init.vim`):

```lua
vim.api.nvim_create_autocmd("BufWritePost", {
  pattern = "*.dpg",
  callback = function()
    local file = vim.fn.expand("%:p")
    vim.fn.system({ "dpg", "fmt", file })
    vim.cmd("checktime")   -- reload the buffer if it changed on disk
  end,
})
```

`checktime` picks up the file rewritten by `dpg fmt` so the buffer stays in sync.

---

## Helix

Helix calls formatters via stdin → stdout. Create a thin wrapper script (e.g. `~/bin/dpg-fmt-stdin`) and make it executable:

```sh
#!/bin/sh
# dpg-fmt-stdin: read .dpg source from stdin, write formatted output to stdout.
tmp=$(mktemp --suffix=.dpg)
trap 'rm -f "$tmp"' EXIT
cat > "$tmp"
dpg fmt "$tmp"
cat "$tmp"
```

Then configure the language in `~/.config/helix/languages.toml`:

```toml
[[language]]
name        = "dpg"
scope       = "source.dpg"
file-types  = ["dpg"]
auto-format = true
formatter   = { command = "dpg-fmt-stdin" }
```

---

## JetBrains IDEs (IntelliJ, GoLand, DataGrip, etc.)

Use **File Watchers** (built-in plugin, available in all JetBrains IDEs):

1. Open **Settings → Tools → File Watchers** and click **+**.
2. Set the following fields:

| Field | Value |
|---|---|
| **Name** | DPG Format |
| **File type** | Other (set scope to `*.dpg`) |
| **Scope** | Current file |
| **Program** | `dpg` |
| **Arguments** | `fmt $FilePath$` |
| **Output paths to refresh** | `$FilePath$` |
| **Working directory** | `$ProjectFileDir$` |

3. Uncheck **Auto-save edited files to trigger the watcher** and check **Trigger the watcher on external changes**.

The IDE will re-read the file after `dpg fmt` completes.

---

## Any editor — shell alias

For editors without a dedicated format-on-save mechanism, save the file normally and run from the terminal:

```sh
dpg fmt path/to/schema.dpg   # format one file
dpg fmt schemas/             # format all .dpg files under a directory
dpg fmt                      # format all source files in the project
```

---

## CI gate

Add `dpg fmt --check` to your CI pipeline to block unformatted files from merging:

```yaml
# GitHub Actions example
- name: Check DPG formatting
  run: dpg fmt --check
```

`--check` exits non-zero if any file would be reformatted without writing any files.
