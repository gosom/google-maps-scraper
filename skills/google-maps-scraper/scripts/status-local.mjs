#!/usr/bin/env node

import { execFileSync } from "node:child_process";
import { readFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import { join, resolve } from "node:path";
import { reconcileState } from "./run-state.mjs";

export function countCSV(contents) {
  let quoted = false;
  let fieldStart = true;
  let record = false;
  let records = 0;
  for (let index = 0; index < contents.length; index++) {
    const char = contents[index];
    if (quoted) {
      if (char === '"') {
        if (contents[index + 1] === '"') index++;
        else quoted = false;
      }
    } else if (char === '"' && fieldStart) {
      quoted = true;
      record = true;
      fieldStart = false;
    } else if (char === "\n") {
      if (record) records++;
      record = false;
      fieldStart = true;
    } else if (char === ",") {
      record = true;
      fieldStart = true;
    } else if (char !== "\r") {
      record = true;
      fieldStart = false;
    }
  }
  // A live writer may still be filling the final record. Count only terminated rows.
  return Math.max(0, records - 1);
}

export async function countResults(outputPath, format) {
  if (format !== "csv" && format !== "json") {
    throw new Error("format must be csv or json");
  }

  let contents;
  try {
    contents = await readFile(outputPath, "utf8");
  } catch (error) {
    if (error.code === "ENOENT") {
      return 0;
    }
    throw error;
  }

  if (format === "csv") return countCSV(contents);
  const lines = contents.split(/\r?\n/);
  lines.pop(); // The scraper terminates complete records with a newline.
  return lines.filter((line) => {
    if (!line.trim()) return false;
    const entry = JSON.parse(line);
    return entry !== null && typeof entry === "object" && !Array.isArray(entry);
  }).length;
}

export function elapsedSeconds(startedAt, now = new Date()) {
  const startMillis = Date.parse(startedAt);
  if (!Number.isFinite(startMillis)) {
    return 0;
  }

  return Math.max(0, Math.floor((now.getTime() - startMillis) / 1000));
}

function parseArguments(args) {
  const options = {
    container: "gmaps-scraper-agent",
    format: "csv",
    output: "",
  };

  for (let index = 0; index < args.length; index += 1) {
    const argument = args[index];
    if (argument === "--run-dir") {
      options.run_dir = args[++index] ?? "";
    } else if (argument === "--container") {
      options.container = args[++index] ?? "";
    } else if (argument === "--format") {
      options.format = args[++index] ?? "";
    } else if (argument === "--output") {
      options.output = args[++index] ?? "";
    } else {
      throw new Error(`unknown option: ${argument}`);
    }
  }

  if (!options.output && !options.run_dir) {
    throw new Error("--run-dir or --output is required");
  }

  return options;
}

async function main() {
  const options = parseArguments(process.argv.slice(2));
  if (options.run_dir) {
    const state = reconcileState(JSON.parse(await readFile(join(resolve(options.run_dir), "run.json"), "utf8")));
    const { status, exit_code: exitCode, finished_at: finishedAt } = state;
    console.log(JSON.stringify({
      backend: state.backend, status,
      elapsed_seconds: elapsedSeconds(state.started_at, finishedAt ? new Date(finishedAt) : new Date()),
      result_count: await countResults(state.output, state.format), output: state.output,
      ...(status === "exited" ? { exit_code: exitCode } : {}),
      ...(state.error ? { error: state.error } : {}),
    }, null, 2));
    return;
  }
  const rawState = execFileSync(
    "docker",
    ["inspect", "--format", "{{json .State}}", options.container],
    { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] },
  );
  const state = JSON.parse(rawState);
  const status = {
    container: options.container,
    status: state.Status,
    elapsed_seconds: elapsedSeconds(state.StartedAt, state.Status === "exited" ? new Date(state.FinishedAt) : new Date()),
    result_count: await countResults(options.output, options.format),
    output: options.output,
  };

  if (state.Status === "exited") {
    status.exit_code = state.ExitCode;
  }
  if (state.Error) {
    status.error = state.Error;
  }

  process.stdout.write(`${JSON.stringify(status, null, 2)}\n`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((error) => {
    process.stderr.write(`Unable to inspect local crawl: ${error.message}\n`);
    process.exitCode = 1;
  });
}
