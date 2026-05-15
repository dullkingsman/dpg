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
// The real startClient/stopClient rely on a live LanguageClient connection.
// We test the observable surface: the module exports exist and the client
// object returned carries the expected id and name without starting a real
// subprocess (the test environment has no dpg-lsp binary).
suite("DPG Client", () => {
    test("startClient and stopClient are exported from client module", async () => {
        // Dynamic import so the module is only resolved inside the extension host.
        // If the compile step succeeded the exports exist; that is what we verify.
        const clientModule = await Promise.resolve().then(() => __importStar(require("../../client")));
        assert.strictEqual(typeof clientModule.startClient, "function");
        assert.strictEqual(typeof clientModule.stopClient, "function");
    });
    test("stopClient returns undefined when no client has been started", async () => {
        const { stopClient } = await Promise.resolve().then(() => __importStar(require("../../client")));
        // Before any startClient call the module-level client is undefined.
        const result = stopClient();
        assert.strictEqual(result, undefined);
    });
    test("LanguageClient constructor receives dpg-lsp as client id", async () => {
        // Intercept LanguageClient construction to capture constructor arguments
        // without starting a real subprocess.
        const vslc = await Promise.resolve().then(() => __importStar(require("vscode-languageclient/node")));
        const original = vslc.LanguageClient;
        let capturedId;
        let capturedName;
        let capturedServerOptions;
        let capturedClientOptions;
        vslc.LanguageClient = class {
            constructor(id, name, so, co) {
                capturedId = id;
                capturedName = name;
                capturedServerOptions = so;
                capturedClientOptions = co;
            }
            start() { return { dispose() { } }; }
            stop() { return Promise.resolve(); }
        };
        try {
            const { startClient } = await Promise.resolve().then(() => __importStar(require("../../client")));
            // Re-import to pick up the patched LanguageClient — clear module cache first.
            // (In the extension host Jest-style module caching does not apply; we
            //  call startClient directly with the module already loaded above.)
            const fakeCtx = {
                subscriptions: { push: () => { } },
            };
            startClient(fakeCtx, "dpg-lsp");
            assert.strictEqual(capturedId, "dpg-lsp");
            assert.ok(typeof capturedName === "string" && capturedName.length > 0, "client name should be non-empty");
            assert.ok(capturedServerOptions, "serverOptions should be passed");
            assert.ok(capturedClientOptions, "clientOptions should be passed");
        }
        finally {
            vslc.LanguageClient = original;
        }
    });
    test("serverOptions uses --stdio arg", async () => {
        const vslc = await Promise.resolve().then(() => __importStar(require("vscode-languageclient/node")));
        const original = vslc.LanguageClient;
        let capturedServerOptions;
        vslc.LanguageClient = class {
            constructor(_id, _name, so) {
                capturedServerOptions = so;
            }
            start() { return { dispose() { } }; }
            stop() { return Promise.resolve(); }
        };
        try {
            const { startClient } = await Promise.resolve().then(() => __importStar(require("../../client")));
            const fakeCtx = {
                subscriptions: { push: () => { } },
            };
            startClient(fakeCtx, "/usr/local/bin/dpg-lsp");
            assert.deepStrictEqual(capturedServerOptions?.args, ["--stdio"], "server should be started with --stdio");
            assert.strictEqual(capturedServerOptions?.command, "/usr/local/bin/dpg-lsp", "server command should be the lspPath passed in");
        }
        finally {
            vslc.LanguageClient = original;
        }
    });
    test("clientOptions selects dpg language documents", async () => {
        const vslc = await Promise.resolve().then(() => __importStar(require("vscode-languageclient/node")));
        const original = vslc.LanguageClient;
        let capturedClientOptions;
        vslc.LanguageClient = class {
            constructor(_id, _name, _so, co) {
                capturedClientOptions = co;
            }
            start() { return { dispose() { } }; }
            stop() { return Promise.resolve(); }
        };
        try {
            const { startClient } = await Promise.resolve().then(() => __importStar(require("../../client")));
            const fakeCtx = {
                subscriptions: { push: () => { } },
            };
            startClient(fakeCtx, "dpg-lsp");
            const selector = capturedClientOptions?.documentSelector;
            assert.ok(Array.isArray(selector), "documentSelector should be an array");
            const hasLang = selector.some((s) => s.language === "dpg" && s.scheme === "file");
            assert.ok(hasLang, "documentSelector should include { scheme: 'file', language: 'dpg' }");
        }
        finally {
            vslc.LanguageClient = original;
        }
    });
});
//# sourceMappingURL=client.test.js.map