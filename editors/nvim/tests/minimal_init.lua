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
package.preload["nvim-treesitter.parsers"] = function()
  return {
    get_parser_configs = function()
      local configs = {}
      return setmetatable(configs, {
        __index  = function(t, k) t[k] = {}; return t[k] end,
        __newindex = function(t, k, v) rawset(t, k, v) end,
      })
    end,
  }
end

-- Stub out nvim-lspconfig so lsp.lua gracefully no-ops.
package.preload["lspconfig"] = function()
  return {
    util = {
      root_pattern = function(...) return function() return nil end end,
    },
  }
end
package.preload["lspconfig.configs"] = function()
  return {}
end

-- Load plenary (required for the busted runner).
-- Plenary must be on runtimepath; install it normally via your plugin manager
-- or clone it alongside this repo for CI:
--   git clone https://github.com/nvim-lua/plenary.nvim /tmp/plenary
--   nvim --headless -u tests/minimal_init.lua ...  (add /tmp/plenary to rtp first)
local plenary_path = vim.fn.expand("~/.local/share/nvim/site/pack/packer/start/plenary.nvim")
if vim.fn.isdirectory(plenary_path) == 1 then
  vim.opt.runtimepath:append(plenary_path)
end
