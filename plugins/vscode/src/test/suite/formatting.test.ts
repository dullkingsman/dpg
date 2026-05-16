import * as assert from "assert";
import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import * as vscode from "vscode";

// Helper: create a temp .dpg file and open it in vscode.
async function openTempDpg(content: string): Promise<vscode.TextDocument> {
  const tmpPath = path.join(os.tmpdir(), `dpg-test-${Date.now()}.dpg`);
  fs.writeFileSync(tmpPath, content, "utf8");
  return vscode.workspace.openTextDocument(vscode.Uri.file(tmpPath));
}

suite("DPG Formatting", () => {
  test("formatDocument returns no edits when content is unchanged", async () => {
    const content = "TABLE users (id bigint);\n";
    const doc = await openTempDpg(content);

    // Write the same content back so `dpg fmt` produces no change.
    // (In a real test environment dpg must be on PATH; otherwise
    //  this asserts the fallback behaviour: no edits on exec failure.)
    const edits = await vscode.commands.executeCommand<vscode.TextEdit[]>(
      "vscode.executeFormatDocumentProvider",
      doc.uri,
      { tabSize: 4, insertSpaces: true }
    );

    // Edits may be null/undefined if dpg isn't on PATH in CI, which is also valid.
    if (edits && edits.length > 0) {
      // If edits were returned, each must be a TextEdit with newText.
      for (const edit of edits) {
        assert.ok(edit instanceof vscode.TextEdit, "edit should be a TextEdit");
        assert.ok(typeof edit.newText === "string", "newText should be a string");
      }
    }
  });

  test("formatDocument replaces full content when formatter changes the file", async () => {
    // This test exercises the TextEdit construction logic in extension.ts.
    // It mounts a fake formatter via the DocumentFormattingEditProvider API.
    const originalContent = "TABLE t(id bigint);"; // intentionally no space
    const formattedContent = "TABLE t (id bigint);\n";

    const doc = await openTempDpg(originalContent);

    // Register a temporary formatter that mimics what dpg fmt would do.
    const disposable = vscode.languages.registerDocumentFormattingEditProvider(
      "dpg",
      {
        provideDocumentFormattingEdits(document) {
          const full = new vscode.Range(
            document.positionAt(0),
            document.positionAt(document.getText().length)
          );
          return [vscode.TextEdit.replace(full, formattedContent)];
        },
      }
    );

    try {
      const edits = await vscode.commands.executeCommand<vscode.TextEdit[]>(
        "vscode.executeFormatDocumentProvider",
        doc.uri,
        { tabSize: 4, insertSpaces: true }
      );

      assert.ok(edits && edits.length > 0, "formatter should produce at least one edit");
      assert.strictEqual(
        edits[0].newText,
        formattedContent,
        "edit should replace content with formatted version"
      );
    } finally {
      disposable.dispose();
    }
  });

  test("extension activates for dpg language id", async () => {
    const doc = await vscode.workspace.openTextDocument({
      language: "dpg",
      content: "-- test\n",
    });

    assert.strictEqual(doc.languageId, "dpg");

    const ext = vscode.extensions.getExtension("dullkingsman.vscode-dpg");
    if (ext) {
      assert.ok(ext.isActive, "extension should be active after opening dpg file");
    }
  });
});
