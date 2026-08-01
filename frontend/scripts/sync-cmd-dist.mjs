import { createHash } from "node:crypto";
import { chmod, copyFile, mkdir, readFile, readdir, rename, rm } from "node:fs/promises";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..", "..");
const source = resolve(root, "frontend", "dist");
const target = resolve(root, "cmd", "flowroutine", "frontend", "dist");
const temporaryTarget = `${target}.tmp`;
const checkOnly = process.argv.includes("--check");

async function listFiles(directory) {
  const files = [];
  const visit = async (current) => {
    const entries = await readdir(current, { withFileTypes: true });
    entries.sort((left, right) => left.name.localeCompare(right.name));
    for (const entry of entries) {
      const path = join(current, entry.name);
      if (entry.isDirectory()) {
        await visit(path);
      } else if (entry.isFile()) {
        files.push(relative(directory, path));
      } else {
        throw new Error(`Unsupported generated asset: ${path}`);
      }
    }
  };
  await visit(directory);
  return files;
}

async function manifest(directory, files) {
  const hash = createHash("sha256");
  for (const file of files) {
    hash.update(file);
    hash.update("\0");
    hash.update(await readFile(join(directory, file)));
    hash.update("\0");
  }
  return hash.digest("hex");
}

async function verifySync(sourceFiles) {
  const targetFiles = await listFiles(target);
  if (sourceFiles.join("\n") !== targetFiles.join("\n") ||
      await manifest(source, sourceFiles) !== await manifest(target, targetFiles)) {
    throw new Error("cmd/flowroutine frontend assets are not synchronized with frontend/dist");
  }
}

const sourceFiles = await listFiles(source);
if (!sourceFiles.includes("index.html")) {
  throw new Error("frontend/dist/index.html is missing; build the frontend before synchronizing assets");
}

if (checkOnly) {
  await verifySync(sourceFiles);
  process.exit(0);
}

await rm(temporaryTarget, { recursive: true, force: true });
await mkdir(temporaryTarget, { recursive: true });
for (const file of sourceFiles) {
  const destination = join(temporaryTarget, file);
  await mkdir(dirname(destination), { recursive: true });
  await copyFile(join(source, file), destination);
  await chmod(destination, 0o644);
}

await rm(target, { recursive: true, force: true });
await rename(temporaryTarget, target);
await verifySync(sourceFiles);
