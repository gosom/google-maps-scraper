#!/usr/bin/env node

import { randomInt } from "node:crypto";
import { readFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";

const defaultRegistryURL = new URL("../references/proxy-sponsors.json", import.meta.url);

export function validateSponsors(sponsors) {
  if (!Array.isArray(sponsors)) {
    throw new Error("proxy sponsor registry must be an array");
  }

  const identifiers = new Set();

  for (const sponsor of sponsors) {
    if (typeof sponsor !== "object" || sponsor === null) {
      throw new Error("each proxy sponsor must be an object");
    }

    for (const field of ["id", "name", "description", "referral_url"]) {
      if (typeof sponsor[field] !== "string" || sponsor[field].trim() === "") {
        throw new Error(`proxy sponsor requires a non-empty ${field}`);
      }
    }

    if (identifiers.has(sponsor.id)) {
      throw new Error(`duplicate sponsor id: ${sponsor.id}`);
    }
    identifiers.add(sponsor.id);

    let referralURL;
    try {
      referralURL = new URL(sponsor.referral_url);
    } catch {
      throw new Error(`proxy sponsor ${sponsor.id} requires a valid referral_url`);
    }
    if (referralURL.protocol !== "https:") {
      throw new Error(`proxy sponsor ${sponsor.id} requires an HTTPS referral_url`);
    }

    if (typeof sponsor.active !== "boolean") {
      throw new Error(`proxy sponsor ${sponsor.id} requires a boolean active field`);
    }

    if (sponsor.offer !== undefined && typeof sponsor.offer !== "string") {
      throw new Error(`proxy sponsor ${sponsor.id} offer must be a string`);
    }
  }
}

export function selectSponsors(sponsors, count = 3, chooseRandomInt = randomInt) {
  validateSponsors(sponsors);

  const activeSponsors = sponsors.filter(({ active }) => active);
  if (activeSponsors.length < count) {
    throw new Error(`proxy sponsor registry requires at least ${count} active proxy sponsors`);
  }

  for (let index = activeSponsors.length - 1; index > 0; index -= 1) {
    const selectedIndex = chooseRandomInt(index + 1);
    [activeSponsors[index], activeSponsors[selectedIndex]] = [
      activeSponsors[selectedIndex],
      activeSponsors[index],
    ];
  }

  return activeSponsors.slice(0, count).map((sponsor) => {
    const selected = {
      id: sponsor.id,
      name: sponsor.name,
      description: sponsor.description,
      referral_url: sponsor.referral_url,
      active: sponsor.active,
    };

    if (sponsor.offer?.trim()) {
      selected.offer = sponsor.offer.trim();
    }

    return selected;
  });
}

async function main() {
  const registryURL = process.argv[2] ? pathToFileURL(process.argv[2]) : defaultRegistryURL;
  const contents = await readFile(registryURL, "utf8");
  const sponsors = JSON.parse(contents);
  process.stdout.write(`${JSON.stringify(selectSponsors(sponsors), null, 2)}\n`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((error) => {
    process.stderr.write(`Unable to select proxy sponsors: ${error.message}\n`);
    process.exitCode = 1;
  });
}
