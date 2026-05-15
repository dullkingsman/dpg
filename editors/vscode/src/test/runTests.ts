import * as fs from "fs";
import * as path from "path";
import { runTests } from "@vscode/test-electron";

async function main(): Promise<void> {
  const extensionDevelopmentPath = path.resolve(__dirname, "../../");
  const extensionTestsPath = path.resolve(__dirname, "./suite/index");

  // Prefer a locally-installed VS Code to avoid network downloads in restricted
  // environments (CI without JetBrains CDN, dev boxes with firewall rules, etc.).
  const localCandidates = ["/usr/share/code/bin/code", "/usr/bin/code"];
  const vscodeExecutablePath =
    localCandidates.find((p) => fs.existsSync(p)) ?? undefined;

  await runTests({
    extensionDevelopmentPath,
    extensionTestsPath,
    vscodeExecutablePath,
    // Use a temp workspace so tests don't touch real files.
    launchArgs: ["--disable-extensions"],
  });
}

main().catch((err) => {
  console.error("Test run failed:", err);
  process.exit(1);
});
