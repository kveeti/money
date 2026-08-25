import { expect, test } from "@playwright/test";

import { login } from "../helpers";

test("login, navigation, and command palette use the real app", async ({
  page,
}, testInfo) => {
  await login(page, testInfo, "e2e-bank-auth");

  const nav = page.getByRole("navigation");
  await expect(
    nav.getByRole("link", { name: "import", exact: true }),
  ).toHaveCount(0);
  await expect(
    nav.getByRole("link", { name: "banks", exact: true }),
  ).toHaveCount(0);
  await page.getByRole("button", { name: "Open menu" }).click();
  await page.getByRole("menuitem", { name: "import" }).click();
  await expect(page).toHaveURL(/\/imports$/);
  await page.getByRole("button", { name: "Open menu" }).click();
  await page.getByRole("menuitem", { name: "banks" }).click();
  await expect(page).toHaveURL(/\/connections$/);
  await page.getByRole("link", { name: "transactions", exact: true }).click();

  await page.getByRole("link", { name: "inbox", exact: true }).click();
  await expect(page).toHaveURL(/\/inbox$/);
  await expect(page.getByText("No transactions to categorize")).toBeVisible();

  await page.goBack();
  await expect(page).toHaveURL(/\/transactions$/);

  const mouseDownWasPrevented = await page
    .getByRole("link", { name: "inbox", exact: true })
    .evaluate(
      (link) =>
        !link.dispatchEvent(
          new MouseEvent("mousedown", {
            bubbles: true,
            cancelable: true,
            button: 0,
          }),
        ),
    );
  expect(mouseDownWasPrevented).toBe(true);
  await expect(page).toHaveURL(/\/inbox$/);

  await page
    .getByRole("link", { name: "transactions", exact: true })
    .dispatchEvent("touchstart");
  await expect(page).toHaveURL(/\/transactions$/);

  await page.keyboard.press("Meta+k");
  const search = page.getByRole("combobox", { name: "Search pages" });
  await expect(search).toBeVisible();
  await search.fill("stat");
  await page.getByRole("option", { name: "stats" }).click();

  await expect(page).toHaveURL(/\/stats$/);
  await expect(page.getByRole("button", { name: "month" })).toBeVisible();

  await page.getByRole("button", { name: "Open menu" }).click();
  await page.getByRole("menuitem", { name: "Sign out" }).click();
  await expect(page).toHaveURL((url) => url.pathname === "/login");
  await expect(
    page.getByRole("button", { name: "Sign in", exact: true }),
  ).toBeVisible();
});
