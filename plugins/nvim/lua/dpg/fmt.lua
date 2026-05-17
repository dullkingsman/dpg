-- Requires Neovim 0.10+  (vim.system API)
local M = {}

function M.setup()
  vim.api.nvim_create_autocmd("BufWritePre", {
    group   = vim.api.nvim_create_augroup("DpgFmt", { clear = true }),
    pattern = "*.dpg",
    callback = function(ev)
      if not vim.system then return end  -- vim.system requires Neovim 0.10+
      if vim.fn.executable("dpg") == 0 then return end
      local path = vim.api.nvim_buf_get_name(ev.buf)
      if path == "" then return end

      -- Write buffer content to a temp file so dpg fmt sees the in-memory
      -- state (BufWritePre fires before the actual disk write).
      local tmp = path .. ".dpg-lsp-tmp"
      local lines = vim.api.nvim_buf_get_lines(ev.buf, 0, -1, false)
      local f = io.open(tmp, "w")
      if not f then return end
      f:write(table.concat(lines, "\n"))
      f:close()

      local result = vim.system({ "dpg", "fmt", tmp }, { text = true }):wait()
      if result.code == 0 then
        local formatted = vim.fn.readfile(tmp)
        vim.api.nvim_buf_set_lines(ev.buf, 0, -1, false, formatted)
      end
      os.remove(tmp)
    end,
  })
end

return M
