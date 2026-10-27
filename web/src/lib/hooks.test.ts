import { expect, test } from "vitest";
import { isOrgId } from "./hooks";

test("isOrgId rejects static /orgs routes", () => {
  expect(isOrgId("new")).toBe(false);
  expect(isOrgId(undefined)).toBe(false);
  expect(isOrgId("")).toBe(false);
  expect(isOrgId("3f1c2b8e-1d2a-4c5b-9e7f-0a1b2c3d4e5f")).toBe(true);
});
