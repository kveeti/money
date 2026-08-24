import { expect, test } from "@playwright/test";

test("built frontend loads through the backend and serves client routes", async ({
  page,
}, testInfo) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Pick a dev user" }),
  ).toBeVisible();
  const user = `production-smoke-${testInfo.retry}-${Date.now()}`;
  await page.getByPlaceholder("new-user-sub").fill(user);
  await page.getByRole("button", { name: "Log in as new user" }).click();
  await expect(page).toHaveURL(/\/transactions$/);
  await expect(
    page.getByRole("heading", { name: "No transactions yet" }),
  ).toBeVisible();

  await page.goto("/stats");
  await expect(page.getByRole("button", { name: "month" })).toBeVisible();
});
