"use strict";
var __createBinding = (this && this.__createBinding) || (Object.create ? (function(o, m, k, k2) {
    if (k2 === undefined) k2 = k;
    var desc = Object.getOwnPropertyDescriptor(m, k);
    if (!desc || ("get" in desc ? !m.__esModule : desc.writable || desc.configurable)) {
      desc = { enumerable: true, get: function() { return m[k]; } };
    }
    Object.defineProperty(o, k2, desc);
}) : (function(o, m, k, k2) {
    if (k2 === undefined) k2 = k;
    o[k2] = m[k];
}));
var __setModuleDefault = (this && this.__setModuleDefault) || (Object.create ? (function(o, v) {
    Object.defineProperty(o, "default", { enumerable: true, value: v });
}) : function(o, v) {
    o["default"] = v;
});
var __importStar = (this && this.__importStar) || (function () {
    var ownKeys = function(o) {
        ownKeys = Object.getOwnPropertyNames || function (o) {
            var ar = [];
            for (var k in o) if (Object.prototype.hasOwnProperty.call(o, k)) ar[ar.length] = k;
            return ar;
        };
        return ownKeys(o);
    };
    return function (mod) {
        if (mod && mod.__esModule) return mod;
        var result = {};
        if (mod != null) for (var k = ownKeys(mod), i = 0; i < k.length; i++) if (k[i] !== "default") __createBinding(result, mod, k[i]);
        __setModuleDefault(result, mod);
        return result;
    };
})();
Object.defineProperty(exports, "__esModule", { value: true });
exports.activate = activate;
exports.deactivate = deactivate;
const fs = __importStar(require("fs"));
const child_process_1 = require("child_process");
const vscode = __importStar(require("vscode"));
const client_1 = require("./client");
function activate(ctx) {
    const cfg = vscode.workspace.getConfiguration("dpg");
    // ── LSP client ────────────────────────────────────────────────────────────
    if (cfg.get("lsp.enabled", true)) {
        const lspPath = cfg.get("lsp.path", "dpg-lsp");
        (0, client_1.startClient)(ctx, lspPath);
    }
    // ── Format on save ────────────────────────────────────────────────────────
    if (cfg.get("fmt.onSave", true)) {
        const fmtPath = cfg.get("fmt.path", "dpg");
        ctx.subscriptions.push(vscode.workspace.onWillSaveTextDocument((event) => {
            if (event.document.languageId !== "dpg")
                return;
            event.waitUntil(formatDocument(fmtPath, event.document));
        }));
    }
}
function deactivate() {
    return (0, client_1.stopClient)();
}
// ── Helpers ───────────────────────────────────────────────────────────────────
/**
 * Runs `dpg fmt <file>` and returns a TextEdit that replaces the entire
 * document with the formatted result, or an empty array on failure.
 */
function formatDocument(fmtPath, doc) {
    const filePath = doc.uri.fsPath;
    return new Promise((resolve) => {
        (0, child_process_1.execFile)(fmtPath, ["fmt", filePath], (err) => {
            if (err) {
                resolve([]);
                return;
            }
            let formatted;
            try {
                formatted = fs.readFileSync(filePath, "utf8");
            }
            catch {
                resolve([]);
                return;
            }
            const original = doc.getText();
            if (formatted === original) {
                resolve([]);
                return;
            }
            const fullRange = new vscode.Range(doc.positionAt(0), doc.positionAt(original.length));
            resolve([vscode.TextEdit.replace(fullRange, formatted)]);
        });
    });
}
//# sourceMappingURL=extension.js.map