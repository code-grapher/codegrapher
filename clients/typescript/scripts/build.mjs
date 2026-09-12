import { execFileSync } from "node:child_process";
import { rmSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const packageDirectory = resolve(dirname(fileURLToPath(import.meta.url)), "..");

rmSync(resolve(packageDirectory, "dist"), { recursive: true, force: true });
execFileSync("tsc", { cwd: packageDirectory, stdio: "inherit" });
