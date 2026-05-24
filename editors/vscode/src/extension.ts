// VS Code extension entry point. Spawns the `fern-lsp` binary the
// user's PATH (or `fern.serverPath` setting) points at and wraps it
// as a language client using stdio transport — the same wire format
// `fern-lsp` already speaks for any generic LSP client.
//
// One file: keeping the extension this small means the maintenance
// surface is small too. If we ever need to ship binaries, build a
// proper publishable VSIX, or add commands, that's a separate
// refactor.

import * as path from "path";
import * as fs from "fs";
import { ExtensionContext, window, workspace, commands } from "vscode";
import {
  LanguageClient,
  LanguageClientOptions,
  ServerOptions,
  TransportKind,
} from "vscode-languageclient/node";

let client: LanguageClient | undefined;

export async function activate(context: ExtensionContext): Promise<void> {
  const serverPath = resolveServerPath();
  if (!serverPath) {
    window.showWarningMessage(
      "fern: fern-lsp binary not found. Set `fern.serverPath` or add it to your PATH.",
    );
    return;
  }

  const serverOptions: ServerOptions = {
    run: { command: serverPath, transport: TransportKind.stdio },
    // Same binary for debug — `fern-lsp` has no dev-only flags.
    debug: { command: serverPath, transport: TransportKind.stdio },
  };

  const clientOptions: LanguageClientOptions = {
    documentSelector: [{ scheme: "file", language: "fern" }],
    synchronize: {
      // Re-load watched files on disk changes so external edits
      // (git checkout, formatter run from a script) flow through
      // to the open buffers.
      fileEvents: workspace.createFileSystemWatcher("**/*.fern"),
    },
  };

  client = new LanguageClient(
    "fern",
    "Fern language server",
    serverOptions,
    clientOptions,
  );

  context.subscriptions.push(
    commands.registerCommand("fern.restartServer", async () => {
      if (!client) return;
      await client.stop();
      await client.start();
      window.showInformationMessage("fern: server restarted.");
    }),
  );

  await client.start();
}

export async function deactivate(): Promise<void> {
  if (client) {
    await client.stop();
  }
}

// resolveServerPath returns an absolute path to a fern-lsp binary the
// extension can spawn, or undefined when nothing was found. Settings
// take precedence; otherwise we look the unqualified `fern-lsp` up
// on PATH. The fs.existsSync check on the configured value lets us
// surface a clear error when the user typed a wrong path instead of
// failing with the spawn error VS Code would otherwise show.
function resolveServerPath(): string | undefined {
  const configured = workspace
    .getConfiguration("fern")
    .get<string>("serverPath", "fern-lsp");

  if (path.isAbsolute(configured)) {
    return fs.existsSync(configured) ? configured : undefined;
  }
  // Search PATH for the unqualified name. The shell does this for
  // us when spawn() runs, but we resolve eagerly so we can show a
  // friendlier error.
  const pathEnv = process.env.PATH ?? "";
  const sep = process.platform === "win32" ? ";" : ":";
  const exts = process.platform === "win32" ? [".exe", ".cmd", ""] : [""];
  for (const dir of pathEnv.split(sep)) {
    if (!dir) continue;
    for (const ext of exts) {
      const candidate = path.join(dir, configured + ext);
      if (fs.existsSync(candidate)) return candidate;
    }
  }
  return undefined;
}
