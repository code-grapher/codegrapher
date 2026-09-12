import { execFileSync } from "node:child_process";
import {
  mkdirSync,
  mkdtempSync,
  existsSync,
  readFileSync,
  readdirSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const root = resolve(fileURLToPath(new URL("..", import.meta.url)));
const packageDirectory = join(root, "clients", "typescript");
const destination = mkdtempSync(join(tmpdir(), "codegrapher-client-package-"));

try {
  const staleOutput = join(packageDirectory, "dist", "stale-client-output.js");
  mkdirSync(dirname(staleOutput), { recursive: true });
  writeFileSync(staleOutput, "throw new Error('stale generated output');\n");
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
  if (existsSync(staleOutput) || contents.includes("stale-client-output")) {
    throw new Error("generated client build did not remove stale compiler output");
  }
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
  const revision = execFileSync("git", ["rev-parse", "HEAD"], {
    cwd: root,
    encoding: "utf8",
  }).trim();
  const gitDependency = process.env.CODEGRAPHER_CLIENT_GIT_URL
    ?? `git+${pathToFileURL(root).href}#${revision}&path:/clients/typescript`;
  writeFileSync(
    join(consumer, "package.json"),
    '{"name":"generated-client-consumer","private":true,"type":"module"}\n',
  );
  writeFileSync(
    join(consumer, "pnpm-workspace.yaml"),
    `allowBuilds:\n  '${packageJson.name}@${gitDependency}': true\n`,
  );
  execFileSync("pnpm", ["add", "--prefer-offline", "--ignore-scripts", gitDependency], {
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
  writeFileSync(
    join(consumer, "consumer.ts"),
    `import { createV1ClientFromTransport, type ApiStatus } from '@code-grapher/browser-api-client';
import { type RepositorySummary } from '@code-grapher/browser-api-client/models';

declare const status: ApiStatus;
declare const repository: RepositorySummary;
const factory: typeof createV1ClientFromTransport = createV1ClientFromTransport;
const assertion: [string, string, typeof factory] = [status.apiVersion, repository.id, factory];
void assertion;
`,
  );
  writeFileSync(
    join(consumer, "tsconfig.json"),
    `${JSON.stringify(
      {
        compilerOptions: {
          lib: ["es2020", "dom"],
          module: "preserve",
          moduleResolution: "bundler",
          noEmit: true,
          skipLibCheck: true,
          strict: true,
          target: "es2022",
        },
        files: ["consumer.ts"],
      },
      null,
      2,
    )}\n`,
  );
  execFileSync(
    "pnpm",
    ["--dir", packageDirectory, "exec", "tsc", "-p", join(consumer, "tsconfig.json")],
    { stdio: "inherit" },
  );

  console.log(
    "generated client package contains and imports its runtime and type exports from Git",
  );
} finally {
  rmSync(destination, { recursive: true, force: true });
}
