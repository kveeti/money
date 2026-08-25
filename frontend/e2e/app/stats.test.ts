import { expect, test } from "@playwright/test";

import { createBucket, createTransaction, login } from "../helpers";

function previousMonthStart(today: string) {
  const date = new Date(`${today}T12:00:00Z`);
  return new Date(Date.UTC(date.getUTCFullYear(), date.getUTCMonth() - 1, 1))
    .toISOString()
    .slice(0, 10);
}

test("stats shows an empty state when the selected period has no transactions", async ({
  page,
}, testInfo) => {
  await login(page, testInfo);
  await page.getByRole("link", { name: "stats", exact: true }).click();

  await expect(
    page.getByRole("heading", { name: "No transactions in this period" }),
  ).toBeVisible();
  await expect(
    page.getByText(
      "Choose another period, or add transactions to start tracking your spending and income.",
    ),
  ).toBeVisible();
});

test("stats periods, comparisons, and category expansion use persisted transactions", async ({
  page,
}, testInfo) => {
  await login(page, testInfo);
  const today = await page.evaluate(() => {
    const now = new Date();
    const year = now.getFullYear();
    const month = String(now.getMonth() + 1).padStart(2, "0");
    const day = String(now.getDate()).padStart(2, "0");
    return `${year}-${month}-${day}`;
  });
  const currentStart = `${today.slice(0, 7)}-01`;
  const comparisonStart = previousMonthStart(today);

  const bank = await createBucket(page, "asset", "Bank");
  const food = await createBucket(page, "expense", "Food");
  const groceries = await createBucket(page, "expense", "Groceries", food.id);
  const salary = await createBucket(page, "income", "Salary");

  await createTransaction(page, {
    date: currentStart,
    accountId: bank.id,
    categoryId: food.id,
    amount: 20_000,
    counterparty: "Cafe",
  });
  await createTransaction(page, {
    date: currentStart,
    accountId: bank.id,
    categoryId: groceries.id,
    amount: 60_000,
    counterparty: "Market",
  });
  await createTransaction(page, {
    date: comparisonStart,
    accountId: bank.id,
    categoryId: groceries.id,
    amount: 65_000,
    counterparty: "Old market",
  });
  await createTransaction(page, {
    date: currentStart,
    accountId: bank.id,
    categoryId: salary.id,
    amount: 200_000,
    counterparty: "Employer",
    kind: "income",
  });
  await createTransaction(page, {
    date: comparisonStart,
    accountId: bank.id,
    categoryId: salary.id,
    amount: 200_000,
    counterparty: "Old employer",
    kind: "income",
  });

  await page.getByRole("link", { name: "stats", exact: true }).click();
  const summary = page.getByRole("region", { name: "Summary" });
  await expect(
    summary
      .locator("article")
      .filter({ hasText: "Expenses" })
      .locator("strong"),
  ).toContainText("€800.00");
  await expect(
    summary.locator("article").filter({ hasText: "Income" }).locator("strong"),
  ).toContainText("€2,000.00");
  await expect(
    summary.locator("article").filter({ hasText: "Net" }).locator("strong"),
  ).toContainText("€1,200.00");

  const foodRow = page.getByRole("button", { name: /Food/ });
  await expect(foodRow).toContainText("€800");
  await expect(foodRow).toContainText("€650");
  await foodRow.click();
  await expect(page.getByText("Other", { exact: true })).toBeVisible();
  await expect(page.getByText("Groceries", { exact: true })).toBeVisible();

  await page.getByRole("button", { name: "last year" }).click();
  await expect(page).toHaveURL(/compare=year/);
  await expect(foodRow).toContainText("€0");

  await page.getByRole("button", { name: "custom" }).click();
  await expect(page).toHaveURL(/period=custom/);
  await expect(page.getByLabel("From date")).toHaveValue(currentStart);
  await expect(page.getByLabel("To date")).toHaveValue(today);

  await page.reload();
  await expect(page.getByLabel("From date")).toHaveValue(currentStart);

  await page.getByRole("button", { name: "week" }).click();
  await expect(page).toHaveURL(/period=week/);
  await page.getByRole("button", { name: "Previous period" }).click();
  await expect(page).toHaveURL(/anchor=/);

  await page.getByRole("button", { name: "year", exact: true }).click();
  await expect(page).toHaveURL(/period=year/);
  await expect(page).toHaveURL(/compare=previous/);
});
