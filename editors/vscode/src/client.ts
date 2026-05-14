import * as path from "path";
import * as vscode from "vscode";
import {
  LanguageClient,
  LanguageClientOptions,
  ServerOptions,
  TransportKind,
} from "vscode-languageclient/node";

let client: LanguageClient | undefined;

export function startClient(
  ctx: vscode.ExtensionContext,
  lspPath: string
): LanguageClient {
  const serverOptions: ServerOptions = {
    command: lspPath,
    args: ["--stdio"],
    transport: TransportKind.stdio,
  };

  const clientOptions: LanguageClientOptions = {
    documentSelector: [{ scheme: "file", language: "dpg" }],
    synchronize: {
      fileEvents: vscode.workspace.createFileSystemWatcher("**/*.dpg"),
    },
    outputChannelName: "DPG Language Server",
  };

  client = new LanguageClient(
    "dpg-lsp",
    "DPG Language Server",
    serverOptions,
    clientOptions
  );

  ctx.subscriptions.push(client.start());
  return client;
}

export function stopClient(): Thenable<void> | undefined {
  return client?.stop();
}
