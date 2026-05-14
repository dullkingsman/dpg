local helpers = require("plenary.busted")
local eq = assert.are.equal

describe("DPG filetype", function()
  before_each(function()
    -- Reset state between tests
    vim.cmd("new")
  end)

  after_each(function()
    vim.cmd("bd!")
  end)

  it("detects .dpg files as filetype=dpg", function()
    -- Simulate opening a .dpg file
    vim.cmd("edit test_file.dpg")
    eq("dpg", vim.bo.filetype)
  end)

  it("sets commentstring to -- %s", function()
    vim.cmd("edit test_file.dpg")
    -- Trigger ftplugin
    vim.bo.filetype = "dpg"
    eq("--%s", vim.bo.commentstring)
  end)

  it("sets shiftwidth to 4", function()
    vim.bo.filetype = "dpg"
    eq(4, vim.bo.shiftwidth)
  end)

  it("sets expandtab", function()
    vim.bo.filetype = "dpg"
    assert.is_true(vim.bo.expandtab)
  end)

  it("does not re-source ftplugin twice (b:did_ftplugin guard)", function()
    vim.bo.filetype = "dpg"
    local first_sw = vim.bo.shiftwidth
    vim.bo.shiftwidth = 99
    -- Re-setting filetype should NOT reset shiftwidth if already loaded
    vim.cmd("runtime ftplugin/dpg.vim")
    -- b:did_ftplugin guard prevents re-sourcing
    eq(99, vim.bo.shiftwidth)
  end)
end)
