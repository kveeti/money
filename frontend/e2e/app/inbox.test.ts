import { expect, test } from "@playwright/test";

import { createBucket, importNordea, login, nordeaRow } from "../helpers";

test("search, select all, and create a category against the real inbox", async ({
  page,
}, testInfo) => {
  await login(page, testInfo);
  const checking = await createBucket(page, "asset", "Checking");
  await createBucket(page, "expense", "Food");
  await importNordea(
    page,
    checking.id,
    nordeaRow("2026/07/01", "-10,00", "Train") +
      nordeaRow("2026/07/02", "-20,00", "Hotel") +
      nordeaRow("2026/07/03", "-30,00", "Museum"),
  );

  await page.goto("/inbox");
  const search = page.getByPlaceholder("Search...");
  await search.fill("Hotel");
  await expect(page).toHaveURL(/q=Hotel/);
  await expect(page.getByText("Hotel")).toBeVisible();
  await expect(page.getByText("Train")).not.toBeVisible();
  await page.reload();
  await expect(search).toHaveValue("Hotel");
  await search.fill("");
  await expect(page.getByText("Train")).toBeVisible();

  await page.getByRole("checkbox", { name: "Select rows" }).click();
  await page.getByRole("button", { name: "Select all" }).click();
  await expect(page.getByText("3 selected")).toBeVisible();
  await page.getByRole("button", { name: "Deselect all" }).click();
  await expect(page.getByText("0 selected")).toBeVisible();
  const hotelRow = page.getByRole("listitem").filter({ hasText: "Hotel" });
  await hotelRow.locator("div").first().click();
  await expect(page.getByText("1 selected")).toBeVisible();
  await expect(
    page.getByRole("combobox").filter({ hasText: "Categorize as..." }),
  ).toBeVisible();
  await hotelRow.locator("div").first().click();
  await page.getByRole("button", { name: "Select all" }).click();

  await page
    .getByRole("combobox")
    .filter({ hasText: "Categorize all as..." })
    .click();
  const actionFilter = page.getByRole("combobox", { name: "Filter buckets" });
  await expect(actionFilter).toHaveAttribute(
    "placeholder",
    "Filter categories...",
  );
  await expect(
    page.getByText("Categorize as...", { exact: true }),
  ).toBeVisible();
  await actionFilter.fill("Travel");
  await page.getByRole("option", { name: 'Expense "Travel"' }).click();
  await expect(page.getByText("No transactions to categorize")).toBeVisible();

  await page.getByRole("link", { name: "transactions", exact: true }).click();
  await expect(page.getByText("Train")).toBeVisible();
  await expect(page.getByText("Hotel")).toBeVisible();
  await expect(page.getByText("Museum")).toBeVisible();
  await expect(page.getByText("Travel")).toHaveCount(3);
});

test("splitting an inbox row categorizes it across two buckets", async ({
  page,
}, testInfo) => {
  await login(page, testInfo);
  const checking = await createBucket(page, "asset", "Checking");
  await createBucket(page, "expense", "Groceries");
  await createBucket(page, "person", "Travel");
  await createBucket(page, "expense", "Dining");
  await importNordea(
    page,
    checking.id,
    nordeaRow("2026/07/01", "-10,00", "Split market") +
      nordeaRow("2026/07/02", "-0,01", "Tiny purchase"),
  );

  await page.goto("/inbox");
  await page
    .getByRole("listitem")
    .filter({ hasText: "Split market" })
    .getByRole("button")
    .click();
  await page.getByRole("option", { name: "Split transaction" }).click();
  await expect(page).toHaveURL(/split-inbox=/);
  await page.reload();

  const dialog = page.getByRole("dialog", { name: "Split transaction" });
  await expect(dialog.getByText("Split market")).toBeVisible();
  await expect(dialog.getByLabel("Split 1 amount")).toHaveValue("");
  await expect(dialog.getByLabel("Split 2 amount")).toHaveValue("");
  const categories = dialog.getByRole("combobox");
  await categories.nth(0).click();
  await page.getByRole("option", { name: "Groceries", exact: true }).click();
  await categories.nth(1).click();
  await page
    .getByRole("option", { name: "Travel", exact: true })
    .last()
    .click();
  await expect(dialog.getByRole("combobox")).toHaveCount(3);
  await expect(dialog.getByRole("button", { name: "Add split" })).toHaveCount(
    0,
  );
  await dialog.getByLabel("Split 1 amount").fill("6.00");
  await expect(
    dialog.getByText("€4.00 remaining", { exact: true }),
  ).toBeVisible();
  await dialog.getByLabel("Split 2 amount").fill("4.00");

  let failSplit = true;
  await page.route("**/api/v1/inbox/*/split", async (route) => {
    if (failSplit) {
      failSplit = false;
      await route.fulfill({ status: 500, json: { error: "Split failed" } });
      return;
    }
    await route.continue();
  });
  await dialog.getByRole("button", { name: "Split", exact: true }).click();
  await expect(dialog.getByText("Split failed", { exact: true })).toBeVisible();
  await expect(dialog.getByLabel("Split 1 amount")).toHaveValue("6.00");
  await expect(dialog.getByLabel("Split 2 amount")).toHaveValue("4.00");
  await expect(page).toHaveURL(/split-inbox=/);

  await dialog.getByRole("button", { name: "Split", exact: true }).click();
  await expect(page).not.toHaveURL(/split-inbox=/);
  const tinyRow = page
    .getByRole("listitem")
    .filter({ hasText: "Tiny purchase" });
  await expect(tinyRow).toBeVisible();
  await tinyRow.getByRole("button").click();
  await page.getByRole("option", { name: "Split transaction" }).click();
  await expect(
    dialog.getByText(
      "This transaction cannot be split because each split must be at least €0.01.",
      { exact: true },
    ),
  ).toBeVisible();
  await dialog.getByRole("button", { name: "Close" }).click();
  await expect(page).not.toHaveURL(/split-inbox=/);

  const response = await page.request.get("/api/v1/transactions");
  const body = (await response.json()) as {
    transactions: {
      postings: { bucket: { name: string }; amount: number }[];
    }[];
  };
  expect(body.transactions).toHaveLength(1);
  expect(
    body.transactions[0].postings
      .map((posting) => [posting.bucket.name, posting.amount])
      .sort(([left], [right]) => String(left).localeCompare(String(right))),
  ).toEqual([
    ["Checking", -1000],
    ["Groceries", 600],
    ["Travel", 400],
  ]);

  await page.goto("/transactions");
  const transactionRow = page
    .getByRole("listitem")
    .filter({ hasText: "Split market" });
  await expect(transactionRow).toContainText("Groceries, Travel");
  await expect(transactionRow).toContainText("Checking");
  await expect(transactionRow).toContainText("-€10.00");
  await expect(transactionRow).not.toContainText("€6.00");
  await transactionRow.getByRole("link", { name: "View Split market" }).click();

  const splits = page.getByRole("heading", { name: "Splits" }).locator("../..");
  await expect(splits.getByText("Groceries", { exact: true })).toBeVisible();
  await expect(splits.getByText("Travel", { exact: true })).toBeVisible();
  await expect(splits.getByText("€6.00", { exact: true })).toBeVisible();
  await expect(splits.getByText("€4.00", { exact: true })).toBeVisible();
  const fields = page.getByRole("region", { name: "Transaction fields" });
  await expect(fields.getByText("Checking", { exact: true })).toBeVisible();
  await expect(fields.getByText("Category", { exact: true })).toHaveCount(0);

  await page.getByRole("button", { name: "Edit splits" }).click();
  await expect(page).toHaveURL(/edit-splits=1/);
  let splitDialog = page.getByRole("dialog", { name: "Split transaction" });
  await splitDialog.getByLabel("Split 1 amount").fill("2.00");
  await splitDialog.getByLabel("Split 2 amount").fill("3.00");
  await splitDialog.getByRole("combobox").last().click();
  await page
    .getByRole("option", { name: "Dining", exact: true })
    .last()
    .click();
  await splitDialog.getByLabel("Split 3 amount").fill("5.00");
  await splitDialog.getByRole("button", { name: "Split", exact: true }).click();
  await expect(page).not.toHaveURL(/edit-splits/);
  await expect(splits.getByText("Dining", { exact: true })).toBeVisible();
  await expect(splits.getByText("€2.00", { exact: true })).toBeVisible();
  await expect(splits.getByText("€3.00", { exact: true })).toBeVisible();
  await expect(splits.getByText("€5.00", { exact: true })).toBeVisible();

  await page.getByRole("button", { name: "Edit splits" }).click();
  splitDialog = page.getByRole("dialog", { name: "Split transaction" });
  await splitDialog.getByLabel("Split 1 amount").fill("10.00");
  await splitDialog
    .getByRole("button", { name: /Remove split/ })
    .nth(1)
    .click();
  await splitDialog
    .getByRole("button", { name: /Remove split/ })
    .nth(1)
    .click();
  await splitDialog.getByRole("button", { name: "Split", exact: true }).click();
  await expect(page).not.toHaveURL(/edit-splits/);
  await expect(splitDialog).not.toBeVisible();
  await expect(page.getByRole("heading", { name: "Splits" })).toHaveCount(0);
  await expect(
    fields.getByRole("combobox").filter({ hasText: "Groceries" }),
  ).toBeVisible();

  await page.getByRole("button", { name: "Split transaction" }).click();
  splitDialog = page.getByRole("dialog", { name: "Split transaction" });
  await expect(splitDialog.getByLabel("Split 1 amount")).toHaveValue("10.00");
  await expect(splitDialog.getByLabel("Split 2 amount")).toHaveValue("");
  await splitDialog
    .getByRole("button", { name: /Remove split/ })
    .first()
    .click();
  await splitDialog
    .getByRole("button", { name: /Remove split/ })
    .first()
    .click();
  await splitDialog
    .getByRole("button", { name: /Remove split/ })
    .first()
    .click();
  await expect(
    splitDialog.getByRole("button", { name: /Remove split/ }),
  ).toHaveCount(1);
  await expect(splitDialog.getByRole("combobox")).toHaveCount(1);

  await splitDialog.getByRole("combobox").click();
  await page
    .getByRole("option", { name: "Groceries", exact: true })
    .last()
    .click();
  await splitDialog.getByRole("combobox").last().click();
  await page
    .getByRole("option", { name: "Travel", exact: true })
    .last()
    .click();
  await splitDialog.getByLabel("Split 1 amount").fill("5.00");
  await splitDialog.getByLabel("Split 2 amount").fill("5.00");

  const concurrentChangeStatus = await page.evaluate(async () => {
    const transactionId = window.location.pathname.split("/").at(-1);
    const transaction = (await fetch(
      `/api/v1/transactions/${transactionId}`,
    ).then((response) => response.json())) as {
      postings: { id: string; imported: boolean }[];
    };
    const buckets = (await fetch("/api/v1/buckets").then((response) =>
      response.json(),
    )) as { id: string; name: string }[];
    const posting = transaction.postings.find(
      (candidate) => !candidate.imported,
    );
    const dining = buckets.find((bucket) => bucket.name === "Dining");
    if (!posting || !dining) throw new Error("Missing concurrent edit data");
    return fetch(`/api/v1/postings/${posting.id}`, {
      method: "PATCH",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ bucket_id: dining.id }),
    }).then((response) => response.status);
  });
  expect(concurrentChangeStatus).toBe(200);

  await splitDialog.getByRole("button", { name: "Split", exact: true }).click();
  await expect(
    splitDialog.getByText(
      "transaction postings changed since you opened the editor",
      { exact: true },
    ),
  ).toBeVisible();
  await expect(splitDialog.getByLabel("Split 1 amount")).toHaveValue("5.00");
  await expect(splitDialog.getByLabel("Split 2 amount")).toHaveValue("5.00");
  await expect(page).toHaveURL(/edit-splits=1/);
  await splitDialog.getByRole("button", { name: "Cancel" }).click();
  await expect(page).not.toHaveURL(/edit-splits/);
});

test("inbox and transactions load older rows from the real backend", async ({
  page,
}, testInfo) => {
  await login(page, testInfo);
  const checking = await createBucket(page, "asset", "Checking");
  const groceries = await createBucket(page, "expense", "Groceries");
  const rows = [
    ...Array.from({ length: 100 }, (_, index) =>
      nordeaRow("2026/07/02", "-1,00", `Current page ${index + 1}`),
    ),
    nordeaRow("2026/07/01", "-1,00", "Older page row"),
  ].join("");
  await importNordea(page, checking.id, rows);

  await page.goto("/inbox");
  await expect(page.locator("main").getByRole("listitem")).toHaveCount(100);
  await expect(page.getByText("Older page row")).not.toBeVisible();
  await page.getByRole("button", { name: "Load older" }).click();
  await expect(page.getByText("Older page row")).toBeVisible();
  await expect(page.locator("main").getByRole("listitem")).toHaveCount(101);

  const firstPage = await page.request.get("/api/v1/inbox");
  expect(firstPage).toBeOK();
  const first = (await firstPage.json()) as {
    rows: { id: string }[];
    next_cursor: { date: string; id: string } | null;
  };
  expect(first.next_cursor).toBeTruthy();
  const secondPage = await page.request.get(
    `/api/v1/inbox?before_date=${encodeURIComponent(first.next_cursor!.date)}&before_id=${first.next_cursor!.id}`,
  );
  expect(secondPage).toBeOK();
  const second = (await secondPage.json()) as { rows: { id: string }[] };
  const categorized = await page.request.post("/api/v1/inbox/categorize", {
    data: {
      row_ids: [...first.rows, ...second.rows].map((row) => row.id),
      bucket_id: groceries.id,
    },
  });
  expect(categorized).toBeOK();

  await page.goto("/transactions");
  await expect(page.locator("main").getByRole("listitem")).toHaveCount(100);
  await expect(page.getByText("Older page row")).not.toBeVisible();
  await page.getByRole("button", { name: "Load older" }).click();
  await expect(page.getByText("Older page row")).toBeVisible();
  await page.getByRole("checkbox", { name: "Select transactions" }).click();
  await page.getByRole("button", { name: "Select all" }).click();
  await expect(page.getByText("101 selected")).toBeVisible();
});
