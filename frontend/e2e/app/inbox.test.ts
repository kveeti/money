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

test("signed entries settle a security deposit with a withheld expense", async ({
  page,
}, testInfo) => {
  await login(page, testInfo);
  const checking = await createBucket(page, "asset", "Checking");
  await createBucket(page, "asset", "Rental security deposit");
  await createBucket(page, "expense", "Rent");
  await createBucket(page, "expense", "Water");
  await importNordea(
    page,
    checking.id,
    nordeaRow("2024/07/01", "-869,99", "Deposit paid") +
      nordeaRow("2026/07/01", "570,00", "Deposit returned"),
  );

  await page.goto("/inbox");
  const paid = page.getByRole("listitem").filter({ hasText: "Deposit paid" });
  await paid.getByRole("button").click();
  await page.getByRole("option", { name: "Edit entries" }).click();
  await expect(
    page.locator('[aria-label="Choose category, person, or action"]'),
  ).not.toBeAttached();
  let dialog = page.getByRole("dialog", { name: "Edit entries" });
  await dialog
    .getByRole("combobox")
    .filter({ hasText: "Account, category, or person" })
    .click();
  await dialog
    .getByRole("option", { name: "Rental security deposit", exact: true })
    .click();
  await expect(
    dialog.getByRole("option", {
      name: "Rental security deposit",
      exact: true,
    }),
  ).toHaveCount(0);
  await dialog.getByLabel("Entry 1 amount").fill("580.00");
  await dialog
    .getByRole("combobox")
    .filter({ hasText: "Account, category, or person" })
    .click();
  await dialog.getByRole("option", { name: "Rent", exact: true }).click();
  await expect(
    dialog.getByRole("option", { name: "Rent", exact: true }),
  ).toHaveCount(0);
  await dialog.getByLabel("Entry 2 amount").fill("289.99");
  await dialog.getByRole("button", { name: "Save entries" }).click();

  const returned = page
    .getByRole("listitem")
    .filter({ hasText: "Deposit returned" });
  await returned.getByRole("button").click();
  await page.getByRole("option", { name: "Edit entries" }).click();
  await expect(
    page.locator('[aria-label="Choose category, person, or action"]'),
  ).not.toBeAttached();
  dialog = page.getByRole("dialog", { name: "Edit entries" });
  await dialog
    .getByRole("combobox")
    .filter({ hasText: "Account, category, or person" })
    .click();
  await dialog
    .getByRole("option", { name: "Rental security deposit", exact: true })
    .click();
  await expect(
    dialog.getByRole("option", {
      name: "Rental security deposit",
      exact: true,
    }),
  ).toHaveCount(0);
  await dialog.getByLabel("Entry 1 amount").fill("580.00");
  await dialog.getByLabel("Entry 1 direction").selectOption("from");
  await dialog
    .getByRole("combobox")
    .filter({ hasText: "Account, category, or person" })
    .click();
  await dialog.getByRole("option", { name: "Water", exact: true }).click();
  await expect(
    dialog.getByRole("option", { name: "Water", exact: true }),
  ).toHaveCount(0);
  await dialog.getByLabel("Entry 2 direction").selectOption("to");
  await dialog.getByLabel("Entry 2 amount").fill("10.00");
  await dialog.getByRole("button", { name: "Save entries" }).click();

  await expect(page.getByText("No transactions to categorize")).toBeVisible();
  await expect
    .poll(async () => {
      const response = await page.request.get("/api/v1/transactions");
      const body = (await response.json()) as {
        transactions: {
          postings: { bucket: { name: string }; amount: number }[];
        }[];
      };
      return body.transactions
        .flatMap((transaction) => transaction.postings)
        .reduce<Record<string, number>>((totals, posting) => {
          totals[posting.bucket.name] =
            (totals[posting.bucket.name] ?? 0) + posting.amount;
          return totals;
        }, {});
    })
    .toEqual({
      Checking: -29999,
      Rent: 28999,
      "Rental security deposit": 0,
      Water: 1000,
    });

  await page.goto("/transactions");
  const paidTransaction = page
    .getByRole("listitem")
    .filter({ hasText: "Deposit paid" });
  await expect(paidTransaction).toContainText("-€869.99");
  await expect(
    paidTransaction.getByText("To Rental security deposit, Rent", {
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    paidTransaction.getByText("Checking", { exact: true }),
  ).toBeVisible();

  const returnedTransaction = page
    .getByRole("listitem")
    .filter({ hasText: "Deposit returned" });
  await expect(returnedTransaction).toContainText("€570.00");
  await expect(
    returnedTransaction.getByText("From Rental security deposit, To Water", {
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    returnedTransaction.getByText("Checking", { exact: true }),
  ).toBeVisible();
  await returnedTransaction
    .getByRole("link", { name: "View Deposit returned" })
    .click();
  const entries = page
    .getByRole("heading", { name: "Entries" })
    .locator("../..");
  await expect(
    entries.getByText("From Rental security deposit", { exact: true }),
  ).toBeVisible();
  await expect(entries.getByText("To Water", { exact: true })).toBeVisible();
  await entries.getByRole("button", { name: "Edit entries" }).click();
  dialog = page.getByRole("dialog", { name: "Edit entries" });
  await expect(dialog.getByLabel("Entry 1 direction")).toHaveValue("from");
  await expect(dialog.getByLabel("Entry 1 amount")).toHaveValue("580.00");
  await expect(dialog.getByLabel("Entry 2 direction")).toHaveValue("to");
  await expect(dialog.getByLabel("Entry 2 amount")).toHaveValue("10.00");
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(dialog).toHaveAttribute("data-ending-style", "");
  await expect(dialog).not.toBeAttached();
});

test("mixed category directions show the bank amount and both entries", async ({
  page,
}, testInfo) => {
  await login(page, testInfo);
  const checking = await createBucket(page, "asset", "Checking");
  const salary = await createBucket(page, "income", "Salary");
  const tax = await createBucket(page, "expense", "Tax");
  await importNordea(
    page,
    checking.id,
    nordeaRow("2026/07/01", "570,00", "Pay after withholding"),
  );
  const response = await page.request.get("/api/v1/inbox");
  expect(response).toBeOK();
  const inbox = (await response.json()) as { rows: { id: string }[] };
  const created = await page.request.post(
    `/api/v1/inbox/${inbox.rows[0].id}/entries`,
    {
      data: {
        postings: [
          { bucket_id: salary.id, amount: -58000 },
          { bucket_id: tax.id, amount: 1000 },
        ],
      },
    },
  );
  expect(created).toBeOK();

  await page.goto("/transactions");
  const transaction = page
    .getByRole("listitem")
    .filter({ hasText: "Pay after withholding" });
  await expect(transaction).toContainText("€570.00");
  await expect(
    transaction.getByText("From Salary, To Tax", { exact: true }),
  ).toBeVisible();
  await expect(
    transaction.getByText("Checking", { exact: true }),
  ).toBeVisible();
});

test("editing inbox entries categorizes it across two buckets", async ({
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
  await page.getByRole("option", { name: "Edit entries" }).click();
  await expect(page).toHaveURL(/entries-inbox=/);
  await page.reload();

  const dialog = page.getByRole("dialog", { name: "Edit entries" });
  await expect(dialog.getByText("Split market")).toBeVisible();
  await expect(dialog.getByLabel("Entry 1 amount")).toHaveValue("");
  let entryBuckets = dialog
    .getByRole("combobox")
    .filter({ hasText: "Account, category, or person" });
  await entryBuckets.first().click();
  await page.getByRole("option", { name: "Groceries", exact: true }).click();
  entryBuckets = dialog
    .getByRole("combobox")
    .filter({ hasText: "Account, category, or person" });
  await entryBuckets.last().click();
  await page
    .getByRole("option", { name: "Travel", exact: true })
    .last()
    .click();
  await expect(dialog.getByLabel(/Entry \d+ direction/)).toHaveCount(3);
  await dialog.getByLabel("Entry 1 amount").fill("6.00");
  await expect(
    dialog.getByText("Difference: €4.00", { exact: true }),
  ).toBeVisible();
  await dialog.getByLabel("Entry 2 amount").fill("4.00");

  let failEntries = true;
  await page.route("**/api/v1/inbox/*/entries", async (route) => {
    if (failEntries) {
      failEntries = false;
      await route.fulfill({ status: 500, json: { error: "Entries failed" } });
      return;
    }
    await route.continue();
  });
  await dialog.getByRole("button", { name: "Save entries" }).click();
  await expect(
    dialog.getByText("Entries failed", { exact: true }),
  ).toBeVisible();
  await expect(dialog.getByLabel("Entry 1 amount")).toHaveValue("6.00");
  await expect(dialog.getByLabel("Entry 2 amount")).toHaveValue("4.00");
  await expect(page).toHaveURL(/entries-inbox=/);

  await dialog.getByRole("button", { name: "Save entries" }).click();
  await expect(page).not.toHaveURL(/entries-inbox=/);
  await expect(
    page.getByRole("listitem").filter({ hasText: "Tiny purchase" }),
  ).toBeVisible();

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

  const entries = page
    .getByRole("heading", { name: "Entries" })
    .locator("../..");
  await expect(
    entries.getByText("To Groceries", { exact: true }),
  ).toBeVisible();
  await expect(entries.getByText("To Travel", { exact: true })).toBeVisible();
  await expect(entries.getByText("€6.00", { exact: true })).toBeVisible();
  await expect(entries.getByText("€4.00", { exact: true })).toBeVisible();
  const fields = page.getByRole("region", { name: "Transaction fields" });
  await expect(fields.getByText("Checking", { exact: true })).toBeVisible();
  await expect(fields.getByText("Category", { exact: true })).toHaveCount(0);

  await entries.getByRole("button", { name: "Edit entries" }).click();
  await expect(page).toHaveURL(/edit-entries=1/);
  let entriesDialog = page.getByRole("dialog", { name: "Edit entries" });
  await entriesDialog.getByLabel("Entry 1 amount").fill("2.00");
  await entriesDialog.getByLabel("Entry 2 amount").fill("3.00");
  await entriesDialog
    .getByRole("combobox")
    .filter({ hasText: "Account, category, or person" })
    .last()
    .click();
  await page
    .getByRole("option", { name: "Dining", exact: true })
    .last()
    .click();
  await entriesDialog.getByLabel("Entry 3 amount").fill("5.00");
  await entriesDialog.getByRole("button", { name: "Save entries" }).click();
  await expect(page).not.toHaveURL(/edit-entries/);
  await expect(entries.getByText("To Dining", { exact: true })).toBeVisible();
  await expect(entries.getByText("€2.00", { exact: true })).toBeVisible();
  await expect(entries.getByText("€3.00", { exact: true })).toBeVisible();
  await expect(entries.getByText("€5.00", { exact: true })).toBeVisible();

  await entries.getByRole("button", { name: "Edit entries" }).click();
  entriesDialog = page.getByRole("dialog", { name: "Edit entries" });
  await entriesDialog.getByLabel("Entry 1 amount").fill("10.00");
  await entriesDialog
    .getByRole("button", { name: /Remove entry/ })
    .nth(1)
    .click();
  await entriesDialog
    .getByRole("button", { name: /Remove entry/ })
    .nth(1)
    .click();
  await entriesDialog.getByRole("button", { name: "Save entries" }).click();
  await expect(page).not.toHaveURL(/edit-entries/);
  await expect(entriesDialog).not.toBeVisible();
  await expect(page.getByRole("heading", { name: "Entries" })).toHaveCount(0);
  await expect(
    fields.getByRole("combobox").filter({ hasText: "Groceries" }),
  ).toBeVisible();

  await page.getByRole("button", { name: "Edit entries" }).click();
  entriesDialog = page.getByRole("dialog", { name: "Edit entries" });
  await expect(entriesDialog.getByLabel("Entry 1 amount")).toHaveValue("10.00");
  await expect(entriesDialog.getByLabel("Entry 2 amount")).toHaveValue("");
  await entriesDialog
    .getByRole("button", { name: /Remove entry/ })
    .first()
    .click();
  await entriesDialog
    .getByRole("button", { name: /Remove entry/ })
    .first()
    .click();
  await expect(
    entriesDialog.getByRole("button", { name: /Remove entry/ }),
  ).toHaveCount(1);
  await expect(entriesDialog.getByLabel(/Entry \d+ direction/)).toHaveCount(1);

  let emptyBuckets = entriesDialog
    .getByRole("combobox")
    .filter({ hasText: "Account, category, or person" });
  await emptyBuckets.first().click();
  await page
    .getByRole("option", { name: "Groceries", exact: true })
    .last()
    .click();
  emptyBuckets = entriesDialog
    .getByRole("combobox")
    .filter({ hasText: "Account, category, or person" });
  await emptyBuckets.last().click();
  await page
    .getByRole("option", { name: "Travel", exact: true })
    .last()
    .click();
  await entriesDialog.getByLabel("Entry 1 amount").fill("5.00");
  await entriesDialog.getByLabel("Entry 2 amount").fill("5.00");

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

  await entriesDialog.getByRole("button", { name: "Save entries" }).click();
  await expect(
    entriesDialog.getByText(
      "transaction postings changed since you opened the editor",
      { exact: true },
    ),
  ).toBeVisible();
  await expect(entriesDialog.getByLabel("Entry 1 amount")).toHaveValue("5.00");
  await expect(entriesDialog.getByLabel("Entry 2 amount")).toHaveValue("5.00");
  await expect(page).toHaveURL(/edit-entries=1/);
  await entriesDialog.getByRole("button", { name: "Cancel" }).click();
  await expect(page).not.toHaveURL(/edit-entries/);
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
