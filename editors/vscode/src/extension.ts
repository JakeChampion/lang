// VS Code extension entry point. Spawns the `lang-lsp` binary the
// user's PATH (or `lang.serverPath` setting) points at and wraps it
// as a language client using stdio transport — the same wire format
// `lang-lsp` already speaks for any generic LSP client.
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
      "lang: lang-lsp binary not found. Set `lang.serverPath` or add it to your PATH.",
    );
    return;
  }

  const serverOptions: ServerOptions = {
    run: { command: serverPath, transport: TransportKind.stdio },
    // Same binary for debug — `lang-lsp` has no dev-only flags.
    debug: { command: serverPath, transport: TransportKind.stdio },
  };

  const clientOptions: LanguageClientOptions = {
    documentSelector: [{ scheme: "file", language: "lang" }],
    synchronize: {
      // Re-load watched files on disk changes so external edits
      // (git checkout, formatter run from a script) flow through
      // to the open buffers.
      fileEvents: workspace.createFileSystemWatcher("**/*.lang"),
    },
  };

  client = new LanguageClient(
    "lang",
    "lang language server",
    serverOptions,
    clientOptions,
  );

  context.subscriptions.push(
    commands.registerCommand("lang.restartServer", async () => {
      if (!client) return;
      await client.stop();
      await client.start();
      window.showInformationMessage("lang: server restarted.");
    }),
  );

  await client.start();
}

export async function deactivate(): Promise<void> {
  if (client) {
    await client.stop();
  }
}

// resolveServerPath returns an absolute path to a lang-lsp binary the
// extension can spawn, or undefined when nothing was found. Settings
// take precedence; otherwise we look the unqualified `lang-lsp` up
// on PATH. The fs.existsSync check on the configured value lets us
// surface a clear error when the user typed a wrong path instead of
// failing with the spawn error VS Code would otherwise show.
function resolveServerPath(): string | undefined {
  const configured = workspace
    .getConfiguration("lang")
    .get<string>("serverPath", "lang-lsp");

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
