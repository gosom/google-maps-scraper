#!/usr/bin/env node

import { execFileSync } from "node:child_process";
import { readFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";

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

  const lineCount = contents.split(/\r?\n/).filter((line) => line.trim() !== "").length;
  return format === "csv" ? Math.max(0, lineCount - 1) : lineCount;
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
    if (argument === "--container") {
      options.container = args[++index] ?? "";
    } else if (argument === "--format") {
      options.format = args[++index] ?? "";
    } else if (argument === "--output") {
      options.output = args[++index] ?? "";
    } else {
      throw new Error(`unknown option: ${argument}`);
    }
  }

  if (!options.output) {
    throw new Error("--output is required");
  }

  return options;
}

async function main() {
  const options = parseArguments(process.argv.slice(2));
  const rawState = execFileSync(
    "docker",
    ["inspect", "--format", "{{json .State}}", options.container],
    { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] },
  );
  const state = JSON.parse(rawState);
  const status = {
    container: options.container,
    status: state.Status,
    elapsed_seconds: elapsedSeconds(state.StartedAt),
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
