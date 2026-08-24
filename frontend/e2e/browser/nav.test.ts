import { expect, test } from "@playwright/test";

test.use({ baseURL: "http://127.0.0.1:3001" });

test("navigation menu tracks syncing across closes and reloads", async ({
  page,
}) => {
  await page.route("**/api/v1/users/@me", (route) =>
    route.fulfill({
      json: {
        id: "user",
        email: "user@example.com",
        home_currency: "EUR",
        enable_banking_available: true,
      },
    }),
  );
  await page.route("**/api/v1/currencies", (route) =>
    route.fulfill({ json: [{ code: "EUR", exponent: 2 }] }),
  );
  await page.route("**/api/v1/enablebanking/connections", (route) =>
    route.fulfill({
      json: [
        {
          id: "first-bank",
          bank: "First",
          country: "FI",
          psu_type: "personal",
          expires_at: "2027-01-01T00:00:00Z",
          accounts: [
            { uid: "mapped-one", bucket_id: "one" },
            { uid: "unmapped" },
          ],
        },
        {
          id: "second-bank",
          bank: "Second",
          country: "FI",
          psu_type: "personal",
          expires_at: "2027-01-01T00:00:00Z",
          accounts: [{ uid: "mapped-two", bucket_id: "two" }],
        },
      ],
    }),
  );

  let syncing = false;
  await page.route("**/api/v1/enablebanking/sync-status", (route) =>
    route.fulfill({ json: { syncing } }),
  );
  let syncedAccounts: { connection_id: string; account_uid: string }[] = [];
  await page.route("**/api/v1/enablebanking/sync", async (route) => {
    syncedAccounts = (
      route.request().postDataJSON() as {
        accounts: { connection_id: string; account_uid: string }[];
      }
    ).accounts;
    syncing = true;
    return route.fulfill({ status: 202, json: { batch_ids: ["batch"] } });
  });

  await page.setViewportSize({ width: 240, height: 600 });
  await page.goto("/e2e/fixture/?nav");
  await expect
    .poll(() =>
      page.evaluate(
        () => document.documentElement.scrollWidth - window.innerWidth,
      ),
    )
    .toBe(0);
  const menuButton = page.getByRole("button", { name: "Open menu" });
  const menuButtonBox = await menuButton.boundingBox();
  expect(menuButtonBox).not.toBeNull();
  expect(menuButtonBox!.x + menuButtonBox!.width).toBeLessThanOrEqual(240);
  await menuButton.click();
  await expect
    .poll(() =>
      page.evaluate(
        () => document.documentElement.scrollWidth - window.innerWidth,
      ),
    )
    .toBe(0);
  let syncItem = page.getByRole("menuitem", { name: "Sync all" });
  await syncItem.click();

  await expect(syncItem).toBeVisible();
  await expect(syncItem.locator("[data-sync-icon]")).toHaveClass(
    /motion-safe:animate-spin/,
  );
  await expect
    .poll(() => syncedAccounts)
    .toEqual([
      { connection_id: "first-bank", account_uid: "mapped-one" },
      { connection_id: "second-bank", account_uid: "mapped-two" },
    ]);

  await page.keyboard.press("Escape");
  await menuButton.click();
  await expect(
    page
      .getByRole("menuitem", { name: "Sync all" })
      .locator("[data-sync-icon]"),
  ).toHaveClass(/motion-safe:animate-spin/);

  await page.reload();
  await menuButton.click();
  syncItem = page.getByRole("menuitem", { name: "Sync all" });
  await expect(syncItem.locator("[data-sync-icon]")).toHaveClass(
    /motion-safe:animate-spin/,
  );

  syncing = false;
  await expect(syncItem.locator("[data-sync-icon]")).not.toHaveClass(
    /motion-safe:animate-spin/,
  );
});
