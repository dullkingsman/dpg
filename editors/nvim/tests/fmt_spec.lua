describe("DPG format-on-save", function()
  local fmt = require("dpg.fmt")

  before_each(function()
    -- Fresh buffer for each test.
    vim.cmd("new")
    vim.bo.filetype = "dpg"
  end)

  after_each(function()
    vim.cmd("bd!")
  end)

  it("registers a BufWritePre autocmd for *.dpg after setup", function()
    fmt.setup()

    local aus = vim.api.nvim_get_autocmds({ group = "DpgFmt", event = "BufWritePre" })
    assert.is_true(#aus > 0, "DpgFmt BufWritePre autocmd should be registered")
  end)

  it("autocmd pattern is *.dpg", function()
    fmt.setup()

    local aus = vim.api.nvim_get_autocmds({ group = "DpgFmt", event = "BufWritePre" })
    local found = false
    for _, au in ipairs(aus) do
      if au.pattern == "*.dpg" then found = true end
    end
    assert.is_true(found, "BufWritePre autocmd should have pattern *.dpg")
  end)

  it("calling setup twice clears and re-registers the augroup", function()
    fmt.setup()
    fmt.setup()

    local aus = vim.api.nvim_get_autocmds({ group = "DpgFmt", event = "BufWritePre" })
    -- { clear = true } on create_augroup means only one autocmd should exist.
    assert.are.equal(1, #aus)
  end)

  it("does not error when dpg binary is absent", function()
    fmt.setup()

    -- Simulate BufWritePre on a named buffer whose path does not exist.
    -- The callback should silently return rather than raise.
    local buf = vim.api.nvim_get_current_buf()
    vim.api.nvim_buf_set_name(buf, "/tmp/dpg-fmt-test-nonexistent.dpg")

    assert.has_no_errors(function()
      -- Fire the autocmd manually.
      vim.api.nvim_exec_autocmds("BufWritePre", {
        group   = "DpgFmt",
        pattern = "*.dpg",
        buf     = buf,
      })
    end)
  end)

  it("does not error for unnamed buffer (path == '')", function()
    fmt.setup()

    local buf = vim.api.nvim_get_current_buf()
    -- Keep the buffer unnamed (empty path).

    assert.has_no_errors(function()
      vim.api.nvim_exec_autocmds("BufWritePre", {
        group   = "DpgFmt",
        pattern = "*.dpg",
        buf     = buf,
      })
    end)
  end)
end)
