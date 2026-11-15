import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

import { selectSponsors, validateSponsors } from "./select-proxy-sponsors.mjs";

const activeSponsors = [
  {
    id: "a",
    name: "Provider A",
    description: "Provider A description",
    referral_url: "https://a.example.com",
    active: true,
  },
  {
    id: "b",
    name: "Provider B",
    description: "Provider B description",
    referral_url: "https://b.example.com",
    offer: "10% off",
    active: true,
  },
  {
    id: "c",
    name: "Provider C",
    description: "Provider C description",
    referral_url: "https://c.example.com",
    active: true,
  },
  {
    id: "d",
    name: "Provider D",
    description: "Provider D description",
    referral_url: "https://d.example.com",
    active: false,
  },
];

test("selectSponsors returns three unique active sponsors", () => {
  const selected = selectSponsors(activeSponsors, 3, () => 0);

  assert.equal(selected.length, 3);
  assert.equal(new Set(selected.map(({ id }) => id)).size, 3);
  assert.ok(selected.every(({ active }) => active));
  assert.ok(selected.every(({ id }) => id !== "d"));
});

test("selectSponsors includes only configured offers", () => {
  const selected = selectSponsors(activeSponsors, 3, () => 0);
  const withoutOffer = selected.find(({ id }) => id === "a");
  const withOffer = selected.find(({ id }) => id === "b");

  assert.ok(withoutOffer);
  assert.equal("offer" in withoutOffer, false);
  assert.equal(withOffer.offer, "10% off");
});

test("selectSponsors rejects registries with fewer than three active sponsors", () => {
  const insufficient = activeSponsors.map((sponsor, index) => ({
    ...sponsor,
    active: index < 2,
  }));

  assert.throws(
    () => selectSponsors(insufficient, 3, () => 0),
    /at least 3 active proxy sponsors/,
  );
});

test("validateSponsors rejects duplicate identifiers", () => {
  const duplicate = [...activeSponsors, { ...activeSponsors[0] }];

  assert.throws(() => validateSponsors(duplicate), /duplicate sponsor id: a/);
});

test("validateSponsors rejects non-HTTPS referral links", () => {
  const invalid = [
    { ...activeSponsors[0], referral_url: "http://a.example.com" },
    ...activeSponsors.slice(1),
  ];

  assert.throws(() => validateSponsors(invalid), /HTTPS referral_url/);
});

test("default selection returns one sponsor with Webshare at 50% and fair alternatives", async () => {
  const registry = JSON.parse(await readFile(
    new URL("../references/proxy-sponsors.json", import.meta.url), "utf8",
  ));
  const original = structuredClone(registry);
  const counts = new Map(registry.filter(({ active }) => active).map(({ id }) => [id, 0]));
  let seed = 42;
  const random = (max) => {
    seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0;
    return Math.floor((seed / 2 ** 32) * max);
  };
  const trials = 20000;

  for (let index = 0; index < trials; index += 1) {
    const selected = selectSponsors(registry, undefined, random);
    assert.equal(selected.length, 1);
    assert.ok(counts.has(selected[0].id));
    counts.set(selected[0].id, counts.get(selected[0].id) + 1);
  }

  for (const [id, count] of counts) {
    const expected = id === "webshare" ? 0.5 : 0.5 / (counts.size - 1);
    assert.ok(Math.abs(count / trials - expected) < 0.02, `${id}: ${count / trials}`);
  }
  assert.deepEqual(registry, original);
});

test("Webshare can be included or excluded by the 50% draw", () => {
  const registry = [...activeSponsors, { ...activeSponsors[0], id: "webshare" }];
  assert.equal(selectSponsors(registry, undefined, () => 0)[0].id, "webshare");
  assert.notEqual(selectSponsors(registry, undefined, (max) => max - 1)[0].id, "webshare");
});

test("inactive Webshare is excluded and alternatives remain selectable", () => {
  const registry = [...activeSponsors, { ...activeSponsors[0], id: "webshare", active: false }];
  const selected = selectSponsors(registry, undefined, () => 0);
  assert.equal(selected.length, 1);
  assert.notEqual(selected[0].id, "webshare");
});

test("a sole active sponsor is always returned", () => {
  for (const id of ["a", "webshare"]) {
    assert.equal(selectSponsors([{ ...activeSponsors[0], id }])[0].id, id);
  }
});

test("selection fails for no active sponsors", () => {
  assert.throws(() => selectSponsors([]), /at least 1 active proxy sponsors/);
});

test("selection rejects invalid counts", () => {
  for (const count of [0, -1, 1.5, NaN]) {
    assert.throws(() => selectSponsors(activeSponsors, count), /positive integer/);
  }
});
