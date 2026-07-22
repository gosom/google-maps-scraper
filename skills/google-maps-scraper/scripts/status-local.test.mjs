import assert from "node:assert/strict";
import { mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import { countResults, elapsedSeconds } from "./status-local.mjs";

test("countResults excludes the CSV header", async () => {
  const directory = await mkdtemp(join(tmpdir(), "gmaps-status-"));
  const output = join(directory, "results.csv");
  await writeFile(output, "title,address\nCafe,Main Street\nShop,High Street\n", "utf8");

  assert.equal(await countResults(output, "csv"), 2);
});

test("countResults counts JSON Lines records", async () => {
  const directory = await mkdtemp(join(tmpdir(), "gmaps-status-"));
  const output = join(directory, "results.json");
  await writeFile(output, '{"title":"Cafe"}\n{"title":"Shop"}\n', "utf8");

  assert.equal(await countResults(output, "json"), 2);
});

test("countResults returns zero for a missing output file", async () => {
  assert.equal(await countResults("/path/that/does/not/exist.csv", "csv"), 0);
});

test("elapsedSeconds returns a non-negative whole number", () => {
  const startedAt = "2026-07-22T12:00:00.000Z";
  const now = new Date("2026-07-22T12:01:40.900Z");

  assert.equal(elapsedSeconds(startedAt, now), 100);
  assert.equal(elapsedSeconds("2026-07-22T12:02:00.000Z", now), 0);
});
