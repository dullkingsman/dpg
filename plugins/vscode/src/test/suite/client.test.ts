import * as assert from "assert";
import * as vscode from "vscode";

// The real startClient/stopClient rely on a live LanguageClient connection.
// We test the observable surface: the module exports exist and the client
// object returned carries the expected id and name without starting a real
// subprocess (the test environment has no dpg-lsp binary).

suite("DPG Client", () => {
  test("startClient and stopClient are exported from client module", async () => {
    // Dynamic import so the module is only resolved inside the extension host.
    // If the compile step succeeded the exports exist; that is what we verify.
    const clientModule = await import("../../client");
    assert.strictEqual(typeof clientModule.startClient, "function");
    assert.strictEqual(typeof clientModule.stopClient, "function");
  });

  test("stopClient returns undefined when no client has been started", async () => {
    const { stopClient } = await import("../../client");
    // Before any startClient call the module-level client is undefined.
    const result = stopClient();
    assert.strictEqual(result, undefined);
  });

  test("LanguageClient constructor receives dpg-lsp as client id", async () => {
    // Intercept LanguageClient construction to capture constructor arguments
    // without starting a real subprocess.
    const vslc = await import("vscode-languageclient/node");
    const original = vslc.LanguageClient;

    let capturedId: string | undefined;
    let capturedName: string | undefined;
    let capturedServerOptions: unknown;
    let capturedClientOptions: unknown;

    (vslc as any).LanguageClient = class {
      constructor(id: string, name: string, so: unknown, co: unknown) {
        capturedId = id;
        capturedName = name;
        capturedServerOptions = so;
        capturedClientOptions = co;
      }
      start() { return { dispose() {} }; }
      stop() { return Promise.resolve(); }
    };

    try {
      const { startClient } = await import("../../client");
      // Re-import to pick up the patched LanguageClient — clear module cache first.
      // (In the extension host Jest-style module caching does not apply; we
      //  call startClient directly with the module already loaded above.)
      const fakeCtx = {
        subscriptions: { push: () => {} },
      } as unknown as vscode.ExtensionContext;

      startClient(fakeCtx, "dpg-lsp");

      assert.strictEqual(capturedId, "dpg-lsp");
      assert.ok(
        typeof capturedName === "string" && capturedName.length > 0,
        "client name should be non-empty"
      );
      assert.ok(capturedServerOptions, "serverOptions should be passed");
      assert.ok(capturedClientOptions, "clientOptions should be passed");
    } finally {
      (vslc as any).LanguageClient = original;
    }
  });

  test("serverOptions uses --stdio arg", async () => {
    const vslc = await import("vscode-languageclient/node");
    const original = vslc.LanguageClient;

    let capturedServerOptions: any;
    (vslc as any).LanguageClient = class {
      constructor(_id: string, _name: string, so: any) {
        capturedServerOptions = so;
      }
      start() { return { dispose() {} }; }
      stop() { return Promise.resolve(); }
    };

    try {
      const { startClient } = await import("../../client");
      const fakeCtx = {
        subscriptions: { push: () => {} },
      } as unknown as vscode.ExtensionContext;

      startClient(fakeCtx, "/usr/local/bin/dpg-lsp");

      assert.deepStrictEqual(
        capturedServerOptions?.args,
        ["--stdio"],
        "server should be started with --stdio"
      );
      assert.strictEqual(
        capturedServerOptions?.command,
        "/usr/local/bin/dpg-lsp",
        "server command should be the lspPath passed in"
      );
    } finally {
      (vslc as any).LanguageClient = original;
    }
  });

  test("clientOptions selects dpg language documents", async () => {
    const vslc = await import("vscode-languageclient/node");
    const original = vslc.LanguageClient;

    let capturedClientOptions: any;
    (vslc as any).LanguageClient = class {
      constructor(_id: string, _name: string, _so: any, co: any) {
        capturedClientOptions = co;
      }
      start() { return { dispose() {} }; }
      stop() { return Promise.resolve(); }
    };

    try {
      const { startClient } = await import("../../client");
      const fakeCtx = {
        subscriptions: { push: () => {} },
      } as unknown as vscode.ExtensionContext;

      startClient(fakeCtx, "dpg-lsp");

      const selector = capturedClientOptions?.documentSelector;
      assert.ok(Array.isArray(selector), "documentSelector should be an array");
      const hasLang = selector.some(
        (s: any) => s.language === "dpg" && s.scheme === "file"
      );
      assert.ok(hasLang, "documentSelector should include { scheme: 'file', language: 'dpg' }");
    } finally {
      (vslc as any).LanguageClient = original;
    }
  });
});
