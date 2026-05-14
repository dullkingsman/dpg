local M = {}

function M.setup()
  vim.api.nvim_create_autocmd("BufWritePre", {
    group   = vim.api.nvim_create_augroup("DpgFmt", { clear = true }),
    pattern = "*.dpg",
    callback = function(ev)
      local path = vim.api.nvim_buf_get_name(ev.buf)
      if path == "" then return end

      -- Write buffer to disk first (BufWritePre fires before the actual write,
      -- so we need to save to a temp file to format the current in-memory state)
      local tmp = path .. ".dpg-lsp-tmp"
      local lines = vim.api.nvim_buf_get_lines(ev.buf, 0, -1, false)
      local f = io.open(tmp, "w")
      if not f then return end
      f:write(table.concat(lines, "\n"))
      f:close()

      local result = vim.system({ "dpg", "fmt", tmp }, { text = true }):wait()
      if result.code == 0 then
        -- Read formatted content back into the buffer
        local formatted = vim.fn.readfile(tmp)
        vim.api.nvim_buf_set_lines(ev.buf, 0, -1, false, formatted)
      end
      os.remove(tmp)
    end,
  })
end

return M
