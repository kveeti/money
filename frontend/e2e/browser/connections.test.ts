import { expect, test } from "@playwright/test";

test.use({ baseURL: "http://127.0.0.1:3001" });

test("accounts sharing an IBAN use one bucket mapping", async ({ page }) => {
  await page.route("**/api/v1/currencies", (route) =>
    route.fulfill({ json: [{ code: "EUR", exponent: 2 }] }),
  );
  await page.route("**/api/v1/buckets**", (route) =>
    route.fulfill({
      json: [
        {
          id: "bank",
          name: "Revolut",
          kind: "asset",
          hidden: false,
          parent_id: null,
        },
      ],
    }),
  );
  let syncedAccounts: { connection_id: string; account_uid: string }[] = [];
  await page.route("**/api/v1/enablebanking/sync", async (route) => {
    const body = route.request().postDataJSON() as {
      accounts: { connection_id: string; account_uid: string }[];
    };
    syncedAccounts = body.accounts;
    return route.fulfill({ json: { batch_ids: ["batch"] } });
  });
  await page.route("**/api/v1/enablebanking/connections", (route) =>
    route.fulfill({
      json: [
        {
          id: "integration",
          bank: "Revolut",
          country: "FI",
          psu_type: "personal",
          expires_at: "2027-01-01T00:00:00Z",
          accounts: ["EUR", "PLN", "AED"].map((currency) => ({
            uid: `${currency.toLowerCase()}-account`,
            iban: "LT813250066911093214",
            name: "Main account",
            currency,
            bucket_id: "bank",
            bucket_name: "Revolut",
          })),
        },
      ],
    }),
  );

  await page.goto("/e2e/fixture/?connections");

  await expect(page.getByLabel("Country")).toBeVisible();
  await expect(page.getByLabel("Enable Banking bank name")).toBeVisible();
  await expect(page.getByLabel("Account type")).toBeVisible();

  await expect(
    page.getByText("LT813250066911093214", { exact: true }),
  ).toHaveCount(1);
  await expect(
    page.getByRole("combobox").filter({ hasText: "Revolut" }),
  ).toHaveCount(1);
  await expect(
    page.getByRole("button", { name: "Sync", exact: true }),
  ).toHaveCount(1);
  const selectAll = page.getByRole("checkbox", {
    name: "Select all bank accounts",
  });
  const euro = page.getByRole("checkbox", { name: "EUR", exact: true });
  const pln = page.getByRole("checkbox", { name: "PLN", exact: true });
  const aed = page.getByRole("checkbox", { name: "AED", exact: true });
  await expect(selectAll).toBeChecked();
  await expect(euro).toBeChecked();
  await expect(pln).toBeChecked();
  await expect(aed).toBeChecked();

  await page.getByText("PLN", { exact: true }).click();
  await expect(pln).not.toBeChecked();
  await expect(selectAll).toHaveAttribute("data-indeterminate");
  await page.getByRole("button", { name: "Sync", exact: true }).click();
  await expect.poll(() => syncedAccounts.length).toBe(2);
  expect(syncedAccounts.map((account) => account.account_uid)).toEqual([
    "eur-account",
    "aed-account",
  ]);
});
