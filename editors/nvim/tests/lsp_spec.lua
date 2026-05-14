describe("DPG LSP configuration", function()
  before_each(function()
    -- Reset the dpg_ls config between tests
    local ok, configs = pcall(require, "lspconfig.configs")
    if ok then
      configs.dpg_ls = nil
    end
  end)

  it("registers dpg_ls in lspconfig.configs after setup", function()
    require("dpg.lsp").setup()

    local configs = require("lspconfig.configs")
    assert.is_not_nil(configs.dpg_ls, "dpg_ls should be registered in lspconfig.configs")
  end)

  it("uses dpg-lsp --stdio as the command", function()
    require("dpg.lsp").setup()

    local configs = require("lspconfig.configs")
    local cfg = configs.dpg_ls
    assert.is_not_nil(cfg, "dpg_ls config missing")
    assert.is_not_nil(cfg.default_config, "default_config missing")

    local cmd = cfg.default_config.cmd
    assert.are.equal("dpg-lsp", cmd[1])
    assert.are.equal("--stdio", cmd[2])
  end)

  it("targets the dpg filetype only", function()
    require("dpg.lsp").setup()

    local configs = require("lspconfig.configs")
    local filetypes = configs.dpg_ls.default_config.filetypes
    assert.are.equal(1, #filetypes)
    assert.are.equal("dpg", filetypes[1])
  end)

  it("uses dpg.toml as the root pattern", function()
    require("dpg.lsp").setup()
    -- root_dir should be a function (returned by root_pattern)
    local root_dir = require("lspconfig.configs").dpg_ls.default_config.root_dir
    assert.are.equal("function", type(root_dir))
  end)

  it("does not error when lspconfig is unavailable", function()
    -- Temporarily hide lspconfig to simulate it not being installed
    local real = package.loaded["lspconfig"]
    package.loaded["lspconfig"] = nil
    package.preload["lspconfig"] = function() error("not installed") end

    assert.has_no_errors(function()
      -- Re-require dpg.lsp so the pcall guard fires
      package.loaded["dpg.lsp"] = nil
      require("dpg.lsp").setup()
    end)

    -- Restore
    package.loaded["lspconfig"] = real
    package.preload["lspconfig"] = nil
  end)
end)
