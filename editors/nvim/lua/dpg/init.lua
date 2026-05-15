-- Requires Neovim 0.10+
local M = {}

---@class DpgOpts
---@field fmt_on_save boolean|nil  default: true
---@field lsp         boolean|nil  default: true  (requires nvim-lspconfig)
---@field treesitter  boolean|nil  default: true  (requires nvim-treesitter)

local defaults = {
  fmt_on_save = true,
  lsp         = true,
  treesitter  = true,
}

function M.setup(opts)
  opts = vim.tbl_deep_extend("force", defaults, opts or {})
  if opts.treesitter then require("dpg.treesitter").setup() end
  if opts.lsp        then require("dpg.lsp").setup() end
  if opts.fmt_on_save then require("dpg.fmt").setup() end
end

return M
