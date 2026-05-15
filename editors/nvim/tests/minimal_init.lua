-- Minimal Neovim init for headless test runs (plenary.nvim busted runner).
-- Usage:
--   nvim --headless -u tests/minimal_init.lua \
--     -c "PlenaryBustedDirectory tests/ {minimal_init='tests/minimal_init.lua'}"

-- Disable shada / swap / undo to keep tests clean.
vim.opt.swapfile  = false
vim.opt.shadafile = "NONE"

-- Resolve the plugin root (the nvim/ directory this file lives in).
local plugin_root = vim.fn.fnamemodify(debug.getinfo(1, "S").source:sub(2), ":h:h")

-- Add nvim-dpg itself to runtimepath so `require("dpg")` works.
vim.opt.runtimepath:prepend(plugin_root)

-- Stub out nvim-treesitter so treesitter.lua gracefully no-ops when the
-- real plugin is not installed in the test environment.
-- One persistent table so writes from setup() are visible to subsequent reads.
local _parser_configs = {}
package.preload["nvim-treesitter.parsers"] = function()
  return {
    get_parser_configs = function() return _parser_configs end,
  }
end

-- Stub out nvim-lspconfig so lsp.lua gracefully no-ops.
-- Shared table: writes to lspconfig.configs are visible on lspconfig[key].
local _lsp_configs = {}
package.preload["lspconfig"] = function()
  return setmetatable({
    util = {
      root_pattern = function(...) return function() return nil end end,
    },
  }, {
    __index = function(_, k)
      if _lsp_configs[k] ~= nil then
        return { setup = function() end }
      end
    end,
  })
end
package.preload["lspconfig.configs"] = function()
  return _lsp_configs
end

-- Load plenary (required for the busted runner).
-- CI: git clone https://github.com/nvim-lua/plenary.nvim /tmp/plenary
-- Dev: install via your plugin manager (packer, lazy, etc.)
local plenary_candidates = {
  "/tmp/plenary",
  vim.fn.expand("~/.local/share/nvim/site/pack/packer/start/plenary.nvim"),
  vim.fn.expand("~/.local/share/nvim/lazy/plenary.nvim"),
}
for _, p in ipairs(plenary_candidates) do
  if vim.fn.isdirectory(p) == 1 then
    vim.opt.runtimepath:append(p)
    break
  end
end
