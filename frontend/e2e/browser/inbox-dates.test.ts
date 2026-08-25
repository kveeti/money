import { expect, test, type Page } from "@playwright/test";

const source = {
  id: "source",
  date: "2001-07-01",
  occurred_at: null,
  amount: -1000,
  currency: "EUR",
  counterparty: "Source",
  description: "",
  account: "Checking",
};

const match = {
  id: "match",
  date: "2001-07-02",
  occurred_at: null,
  amount: 1000,
  currency: "EUR",
  counterparty: "Candidate",
  description: "",
  account: "Savings",
  kind: "transfer",
};

async function mockInbox(page: Page, date = source.date) {
  await page.route("**/api/v1/currencies", (route) =>
    route.fulfill({ json: [{ code: "EUR", exponent: 2 }] }),
  );
  await page.route("**/api/v1/buckets**", (route) =>
    route.fulfill({ json: [] }),
  );
  await page.route("**/api/v1/inbox/source/matches**", (route) =>
    route.fulfill({ json: { source, matches: [match] } }),
  );
  await page.route(/\/api\/v1\/inbox(?:\?.*)?$/, (route) =>
    route.fulfill({
      json: { rows: [{ ...source, date }], next_cursor: null },
    }),
  );
}

test.describe("date-only values west of UTC", () => {
  test.use({
    baseURL: "http://127.0.0.1:3001",
    timezoneId: "America/Los_Angeles",
  });

  test("inbox and match dates keep their stored calendar day", async ({
    page,
  }) => {
    await mockInbox(page);
    await page.goto("/e2e/fixture/");

    await expect
      .soft(page.getByRole("heading", { name: "Jul 1, 2001" }))
      .toBeVisible();

    await page.getByRole("button", { name: /Source/ }).click();
    await page.getByRole("option", { name: "Match transactions" }).click();

    const dialog = page.getByRole("dialog", { name: "Match transactions" });
    await expect(dialog).toBeVisible();
    await expect.soft(dialog.locator("section")).toContainText("Jul 1, 2001");
    await expect(
      dialog.getByRole("option").filter({ hasText: "Candidate" }),
    ).toContainText("Jul 2, 2001");
  });
});

test.describe("date-only values on a skipped local day", () => {
  test.use({
    baseURL: "http://127.0.0.1:3001",
    timezoneId: "Pacific/Apia",
  });

  test("inbox date does not depend on whether local midnight existed", async ({
    page,
  }) => {
    const date = "2011-12-30";
    await mockInbox(page, date);
    await page.goto("/e2e/fixture/");

    await expect(
      page.getByRole("heading", { name: "Dec 30, 2011" }),
    ).toBeVisible();
  });
});
