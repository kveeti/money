import { expect, test, type Page } from "@playwright/test";

test.use({ baseURL: "http://127.0.0.1:3001" });

const buckets = ["Restaurants", "Groceries"].map((name, index) => ({
  id: `b${index + 1}`,
  kind: "expense",
  name,
  hidden: false,
}));

async function mockApi(page: Page, rowCount: number) {
  const categorizedIds = new Set<string>();
  const rows = Array.from({ length: rowCount }, (_, index) => ({
    id: `r${index + 1}`,
    counterparty: `Row ${index + 1}`,
    date: "2026-07-01",
    amount: -1000,
    currency: "EUR",
    description: "",
    account: "Checking",
  }));

  await page.route("**/api/v1/currencies", (route) =>
    route.fulfill({ json: [{ code: "EUR", exponent: 2 }] }),
  );
  await page.route("**/api/v1/buckets**", (route) =>
    route.fulfill({ json: buckets }),
  );
  await page.route(/\/api\/v1\/inbox(?:\?.*)?$/, (route) =>
    route.fulfill({
      json: {
        rows: rows.filter((row) => !categorizedIds.has(row.id)),
        next_cursor: null,
      },
    }),
  );
  await page.route("**/api/v1/inbox/categorize", (route) => {
    const body = route.request().postDataJSON() as { row_ids: string[] };
    for (const id of body.row_ids) categorizedIds.add(id);
    return route.fulfill({ json: { categorized: body.row_ids.length } });
  });
}

for (const rowCount of [3, 1]) {
  test(`categorizing closes the picker with ${rowCount} inbox rows`, async ({
    page,
  }) => {
    await mockApi(page, rowCount);
    await page.goto("/e2e/fixture/");
    await page.getByRole("button", { name: "Row 1" }).click();
    const picker = page.locator('[aria-label^="Choose category"]');
    await expect(picker).toBeVisible();
    await page
      .getByRole("option", { name: "Restaurants", exact: true })
      .click();
    await expect(picker).not.toBeAttached();
    await expect(page.getByRole("button", { name: "Row 1" })).not.toBeVisible();
    if (rowCount === 1) {
      await expect(
        page.getByText("No transactions to categorize"),
      ).toBeVisible();
    } else {
      await expect(page.getByRole("button", { name: "Row 2" })).toBeVisible();
    }
  });
}

test("Escape closes the picker without categorizing and returns focus", async ({
  page,
}) => {
  await mockApi(page, 3);
  await page.goto("/e2e/fixture/");
  const trigger = page.getByRole("button", { name: "Row 2" });
  const picker = page.locator('[aria-label^="Choose category"]');
  await trigger.click();
  await expect(picker).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(picker).not.toBeAttached();
  await expect(trigger).toBeFocused();
  await trigger.click();
  await expect(picker).toBeVisible();
  await expect(page.getByRole("option", { name: "Restaurants" })).toBeVisible();
});
