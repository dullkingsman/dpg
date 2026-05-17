local M = {}

function M.setup()
  local ok, parsers = pcall(require, "nvim-treesitter.parsers")
  if not ok then
    vim.notify("[dpg] nvim-treesitter not found; tree-sitter highlighting disabled", vim.log.levels.WARN)
    return
  end

  parsers.get_parser_configs().dpg = {
    install_info = {
      url                            = "https://github.com/dullkingsman/dpg",
      files                          = { "src/parser.c", "src/scanner.c" },
      branch                         = "master",
      subdir                         = "lang/grammar",
      generate_requires_npm          = false,
      requires_generate_from_grammar = false,
    },
    filetype = "dpg",
  }
end

return M
