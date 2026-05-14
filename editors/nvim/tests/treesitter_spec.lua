describe("DPG tree-sitter configuration", function()
  it("registers the dpg parser config after setup", function()
    require("dpg.treesitter").setup()

    local parsers = require("nvim-treesitter.parsers")
    local cfg = parsers.get_parser_configs().dpg
    assert.is_not_nil(cfg, "dpg parser config should be registered")
  end)

  it("sets the filetype to dpg", function()
    require("dpg.treesitter").setup()

    local parsers = require("nvim-treesitter.parsers")
    local cfg = parsers.get_parser_configs().dpg
    assert.are.equal("dpg", cfg.filetype)
  end)

  it("points to the correct GitHub URL", function()
    require("dpg.treesitter").setup()

    local parsers = require("nvim-treesitter.parsers")
    local install_info = parsers.get_parser_configs().dpg.install_info
    assert.is_not_nil(install_info)
    assert.truthy(install_info.url:find("dullkingsman/tree-sitter-dpg"))
  end)

  it("includes both parser.c and scanner.c", function()
    require("dpg.treesitter").setup()

    local parsers = require("nvim-treesitter.parsers")
    local files = parsers.get_parser_configs().dpg.install_info.files
    assert.is_not_nil(files)

    local has_parser, has_scanner = false, false
    for _, f in ipairs(files) do
      if f:find("parser%.c") then has_parser = true end
      if f:find("scanner%.c") then has_scanner = true end
    end
    assert.is_true(has_parser, "install_info.files should include parser.c")
    assert.is_true(has_scanner, "install_info.files should include scanner.c")
  end)

  it("does not error when nvim-treesitter is unavailable", function()
    local real = package.loaded["nvim-treesitter.parsers"]
    package.loaded["nvim-treesitter.parsers"] = nil
    package.preload["nvim-treesitter.parsers"] = function() error("not installed") end

    assert.has_no_errors(function()
      package.loaded["dpg.treesitter"] = nil
      require("dpg.treesitter").setup()
    end)

    package.loaded["nvim-treesitter.parsers"] = real
    package.preload["nvim-treesitter.parsers"] = nil
  end)
end)
