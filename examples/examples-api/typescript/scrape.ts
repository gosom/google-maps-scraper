#!/usr/bin/env npx tsx
/**
 * Batch Maps scraper client.
 *
 * Submits keywords in parallel (up to 20 at a time), polls for results,
 * and saves each completed job to a JSON file.
 *
 * Usage:
 *   # Keywords as arguments
 *   npx tsx scrape.ts --base-url https://example.com --api-key gms_... "cafes in athens" "hotels in berlin"
 *
 *   # Keywords from stdin (one per line)
 *   cat keywords.txt | npx tsx scrape.ts --base-url https://example.com --api-key gms_...
 *
 *   # Custom output directory
 *   npx tsx scrape.ts --base-url https://example.com --api-key gms_... -o results "cafes in athens"
 *
 *   # Skip TLS certificate verification (e.g. self-signed certs)
 *   npx tsx scrape.ts --base-url https://example.com --api-key gms_... --insecure "cafes in athens"
 */

import { writeFile, mkdir } from "node:fs/promises";
import { createInterface } from "node:readline";
import { parseArgs } from "node:util";
import { join } from "node:path";

interface ScrapeResponse {
  job_id: string;
  status: string;
}

interface JobStatusResponse {
  job_id: string;
  status: string;
  keyword: string;
  results: unknown[] | null;
  result_count: number;
  error: string;
}

function safeFilename(s: string): string {
  return s
    .trim()
    .toLowerCase()
    .replace(/[^\w\s-]/g, "")
    .replace(/[\s]+/g, "_")
    .slice(0, 100);
}

async function apiRequest<T>(
  baseUrl: string,
  apiKey: string,
  method: string,
  path: string,
  body?: unknown
): Promise<T> {
  const url = baseUrl.replace(/\/+$/, "") + path;
  const resp = await fetch(url, {
    method,
    headers: {
      "X-API-Key": apiKey,
      "Content-Type": "application/json",
    },
    body: body ? JSON.stringify(body) : undefined,
  });

  if (!resp.ok) {
    const text = await resp.text();
    throw new Error(`HTTP ${resp.status}: ${text}`);
  }

  return resp.json() as Promise<T>;
}

function setInsecure(): void {
  // Disable TLS certificate verification for Node.js fetch (undici).
  // This covers self-signed or unknown-CA certificates.
  const { Agent, setGlobalDispatcher } = require("undici") as typeof import("undici");
  setGlobalDispatcher(new Agent({ connect: { rejectUnauthorized: false } }));
}

async function submitJob(
  baseUrl: string,
  apiKey: string,
  keyword: string,
  lang: string,
  maxDepth: number
): Promise<{ jobId: string; keyword: string }> {
  const resp = await apiRequest<ScrapeResponse>(
    baseUrl,
    apiKey,
    "POST",
    "/api/v1/scrape",
    { keyword, lang, max_depth: maxDepth }
  );
  return { jobId: resp.job_id, keyword };
}

async function pollJob(
  baseUrl: string,
  apiKey: string,
  jobId: string,
  keyword: string,
  outputDir: string,
  pollInterval = 5000
): Promise<void> {
  for (;;) {
    const resp = await apiRequest<JobStatusResponse>(
      baseUrl,
      apiKey,
      "GET",
      `/api/v1/jobs/${jobId}`
    );

    if (resp.status === "completed") {
      const fname = `${jobId}-${safeFilename(keyword)}.json`;
      const fpath = join(outputDir, fname);
      await writeFile(fpath, JSON.stringify(resp.results ?? [], null, 2));
      console.log(
        `  [done] "${keyword}" -> ${resp.result_count} results -> ${fname}`
      );
      return;
    }

    if (resp.status === "failed") {
      console.error(`  [fail] "${keyword}": ${resp.error || "unknown error"}`);
      return;
    }

    await new Promise((r) => setTimeout(r, pollInterval));
  }
}

async function processKeyword(
  baseUrl: string,
  apiKey: string,
  keyword: string,
  outputDir: string,
  lang: string,
  maxDepth: number
): Promise<void> {
  try {
    const job = await submitJob(baseUrl, apiKey, keyword, lang, maxDepth);
    console.log(`  [submitted] "${keyword}" -> job ${job.jobId}`);
    await pollJob(baseUrl, apiKey, job.jobId, keyword, outputDir);
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e);
    console.error(`  [error] "${keyword}": ${msg}`);
  }
}

async function readStdin(): Promise<string[]> {
  const lines: string[] = [];
  const rl = createInterface({ input: process.stdin });
  for await (const line of rl) {
    const trimmed = line.trim();
    if (trimmed) lines.push(trimmed);
  }
  return lines;
}

/** Run up to `limit` async tasks at a time. */
async function parallelLimit<T>(
  tasks: (() => Promise<T>)[],
  limit: number
): Promise<void> {
  const executing = new Set<Promise<void>>();

  for (const task of tasks) {
    const p = task().then(
      () => {
        executing.delete(p);
      },
      () => {
        executing.delete(p);
      }
    );
    executing.add(p);

    if (executing.size >= limit) {
      await Promise.race(executing);
    }
  }

  await Promise.all(executing);
}

async function main(): Promise<void> {
  const { values, positionals } = parseArgs({
    options: {
      "base-url": { type: "string" },
      "api-key": { type: "string" },
      output: { type: "string", short: "o", default: "map-outputs" },
      workers: { type: "string", short: "w", default: "20" },
      lang: { type: "string", default: "en" },
      "max-depth": { type: "string", default: "1" },
      insecure: { type: "boolean", short: "k", default: false },
    },
    allowPositionals: true,
    strict: true,
  });

  const baseUrl = values["base-url"];
  const apiKey = values["api-key"];
  const outputDir = values.output!;
  const workers = parseInt(values.workers!, 10);
  const lang = values.lang!;
  const maxDepth = parseInt(values["max-depth"]!, 10);

  if (values.insecure) {
    setInsecure();
  }

  if (!baseUrl || !apiKey) {
    console.error("Usage: scrape.ts --base-url URL --api-key KEY [keywords...]");
    process.exit(1);
  }

  let keywords = positionals;
  if (keywords.length === 0) {
    if (process.stdin.isTTY) {
      console.error("Provide keywords as arguments or pipe them via stdin");
      process.exit(1);
    }
    keywords = await readStdin();
  }

  if (keywords.length === 0) {
    console.error("No keywords provided");
    process.exit(1);
  }

  await mkdir(outputDir, { recursive: true });

  console.log(
    `Scraping ${keywords.length} keyword(s), max ${workers} parallel, output -> ${outputDir}/`
  );

  const tasks = keywords.map(
    (kw) => () => processKeyword(baseUrl, apiKey, kw, outputDir, lang, maxDepth)
  );

  await parallelLimit(tasks, workers);

  console.log("Done.");
}

main();
