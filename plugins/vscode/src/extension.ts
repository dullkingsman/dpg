import * as fs from "fs";
import { execFile } from "child_process";
import * as vscode from "vscode";
import { startClient, stopClient } from "./client";

export function activate(ctx: vscode.ExtensionContext): void {
  const cfg = vscode.workspace.getConfiguration("dpg");

  // ── LSP client ────────────────────────────────────────────────────────────
  if (cfg.get<boolean>("lsp.enabled", true)) {
    const lspPath = cfg.get<string>("lsp.path", "dpg-lsp");
    startClient(ctx, lspPath);
  }

  // ── Format on save ────────────────────────────────────────────────────────
  if (cfg.get<boolean>("fmt.onSave", true)) {
    const fmtPath = cfg.get<string>("fmt.path", "dpg");
    ctx.subscriptions.push(
      vscode.workspace.onWillSaveTextDocument((event) => {
        if (event.document.languageId !== "dpg") return;
        event.waitUntil(formatDocument(fmtPath, event.document));
      })
    );
  }
}

export function deactivate(): Thenable<void> | undefined {
  return stopClient();
}

// ── Helpers ───────────────────────────────────────────────────────────────────

/**
 * Runs `dpg fmt <file>` and returns a TextEdit that replaces the entire
 * document with the formatted result, or an empty array on failure.
 */
function formatDocument(
  fmtPath: string,
  doc: vscode.TextDocument
): Thenable<vscode.TextEdit[]> {
  const filePath = doc.uri.fsPath;

  return new Promise<vscode.TextEdit[]>((resolve) => {
    execFile(fmtPath, ["fmt", filePath], (err: Error | null) => {
      if (err) {
        resolve([]);
        return;
      }
      let formatted: string;
      try {
        formatted = fs.readFileSync(filePath, "utf8");
      } catch {
        resolve([]);
        return;
      }
      const original = doc.getText();
      if (formatted === original) {
        resolve([]);
        return;
      }
      const fullRange = new vscode.Range(
        doc.positionAt(0),
        doc.positionAt(original.length)
      );
      resolve([vscode.TextEdit.replace(fullRange, formatted)]);
    });
  });
}
