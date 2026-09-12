import { execFileSync } from "node:child_process";
import {
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const root = resolve(fileURLToPath(new URL("..", import.meta.url)));
const packageDirectory = join(root, "clients", "typescript");
const destination = mkdtempSync(join(tmpdir(), "codegrapher-client-package-"));

try {
  execFileSync(
    "pnpm",
    ["--dir", packageDirectory, "pack", "--pack-destination", destination],
    { stdio: "inherit" },
  );

  const archive = readdirSync(destination).find((name) => name.endsWith(".tgz"));
  if (!archive) {
    throw new Error("pnpm pack did not produce a package archive");
  }

  const contents = execFileSync("tar", ["-tzf", join(destination, archive)], {
    encoding: "utf8",
  });
  for (const expected of [
    "package/dist/index.js",
    "package/dist/index.d.ts",
    "package/dist/models/index.js",
    "package/dist/models/index.d.ts",
  ]) {
    if (!contents.split("\n").includes(expected)) {
      throw new Error(`generated client package is missing ${expected}`);
    }
  }

  const packageJson = JSON.parse(
    readFileSync(join(packageDirectory, "package.json"), "utf8"),
  );
  if (!packageJson.scripts?.prepare) {
    throw new Error("generated client package must build during Git installation");
  }

  const consumer = join(destination, "consumer");
  mkdirSync(consumer);
  writeFileSync(
    join(consumer, "package.json"),
    '{"name":"generated-client-consumer","private":true,"type":"module"}\n',
  );
  const revision = execFileSync("git", ["rev-parse", "HEAD"], {
    cwd: root,
    encoding: "utf8",
  }).trim();
  const gitDependency = `git+${pathToFileURL(root).href}#${revision}&path:/clients/typescript`;
  execFileSync("pnpm", ["add", "--prefer-offline", gitDependency], {
    cwd: consumer,
    stdio: "inherit",
  });
  execFileSync(
    process.execPath,
    [
      "--input-type=module",
      "--eval",
      "import('@code-grapher/browser-api-client').then((client) => { if (typeof client.createV1ClientFromTransport !== 'function') throw new Error('generated client root export is not importable') })",
    ],
    { cwd: consumer, stdio: "inherit" },
  );

  console.log(
    "generated client package contains and imports its runtime and type exports from Git",
  );
} finally {
  rmSync(destination, { recursive: true, force: true });
}
