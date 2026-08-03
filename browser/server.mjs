// Builds and runs the real binary against a throwaway database, so browser
// tests exercise the shipped server rather than a test double.

import {execFileSync, spawn} from "node:child_process";
import {mkdtemp, rm} from "node:fs/promises";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import process from "node:process";

async function freePort() {
  return new Promise((resolve, reject) => {
    const probe = net.createServer();
    probe.on("error", reject);
    probe.listen(0, "127.0.0.1", () => {
      const {port} = probe.address();
      probe.close(() => resolve(port));
    });
  });
}

export async function startServer() {
  const root = path.resolve(import.meta.dirname, "..");
  const workDirectory = await mkdtemp(path.join(os.tmpdir(), "process-lab-browser-"));
  const executable = path.join(workDirectory, "processlab");
  execFileSync("go", ["build", "-o", executable, "./cmd/processlab"], {
    cwd: root,
    stdio: "pipe",
  });

  const port = await freePort();
  const url = `http://127.0.0.1:${port}`;
  const child = spawn(
    executable,
    ["-addr", `127.0.0.1:${port}`, "-db", path.join(workDirectory, "processlab.db")],
    {cwd: root, stdio: ["ignore", "pipe", "pipe"]},
  );
  let output = "";
  child.stdout.setEncoding("utf8");
  child.stderr.setEncoding("utf8");
  child.stdout.on("data", (chunk) => {
    output += chunk;
  });
  child.stderr.on("data", (chunk) => {
    output += chunk;
  });

  const deadline = Date.now() + 30_000;
  for (;;) {
    if (child.exitCode !== null) {
      throw new Error(`process-lab exited with ${child.exitCode}: ${output}`);
    }
    try {
      const response = await fetch(url, {signal: AbortSignal.timeout(1000)});
      if (response.ok) {
        break;
      }
    } catch {
      // not listening yet
    }
    if (Date.now() > deadline) {
      throw new Error(`process-lab did not become ready: ${output}`);
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }

  return {
    url,
    async stop() {
      if (child.exitCode === null) {
        child.kill("SIGTERM");
        await new Promise((resolve) => child.once("exit", resolve));
      }
      await rm(workDirectory, {recursive: true, force: true});
    },
  };
}

export const chromePath = process.env.CHROME_PATH ??
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
