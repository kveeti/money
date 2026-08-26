import { expect, test, type Page, type TestInfo } from "@playwright/test";

import {
  createBucket,
  createTransaction,
  importNordea,
  login,
  nordeaRow,
} from "../helpers";

function row(page: Page, name: string) {
  return page.getByRole("listitem").filter({ hasText: name });
}

async function selectRow(page: Page, name: string) {
  await row(page, name).locator("div").first().click();
}

async function setupTransactions(page: Page, testInfo: TestInfo) {
  await login(page, testInfo);
  const checking = await createBucket(page, "asset", "Checking");
  const groceries = await createBucket(page, "expense", "Groceries");
  await createBucket(page, "expense", "Dining");
  await importNordea(
    page,
    checking.id,
    nordeaRow("2026/07/01", "-12,00", "Failure one") +
      nordeaRow("2026/07/02", "-8,00", "Failure two"),
  );
  const inboxResponse = await page.request.get("/api/v1/inbox");
  const inbox = (await inboxResponse.json()) as { rows: { id: string }[] };
  const categorized = await page.request.post("/api/v1/inbox/categorize", {
    data: {
      row_ids: inbox.rows.map((item) => item.id),
      bucket_id: groceries.id,
    },
  });
  expect(categorized).toBeOK();

  await page.goto("/transactions");
}

test("transaction date and month headings group only matching dates", async ({
  page,
}, testInfo) => {
  await login(page, testInfo);
  const checking = await createBucket(page, "asset", "Checking");
  const groceries = await createBucket(page, "expense", "Groceries");
  const headings = await page.evaluate(() => {
    const dateParts = (date: Date) =>
      Object.fromEntries(
        new Intl.DateTimeFormat("en-CA", {
          year: "numeric",
          month: "2-digit",
          day: "2-digit",
        })
          .formatToParts(date)
          .filter(({ type }) => type !== "literal")
          .map(({ type, value }) => [type, value]),
      );
    const today = new Date();
    const format = (date: Date) => ({
      date: new Intl.DateTimeFormat(undefined, {
        month: "short",
        day: "numeric",
        year: "numeric",
      }).format(date),
      month: new Intl.DateTimeFormat(undefined, {
        month: "long",
        year: "numeric",
      }).format(date),
    });
    const todayParts = dateParts(today);

    return {
      today: `${todayParts.year}-${todayParts.month}-${todayParts.day}`,
      todayDate: new Intl.DateTimeFormat(undefined, {
        weekday: "short",
        month: "short",
        day: "numeric",
      }).format(today),
      todayMonth: new Intl.DateTimeFormat(undefined, {
        month: "long",
      }).format(today),
      july2001: format(new Date("2001-07-01T12:00:00Z")),
      july2000: format(new Date("2000-07-02T12:00:00Z")),
    };
  });

  for (const [date, counterparty] of [
    [headings.today, "Today one"],
    [headings.today, "Today two"],
    ["2001-07-01", "July 2001"],
    ["2000-07-02", "July 2000"],
  ]) {
    await createTransaction(page, {
      date,
      counterparty,
      accountId: checking.id,
      categoryId: groceries.id,
      amount: 12,
    });
  }

  await page.goto("/transactions?range=all-time");
  await expect(
    page.getByRole("heading", {
      level: 3,
      name: headings.todayDate,
      exact: true,
    }),
  ).toHaveCount(1);
  await expect(
    page.getByRole("heading", {
      level: 3,
      name: headings.july2001.date,
      exact: true,
    }),
  ).toHaveCount(1);
  await expect(
    page.getByRole("heading", {
      level: 3,
      name: headings.july2000.date,
      exact: true,
    }),
  ).toHaveCount(1);
  await expect(
    page.getByRole("heading", {
      level: 2,
      name: headings.todayMonth,
      exact: true,
    }),
  ).toHaveCount(1);
  await expect(
    page.getByRole("heading", {
      level: 2,
      name: headings.july2001.month,
      exact: true,
    }),
  ).toHaveCount(1);
  await expect(
    page.getByRole("heading", {
      level: 2,
      name: headings.july2000.month,
      exact: true,
    }),
  ).toHaveCount(1);
});

async function selectFailureTransactions(page: Page) {
  await page.getByRole("checkbox", { name: "Select transactions" }).click();
  await selectRow(page, "Failure one");
  await selectRow(page, "Failure two");
}

async function holdFailedRequest(
  page: Page,
  url: string,
  method: string,
  action: () => Promise<void>,
) {
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });

  await page.route(url, async (route) => {
    if (route.request().method() !== method) {
      await route.continue();
      return;
    }
    await gate;
    await route.fulfill({
      status: 500,
      contentType: "application/json",
      body: JSON.stringify({ error: "test failure" }),
    });
  });

  const response = page.waitForResponse(
    (next) =>
      next.url().includes(url.replace("**", "")) &&
      next.request().method() === method &&
      next.status() === 500,
  );
  await action();

  return async () => {
    release();
    await response;
    await page.unroute(url);
  };
}

test("bulk tagging selected imported transactions persists", async ({
  page,
}, testInfo) => {
  await setupTransactions(page, testInfo);
  await selectFailureTransactions(page);
  await page.getByRole("combobox").filter({ hasText: "Actions" }).click();
  await page
    .getByRole("combobox", { name: "Filter transaction actions" })
    .fill("reviewed");
  const tag = page.getByRole("option").filter({ hasText: "Create #reviewed" });
  await expect(tag).toBeVisible();

  const response = page.waitForResponse(
    (next) =>
      next.url().endsWith("/api/v1/transactions/tags") &&
      next.request().method() === "POST",
  );
  await tag.click();
  expect((await response).status()).toBe(200);

  await page.reload();
  await expect(row(page, "Failure one")).toContainText("#reviewed");
  await expect(row(page, "Failure two")).toContainText("#reviewed");
});

test("search, recategorize, and remove persisted transactions", async ({
  page,
}, testInfo) => {
  await login(page, testInfo);
  const checking = await createBucket(page, "asset", "Checking");
  const groceries = await createBucket(page, "expense", "Groceries");
  await createBucket(page, "expense", "Dining");
  await createBucket(page, "person", "Bob");
  await importNordea(
    page,
    checking.id,
    nordeaRow("2026/07/01", "-12,00", "Market one") +
      nordeaRow("2026/07/02", "-8,00", "Market two"),
  );
  const inboxResponse = await page.request.get("/api/v1/inbox");
  const inbox = (await inboxResponse.json()) as { rows: { id: string }[] };
  const categorized = await page.request.post("/api/v1/inbox/categorize", {
    data: {
      row_ids: inbox.rows.map((item) => item.id),
      bucket_id: groceries.id,
    },
  });
  expect(categorized).toBeOK();

  await page.goto("/transactions");
  const search = page.getByPlaceholder("Search...");
  await search.fill("Market one");
  await expect(page).toHaveURL(/q=Market(%20|\+)one/);
  await expect(page.getByText("Market one")).toBeVisible();
  await expect(page.getByText("Market two")).not.toBeVisible();
  await page.reload();
  await expect(search).toHaveValue("Market one");
  await search.fill("");
  await expect(page.getByText("Market two")).toBeVisible();

  await page.getByRole("checkbox", { name: "Select transactions" }).click();
  await selectRow(page, "Market one");
  await page.getByRole("combobox").filter({ hasText: "Actions" }).click();
  await expect(page.getByText("Categorize as…")).toBeVisible();
  await expect(page.getByRole("option", { name: "Bob" })).toBeVisible();
  await page.getByRole("option", { name: "Dining" }).click();
  await expect(row(page, "Market one")).toContainText("Dining");

  await page.getByRole("checkbox", { name: "Select transactions" }).click();
  await selectRow(page, "Market two");
  await page.getByRole("combobox").filter({ hasText: "Actions" }).click();
  await page.getByRole("option", { name: "Remove transactions" }).click();
  await expect(page.getByRole("alertdialog")).toContainText("Are you sure?");
  await expect(page.getByText("Market two")).toBeVisible();
  await page
    .getByRole("button", { name: "Remove transaction", exact: true })
    .click();
  await expect(page.getByText("Market two")).not.toBeVisible();

  await page.getByRole("link", { name: "inbox", exact: true }).click();
  await expect(page.getByText("Market two")).toBeVisible();
  await page.reload();
  await expect(page.getByText("Market two")).toBeVisible();
});

test("transaction detail edits memo, category, and tags", async ({
  page,
}, testInfo) => {
  await login(page, testInfo);
  const checking = await createBucket(page, "asset", "Checking");
  const groceries = await createBucket(page, "expense", "Groceries");
  await createBucket(page, "expense", "Dining");
  await importNordea(
    page,
    checking.id,
    nordeaRow("2026/07/01", "-12,00", "Detail Market"),
  );
  const inboxResponse = await page.request.get("/api/v1/inbox");
  const inbox = (await inboxResponse.json()) as { rows: { id: string }[] };
  const categorized = await page.request.post("/api/v1/inbox/categorize", {
    data: { row_ids: [inbox.rows[0].id], bucket_id: groceries.id },
  });
  expect(categorized).toBeOK();
  await createTransaction(page, {
    date: "2026-07-02",
    accountId: checking.id,
    categoryId: groceries.id,
    amount: 800,
    counterparty: "Other market",
  });

  await page.goto("/transactions");
  await page.getByRole("link", { name: "View Detail Market" }).click();
  await expect(page).toHaveURL(/\/transactions\/[0-9a-f-]+$/);

  await expect(
    page.getByRole("heading", { name: "Detail Market" }),
  ).toBeVisible();
  const fields = page.getByRole("region", { name: "Transaction fields" });
  await expect(fields.getByText("Account", { exact: true })).toBeVisible();
  await expect(fields.getByText("Checking", { exact: true })).toBeVisible();
  await expect(fields.getByLabel("Counterparty")).toHaveCount(0);
  await expect(fields.getByLabel("Description")).toHaveCount(0);
  await Promise.all([
    page.waitForResponse(
      (response) =>
        response.url().includes("/api/v1/transactions/") &&
        response.request().method() === "PATCH",
    ),
    fields.getByLabel("Memo").fill("Work meal"),
  ]);

  await page.getByRole("combobox").filter({ hasText: "Groceries" }).click();
  await Promise.all([
    page.waitForResponse(
      (response) =>
        response.url().includes("/api/v1/postings/") &&
        response.request().method() === "PATCH",
    ),
    page.getByRole("option", { name: "Dining" }).click(),
  ]);
  await expect(
    page.getByRole("combobox").filter({ hasText: "Dining" }),
  ).toBeVisible();

  const tagTrigger = page.getByRole("combobox", {
    name: "Tags",
    exact: true,
  });
  await tagTrigger.click();
  await page
    .getByRole("combobox", { name: "Change or add tags..." })
    .fill("Travel");
  const travelOption = page.getByRole("option").filter({ hasText: "#travel" });
  await expect(travelOption).toBeVisible();
  await Promise.all([
    page.waitForResponse(
      (response) =>
        response.url().endsWith("/api/v1/transactions/tags") &&
        response.request().method() === "POST",
    ),
    travelOption.click(),
  ]);
  await expect(tagTrigger).toContainText("#travel");

  await tagTrigger.click();
  await expect(
    travelOption.getByRole("checkbox", {
      name: "Remove tag #travel from selected transactions",
    }),
  ).toBeChecked();
  await Promise.all([
    page.waitForResponse(
      (response) =>
        response.url().endsWith("/api/v1/transactions/tags") &&
        response.request().method() === "DELETE",
    ),
    travelOption.click(),
  ]);
  await expect(tagTrigger).not.toContainText("#travel");

  await page.reload();
  await expect(
    page.getByRole("heading", { name: "Detail Market" }),
  ).toBeVisible();
  await expect(fields.getByLabel("Memo")).toHaveValue("Work meal");
  await expect(
    page.getByRole("combobox").filter({ hasText: "Dining" }),
  ).toBeVisible();
  await expect(page.getByRole("heading", { name: "Postings" })).toHaveCount(0);

  await tagTrigger.click();
  await page
    .getByRole("combobox", { name: "Change or add tags..." })
    .fill("Filter");
  const filterOption = page.getByRole("option").filter({ hasText: "#filter" });
  await Promise.all([
    page.waitForResponse(
      (response) =>
        response.url().endsWith("/api/v1/transactions/tags") &&
        response.request().method() === "POST",
    ),
    filterOption.click(),
  ]);

  await page.getByRole("link", { name: "transactions", exact: true }).click();
  await page.getByRole("button", { name: "#filter" }).click();
  await expect(page).toHaveURL(/tag=filter/);
  await expect(page.getByText("Detail Market")).toBeVisible();
  await expect(page.getByText("Other market")).not.toBeVisible();
  await page.reload();
  await expect(page.getByText("Detail Market")).toBeVisible();
  await expect(page.getByText("Other market")).not.toBeVisible();
});

test("income transaction detail shows category and tags", async ({
  page,
}, testInfo) => {
  await login(page, testInfo);
  const checking = await createBucket(page, "asset", "Checking");
  const salary = await createBucket(page, "income", "Salary");
  await importNordea(
    page,
    checking.id,
    nordeaRow("2026/07/01", "1200,00", "Employer"),
  );
  const inboxResponse = await page.request.get("/api/v1/inbox");
  const inbox = (await inboxResponse.json()) as { rows: { id: string }[] };
  const categorized = await page.request.post("/api/v1/inbox/categorize", {
    data: { row_ids: [inbox.rows[0].id], bucket_id: salary.id },
  });
  expect(categorized).toBeOK();

  await page.goto("/transactions");
  await page.getByRole("link", { name: "View Employer" }).click();
  await expect(
    page.getByRole("combobox").filter({ hasText: "Salary" }),
  ).toBeVisible();
  await expect(page.getByRole("combobox", { name: "Tags" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Postings" })).toHaveCount(0);
});

async function setupMatchedTransfer(page: Page, incomingDate = "2026/07/01") {
  const checking = await createBucket(page, "asset", "Checking");
  const savings = await createBucket(page, "asset", "Savings");
  await importNordea(
    page,
    checking.id,
    nordeaRow("2026/07/01", "-50,00", "Transfer out"),
  );
  await importNordea(
    page,
    savings.id,
    nordeaRow(incomingDate, "50,00", "Transfer in"),
  );
  const inboxResponse = await page.request.get("/api/v1/inbox");
  const inbox = (await inboxResponse.json()) as {
    rows: { id: string; counterparty: string }[];
  };
  const outgoing = inbox.rows.find(
    (row) => row.counterparty === "Transfer out",
  )!;
  const incoming = inbox.rows.find(
    (row) => row.counterparty === "Transfer in",
  )!;
  const matched = await page.request.post(
    `/api/v1/inbox/${outgoing.id}/match`,
    {
      data: { match_id: incoming.id },
    },
  );
  expect(matched).toBeOK();

  const response = await page.request.get("/api/v1/transactions");
  const transactions = (await response.json()) as {
    transactions: { id: string; counterparty: string }[];
  };
  return transactions.transactions.find(
    (transaction) => transaction.counterparty === "Transfer out",
  )!;
}

test("transfers on separate days show the account for each row", async ({
  page,
}, testInfo) => {
  await login(page, testInfo);
  await setupMatchedTransfer(page, "2026/07/02");
  await page.goto("/transactions?range=all-time");

  const outgoingRow = page
    .getByRole("link", { name: "View Transfer out" })
    .locator("..");
  const incomingRow = page
    .getByRole("link", { name: "View Transfer in" })
    .locator("..");
  await expect(
    outgoingRow.getByText("Checking", { exact: true }),
  ).toBeVisible();
  await expect(incomingRow.getByText("Savings", { exact: true })).toBeVisible();
});

test("removing one transfer side leaves the counterpart unmatched", async ({
  page,
}, testInfo) => {
  await login(page, testInfo);
  const transaction = await setupMatchedTransfer(page);
  await page.goto("/transactions");
  await expect(page.getByText("Checking → Savings")).toHaveCount(1);
  const outgoingRow = page
    .getByRole("link", { name: "View Transfer out" })
    .locator("..");
  await expect(outgoingRow).toContainText("-€50.00");
  await expect(
    page.getByRole("link", { name: "View Transfer in" }),
  ).toHaveCount(0);
  await page.goto(`/transactions/${transaction.id}`);
  const fields = page.getByRole("region", { name: "Transaction fields" });
  await expect(fields.getByText("Account", { exact: true })).toBeVisible();
  await expect(fields.getByText("Checking", { exact: true })).toBeVisible();
  await expect(fields.getByLabel("Memo")).toBeVisible();
  await expect(page.getByLabel("Category")).toHaveCount(0);
  await expect(page.getByLabel("Tags", { exact: true })).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Postings" })).toHaveCount(0);
  const counterpart = page.getByRole("link", { name: "View counterpart" });
  await expect(counterpart).toBeVisible();
  await counterpart.click();
  await expect(
    page.getByRole("heading", { name: "Transfer in" }),
  ).toBeVisible();
  await page.goto(`/transactions/${transaction.id}`);
  await page.getByRole("button", { name: "Remove this side" }).click();
  await page
    .getByRole("alertdialog")
    .getByRole("button", { name: "Remove this side" })
    .click();
  await expect(page).toHaveURL(/\/transactions$/);

  const response = await page.request.get("/api/v1/transactions");
  const body = (await response.json()) as {
    transactions: { transfer?: { unmatched?: boolean } }[];
  };
  expect(body.transactions).toHaveLength(1);
  expect(body.transactions[0].transfer?.unmatched).toBe(true);
});

test("unmatching a transfer returns both sides to the inbox", async ({
  page,
}, testInfo) => {
  await login(page, testInfo);
  const transaction = await setupMatchedTransfer(page);
  await page.goto(`/transactions/${transaction.id}`);
  await page.getByRole("button", { name: "Unmatch both" }).click();
  await page
    .getByRole("alertdialog")
    .getByRole("button", { name: "Unmatch both" })
    .click();
  await expect(page).toHaveURL(/\/transactions$/);

  const response = await page.request.get("/api/v1/inbox");
  const body = (await response.json()) as { rows: unknown[] };
  expect(body.rows).toHaveLength(2);
});

test("transaction action search keeps previous categories while results load", async ({
  page,
}, testInfo) => {
  await setupTransactions(page, testInfo);
  await selectFailureTransactions(page);
  await page.getByRole("combobox").filter({ hasText: "Actions" }).click();
  const input = page.getByRole("combobox", {
    name: "Filter transaction actions",
  });
  await expect(page.getByRole("option", { name: "Groceries" })).toBeVisible();

  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route("**/api/v1/buckets?*", async (route) => {
    if (new URL(route.request().url()).searchParams.get("q") !== "nothing") {
      await route.continue();
      return;
    }
    await gate;
    await route.continue();
  });

  await input.fill("nothing");
  await expect(page.getByRole("option", { name: "Groceries" })).toBeVisible();
  release();
  await expect(
    page.getByRole("option", { name: "Create expense “nothing”" }),
  ).toBeVisible();
});

test("transaction action keyboard looping preserves scroll padding", async ({
  page,
}, testInfo) => {
  await setupTransactions(page, testInfo);
  await Promise.all(
    Array.from({ length: 16 }, (_, index) =>
      createBucket(
        page,
        "expense",
        `Category ${String(index).padStart(2, "0")}`,
      ),
    ),
  );
  await page.reload();
  await selectFailureTransactions(page);
  await page.getByRole("combobox").filter({ hasText: "Actions" }).click();
  const input = page.getByRole("combobox", {
    name: "Filter transaction actions",
  });
  await expect(page.getByRole("option", { name: "Category 15" })).toBeVisible();

  const scroll = page.locator("[data-transaction-actions-scroll]");
  await input.press("ArrowUp");
  await expect(page.locator("[data-highlighted]")).toContainText(
    "Remove transactions",
  );
  const bottomGap = await scroll.evaluate((element) => {
    const item = element.querySelector<HTMLElement>("[data-highlighted]");
    if (!item) throw new Error("No highlighted action");
    return (
      element.getBoundingClientRect().bottom -
      item.getBoundingClientRect().bottom
    );
  });
  expect(bottomGap).toBeGreaterThanOrEqual(3);

  await input.press("ArrowDown");
  await expect(page.locator("[data-highlighted]")).not.toContainText(
    "Remove transactions",
  );
  const topGap = await scroll.evaluate((element) => {
    const item = element.querySelector<HTMLElement>("[data-highlighted]");
    if (!item) throw new Error("No highlighted action");
    return (
      item.getBoundingClientRect().top - element.getBoundingClientRect().top
    );
  });
  expect(topGap).toBeGreaterThanOrEqual(3);
});

test("failed transaction actions roll back and preserve selection", async ({
  page,
}, testInfo) => {
  await setupTransactions(page, testInfo);
  await selectFailureTransactions(page);
  await page.getByRole("combobox").filter({ hasText: "Actions" }).click();
  const finishFailedCategorize = await holdFailedRequest(
    page,
    "**/api/v1/transactions/categorize",
    "POST",
    async () => {
      await page.getByRole("option", { name: "Dining" }).click();
    },
  );
  await expect(row(page, "Failure one")).toContainText("Dining");
  await expect(row(page, "Failure two")).toContainText("Dining");
  await finishFailedCategorize();
  await expect(row(page, "Failure one")).toContainText("Groceries");
  await expect(row(page, "Failure two")).toContainText("Groceries");
  await expect(page.getByText("2 selected")).toBeVisible();

  await page.getByRole("combobox").filter({ hasText: "Actions" }).click();
  await page.getByRole("option", { name: "Remove transactions" }).click();
  const finishFailedRemoveTransactions = await holdFailedRequest(
    page,
    "**/api/v1/transactions",
    "DELETE",
    async () => {
      await page.getByRole("button", { name: "Remove transactions" }).click();
    },
  );
  await expect(row(page, "Failure one")).not.toBeVisible();
  await expect(row(page, "Failure two")).not.toBeVisible();
  await finishFailedRemoveTransactions();
  await expect(row(page, "Failure one")).toBeVisible();
  await expect(row(page, "Failure two")).toBeVisible();
  await expect(page.getByText("2 selected")).toBeVisible();
});

test("transaction direction filters work without an amount", async ({
  page,
}, testInfo) => {
  await login(page, testInfo);
  const checking = await createBucket(page, "asset", "Checking");
  const groceries = await createBucket(page, "expense", "Groceries");
  const salary = await createBucket(page, "income", "Salary");
  const today = await page.evaluate(() => {
    const date = new Date();
    const year = date.getFullYear();
    const month = String(date.getMonth() + 1).padStart(2, "0");
    const day = String(date.getDate()).padStart(2, "0");
    return `${year}-${month}-${day}`;
  });
  await createTransaction(page, {
    date: today,
    accountId: checking.id,
    categoryId: groceries.id,
    amount: 1200,
    counterparty: "Direction purchase",
  });
  await createTransaction(page, {
    date: today,
    accountId: checking.id,
    categoryId: salary.id,
    amount: 5000,
    counterparty: "Direction salary",
    kind: "income",
  });

  await page.goto("/transactions");
  await page.getByRole("button", { name: "Filters" }).click();
  await page.getByRole("radio", { name: "Out" }).click();
  await expect(page.getByText("Direction purchase")).toBeVisible();
  await expect(page.getByText("Direction salary")).toHaveCount(0);

  await page.getByRole("radio", { name: "In" }).click();
  await expect(page.getByText("Direction salary")).toBeVisible();
  await expect(page.getByText("Direction purchase")).toHaveCount(0);
});
