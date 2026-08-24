import { expect, test } from "@playwright/test";

import { login } from "../helpers";

test("bank connections page exposes connect and re-auth entry points", async ({
  page,
}, testInfo) => {
  await login(page, testInfo, "e2e-bank-connections");
  await page.getByRole("button", { name: "Open menu" }).click();
  await page.getByRole("menuitem", { name: "banks" }).click();
  await expect(page).toHaveURL(/\/connections$/);
  await expect(
    page.getByRole("heading", { name: "Connect a bank" }),
  ).toBeVisible();
  await expect(page.getByLabel("Country")).toHaveValue("FI");
  await expect(page.getByLabel("Enable Banking bank name")).toBeVisible();
  await expect(page.getByText("No banks connected.")).toBeVisible();
});
