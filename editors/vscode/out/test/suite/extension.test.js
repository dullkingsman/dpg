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
const assert = __importStar(require("assert"));
const vscode = __importStar(require("vscode"));
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
        assert.ok(languages.includes("dpg"), `Expected 'dpg' in registered languages, got: ${languages.join(", ")}`);
    });
    test("default config lsp.enabled is true", () => {
        const cfg = vscode.workspace.getConfiguration("dpg");
        assert.strictEqual(cfg.get("lsp.enabled"), true);
    });
    test("default config lsp.path is dpg-lsp", () => {
        const cfg = vscode.workspace.getConfiguration("dpg");
        assert.strictEqual(cfg.get("lsp.path"), "dpg-lsp");
    });
    test("default config fmt.onSave is true", () => {
        const cfg = vscode.workspace.getConfiguration("dpg");
        assert.strictEqual(cfg.get("fmt.onSave"), true);
    });
    test("default config fmt.path is dpg", () => {
        const cfg = vscode.workspace.getConfiguration("dpg");
        assert.strictEqual(cfg.get("fmt.path"), "dpg");
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
//# sourceMappingURL=extension.test.js.map