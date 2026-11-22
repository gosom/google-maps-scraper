#!/usr/bin/env node
import { execFileSync, spawn } from "node:child_process";
import { copyFile, mkdir, open, readFile, rename, rm, writeFile } from "node:fs/promises";
import { homedir } from "node:os";
import { join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { randomUUID } from "node:crypto";
import { processIsAlive, reconcileState } from "./run-state.mjs";

const filename = fileURLToPath(import.meta.url);
export async function saveState(directory, state) {
  await writeFile(join(directory, "run.json.tmp"), JSON.stringify(state, null, 2) + "\n", { mode: 0o600 });
  await rename(join(directory, "run.json.tmp"), join(directory, "run.json"));
}

export function parseOptions(args) {
  const options = { format: "csv", lang: "en", depth: "1", concurrency: "1", backend: "auto" };
  const values = new Set(["queries", "output-dir", "format", "lang", "depth", "concurrency", "backend", "executable", "proxy-file", "grid-bbox", "grid-cell"]);
  const switches = new Set(["email", "extra-reviews", "resume", "recover-interrupted", "skip-image-pull"]);
  for (let i = 0; i < args.length; i++) {
    const key = args[i].replace(/^--/, "");
    if (!args[i].startsWith("--")) throw new Error(`Unknown argument: ${args[i]}`);
    if (switches.has(key)) options[key] = true;
    else if (values.has(key) && args[i + 1] && !args[i + 1].startsWith("--")) options[key] = args[++i];
    else throw new Error(`Unknown option or missing value: ${args[i]}`);
  }
  if (!options.queries || !options["output-dir"]) throw new Error("--queries FILE and --output-dir DIR are required");
  if (options["recover-interrupted"] && !options.resume) throw new Error("--recover-interrupted requires --resume");
  if (!["native", "docker", "auto"].includes(options.backend)) throw new Error("--backend must be native, docker, or auto");
  if (!["csv", "json"].includes(options.format)) throw new Error("--format must be csv or json");
  if (options["extra-reviews"] && options.format !== "json") throw new Error("Extra reviews require --format json");
  for (const key of ["depth", "concurrency"]) if (!/^[1-9]\d*$/.test(options[key])) throw new Error(`--${key} must be positive`);
  if (options["grid-bbox"]) {
    const box = options["grid-bbox"].split(",").map(Number);
    if (box.length !== 4 || !box.every(Number.isFinite) || box[0] >= box[2] || box[1] >= box[3] || box[0] < -90 || box[2] > 90 || box[1] < -180 || box[3] > 180) throw new Error("Invalid --grid-bbox");
  }
  if (options["grid-cell"] && (!options["grid-bbox"] || !(Number(options["grid-cell"]) > 0) || !Number.isFinite(Number(options["grid-cell"])))) throw new Error("--grid-cell requires a bounding box and positive finite size");
  return options;
}

async function worker(directory) {
  const state = JSON.parse(await readFile(join(directory, "run.json"), "utf8"));
  const log = await open(join(directory, "crawl.log"), "a", 0o600);
  const child = spawn(state.command, state.args, { cwd: directory, stdio: ["ignore", log.fd, log.fd] });
  const completion = new Promise((accept) => child.once("close", (code, signal) => accept({ code, signal })));
  let spawnError;
  try {
    await new Promise((accept, reject) => { child.once("spawn", accept); child.once("error", reject); });
    state.status = "running";
    state.pid = child.pid;
    state.worker_pid = process.pid;
    await saveState(directory, state);
    process.send?.({ ready: true });
    process.disconnect?.();
  } catch {
    spawnError = "Unable to launch the configured executable";
  }
  const { code, signal } = await completion;
  state.status = "exited";
  state.exit_code = code ?? 1;
  state.signal = signal;
  state.finished_at = new Date().toISOString();
  if (spawnError) state.error = spawnError;
  await saveState(directory, state);
  await log.close();
  await rm(join(directory, ".agent-lock"), { recursive: true, force: true });
  if (process.connected) { process.send?.({ error: spawnError || "Crawl ended before startup" }); process.disconnect(); }
}

async function main() {
  if (process.argv[2] === "--worker") return worker(process.argv[3]);
  const options = parseOptions(process.argv.slice(2));
  const directory = resolve(options["output-dir"]);
  const queries = resolve(options.queries);
  if (!(await readFile(queries, "utf8")).trim()) throw new Error("Query file is empty");
  let executable = options.executable ? resolve(options.executable) : "";
  if (!executable && options.backend !== "docker") {
    try { executable = (await readFile(join(process.env.XDG_CONFIG_HOME || join(homedir(), ".config"), "google-maps-scraper", "native-executable"), "utf8")).trim(); }
    catch (error) { if (error.code !== "ENOENT") throw error; }
  }
  const backend = options.backend === "auto" ? (executable ? "native" : "docker") : options.backend;
  if (backend === "native" && !executable) throw new Error("Install the native binary first or supply --executable PATH");
  let image;
  if (backend === "docker") {
    execFileSync("docker", ["info"], { stdio: "ignore" });
    if (!options["skip-image-pull"]) execFileSync("docker", ["pull", "gosom/google-maps-scraper"], { stdio: ["ignore", 2, 2] });
    image = execFileSync("docker", ["image", "inspect", "--format", "{{.Id}}", "gosom/google-maps-scraper"], { encoding: "utf8" }).trim();
  }
  if (options.resume) {
    const previous = JSON.parse(await readFile(join(directory, "run.json"), "utf8"));
    const current = reconcileState(previous);
    if (!["exited", "dead", "interrupted"].includes(current.status)) {
      throw new Error("The previous crawl has not stopped; inspect or stop it before resuming");
    }
    if (previous.status !== "exited") {
      if (processIsAlive(previous.worker_pid) || processIsAlive(previous.pid)) throw new Error("The previous supervisor or crawl process has not stopped");
      if (!options["recover-interrupted"]) throw new Error("Confirm the interrupted crawl has stopped, remove its stale .agent-lock directory, then use --resume --recover-interrupted");
    }
    const { resume: ignored, "recover-interrupted": ignoredRecovery, "skip-image-pull": ignoredPull, ...settings } = options;
    const { resume: oldResume, "recover-interrupted": oldRecovery, "skip-image-pull": oldPull, ...oldSettings } = previous.options;
    if (JSON.stringify(Object.entries(settings).sort()) !== JSON.stringify(Object.entries(oldSettings).sort()) || previous.backend !== backend || previous.command !== (backend === "native" ? executable : "docker")) throw new Error("Resume must use the original options and backend");
    if (!(await readFile(queries)).equals(await readFile(join(directory, "queries.txt")))) throw new Error("Resume queries differ from the original run");
  } else {
    // Exclusive creation prevents accidental overwrite, including concurrent starts.
    await mkdir(directory, { recursive: false, mode: 0o700 });
    await copyFile(queries, join(directory, "queries.txt"));
  }
  await mkdir(join(directory, ".agent-lock"));
  try {
    const output = join(directory, `results.${options.format}`);
    if (backend === "docker" && !options.resume) {
      // The scraper opens existing output in place. Pre-create it as the host user
      // so private (0600) results remain readable even with Docker UID remapping.
      const results = await open(output, "wx", 0o600);
      await results.close();
    }
    const args = ["-input", backend === "native" ? join(directory, "queries.txt") : "/out/queries.txt", "-results", backend === "native" ? output : `/out/results.${options.format}`, "-depth", options.depth, "-lang", options.lang, "-c", options.concurrency, "-exit-on-inactivity", "3m", "-resume"];
    if (options.format === "json") args.push("-json");
    for (const flag of ["email", "extra-reviews"]) if (options[flag]) args.push(`-${flag}`);
    for (const flag of ["grid-bbox", "grid-cell"]) if (options[flag]) args.push(`-${flag}`, options[flag]);
    const proxy = options["proxy-file"] ? resolve(options["proxy-file"]) : null;
    if (proxy) args.push("-proxies-file", backend === "native" ? proxy : "/run/secrets/gmaps-proxies");
    const container = `gmaps-agent-${randomUUID()}`;
    // The image's default user must be able to update bundled browser metadata.
    const dockerArgs = ["run", "--name", container, "-v", `${directory}:/out`];
    if (proxy) dockerArgs.push("-v", `${proxy}:/run/secrets/gmaps-proxies:ro`);
    const state = { backend, status: "starting", started_at: new Date().toISOString(), output, format: options.format, options, command: backend === "native" ? executable : "docker", args: backend === "native" ? args : [...dockerArgs, image, ...args], ...(backend === "docker" ? { container } : {}) };
    await saveState(directory, state);
    const child = spawn(process.execPath, [filename, "--worker", directory], { detached: true, stdio: ["ignore", "ignore", "ignore", "ipc"] });
    await new Promise((accept, reject) => {
      child.once("message", (message) => message.ready ? accept() : reject(new Error(message.error)));
      child.once("error", reject);
      child.once("exit", () => reject(new Error("Crawl worker exited before startup")));
    });
    child.unref();
    console.log(JSON.stringify({ backend, run_dir: directory, output, status: "started" }));
  } catch (error) {
    await rm(join(directory, ".agent-lock"), { recursive: true, force: true });
    throw error;
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((error) => { console.error(error.message); process.exitCode = 1; });
}
