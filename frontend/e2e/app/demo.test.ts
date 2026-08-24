import { expect, test } from "@playwright/test";

test("demo login opens a seeded account and offers a sample import", async ({
  page,
}) => {
  await page.goto("/");
  await expect(page).toHaveURL(/\/login\?demo=1$/);
  await expect(
    page.getByRole("heading", { name: "Track your money" }),
  ).toBeVisible();
  await expect(page.getByText("deleted after 30 minutes")).toBeVisible();

  await page.getByRole("button", { name: "Try the demo" }).click();
  await expect(page).toHaveURL(/\/transactions$/);
  await expect(page.getByText("S-Market").first()).toBeVisible();

  await page.getByRole("button", { name: "Open menu" }).click();
  await expect(page.getByRole("menuitem", { name: "banks" })).toHaveCount(0);
  await page.getByRole("menuitem", { name: "import" }).click();

  await expect(
    page.getByRole("button", { name: "Use sample CSV" }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Download sample CSV" }),
  ).toBeVisible();

  await page.getByRole("button", { name: "Use sample CSV" }).click();
  await expect(page.getByText("dash-demo.csv", { exact: true })).toBeVisible();
  await page
    .getByRole("combobox")
    .filter({ hasText: "Select account" })
    .click();
  await page.getByRole("combobox", { name: "Filter buckets" }).fill("Checking");
  await page.getByRole("option", { name: "Checking", exact: true }).click();

  const imported = page.waitForResponse(
    (response) =>
      response.url().endsWith("/api/v1/imports") &&
      response.request().method() === "POST",
  );
  await page.getByRole("button", { name: "Import", exact: true }).click();
  expect((await imported).ok()).toBe(true);
});
