import * as assert from "assert";
import * as vscode from "vscode";

suite("DPG Extension", () => {
  suiteSetup(async () => {
    // Ensure the extension is activated before any test runs.
    const ext = vscode.extensions.getExtension("dullkingsman.vscode-dpg");
    if (ext && !ext.isActive) {
      await ext.activate();
    }
  });

  test("dpg language is registered", async () => {
    const languages = await vscode.languages.getLanguages();
    assert.ok(
      languages.includes("dpg"),
      `Expected 'dpg' in registered languages, got: ${languages.join(", ")}`
    );
  });

  test("default config lsp.enabled is true", () => {
    const cfg = vscode.workspace.getConfiguration("dpg");
    assert.strictEqual(cfg.get<boolean>("lsp.enabled"), true);
  });

  test("default config lsp.path is dpg-lsp", () => {
    const cfg = vscode.workspace.getConfiguration("dpg");
    assert.strictEqual(cfg.get<string>("lsp.path"), "dpg-lsp");
  });

  test("default config fmt.onSave is true", () => {
    const cfg = vscode.workspace.getConfiguration("dpg");
    assert.strictEqual(cfg.get<boolean>("fmt.onSave"), true);
  });

  test("default config fmt.path is dpg", () => {
    const cfg = vscode.workspace.getConfiguration("dpg");
    assert.strictEqual(cfg.get<string>("fmt.path"), "dpg");
  });

  test(".dpg files open with dpg language mode", async () => {
    const content = "TABLE test (id bigint);";
    const doc = await vscode.workspace.openTextDocument({
      language: "dpg",
      content,
    });
    assert.strictEqual(doc.languageId, "dpg");
  });
});
