import { expect, test } from "@playwright/test";

import { createBucket, importNordea, login, nordeaRow } from "../helpers";

test("categorizing closes the picker and allows categorizing another row", async ({
  page,
}, testInfo) => {
  await login(page, testInfo);
  const checking = await createBucket(page, "asset", "Checking");
  await createBucket(page, "expense", "Restaurants");
  await importNordea(
    page,
    checking.id,
    nordeaRow("2026/07/01", "-1,00", "First purchase") +
      nordeaRow("2026/07/01", "-2,00", "Second purchase"),
  );

  await page.goto("/inbox");
  const picker = page.locator('[aria-label^="Choose category"]');
  for (const name of ["First purchase", "Second purchase"]) {
    const row = page.getByRole("listitem").filter({ hasText: name });
    await row.getByRole("button").click();
    await expect(picker).toBeVisible();
    await page
      .getByRole("option", { name: "Restaurants", exact: true })
      .click();
    await expect(picker).not.toBeAttached();
    await expect(row).not.toBeVisible();
  }
  await expect(page.getByText("No transactions to categorize")).toBeVisible();
  await page.reload();
  await expect(page.getByText("No transactions to categorize")).toBeVisible();
});
