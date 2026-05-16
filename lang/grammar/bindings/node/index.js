const { createRequire } = require("module");
const { resolve } = require("path");

let binding;
try {
  binding = require("./build/Release/tree_sitter_dpg_binding");
} catch (_) {
  try {
    binding = require("./build/Debug/tree_sitter_dpg_binding");
  } catch (_) {
    binding = null;
  }
}

module.exports = binding
  ? { language: binding.language() }
  : null;
