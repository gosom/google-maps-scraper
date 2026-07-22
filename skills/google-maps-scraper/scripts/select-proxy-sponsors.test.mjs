import assert from "node:assert/strict";
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
