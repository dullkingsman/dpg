local M = {}

function M.setup(opts)
  local ok, lspconfig = pcall(require, "lspconfig")
  if not ok then
    vim.notify("[dpg] nvim-lspconfig not found; LSP disabled", vim.log.levels.WARN)
    return
  end

  local configs = require("lspconfig.configs")
  if not configs.dpg_ls then
    configs.dpg_ls = {
      default_config = {
        cmd       = { "dpg-lsp", "--stdio" },
        filetypes = { "dpg" },
        root_dir  = lspconfig.util.root_pattern("dpg.toml"),
        settings  = {},
      },
    }
  end

  lspconfig.dpg_ls.setup(opts or {})
end

return M
