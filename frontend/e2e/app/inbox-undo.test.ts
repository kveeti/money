import { expect, test, type Page } from "@playwright/test";

test.use({ viewport: { width: 390, height: 844 } });

const nordeaHeader = "date,occurred_at,amount,currency,counterparty,note\n";

async function login(page: Page, user: string) {
  await page.goto("/");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.getByPlaceholder("new-user-sub").fill(user);
  await page.getByRole("button", { name: "Log in as new user" }).click();
  await expect
    .poll(async () => (await page.request.get("/api/v1/buckets")).status())
    .toBe(200);
}

async function createBucket(
  page: Page,
  kind: "asset" | "expense",
  name: string,
) {
  const response = await page.request.post("/api/v1/buckets", {
    data: { kind, name },
  });
  expect(response).toBeOK();
  return (await response.json()) as { id: string };
}

async function importRow(
  page: Page,
  bucketID: string,
  row = "2026-07-01,,-12.34,EUR,Undo shop,\n",
) {
  const response = await page.request.post("/api/v1/imports", {
    multipart: {
      bucket_id: bucketID,
      file: {
        name: "export.csv",
        mimeType: "text/csv",
        buffer: Buffer.from(nordeaHeader + row),
      },
    },
  });
  expect(response).toBeOK();
  const { batch_id: batchID } = (await response.json()) as {
    batch_id: string;
  };
  await expect
    .poll(async () => {
      const report = await page.request.get(`/api/v1/imports/${batchID}`);
      return ((await report.json()) as { status: string }).status;
    })
    .toBe("done");
}

async function categorize(page: Page) {
  await page.getByRole("button", { name: /Undo shop/ }).click();
  await page.getByRole("option", { name: "Groceries" }).click();
  await expect(page.getByRole("button", { name: "Undo" })).toBeVisible();
  await expect(page.getByText("Undo shop")).not.toBeVisible();
}

test("real app: inbox undo restores the imported row", async ({ page }) => {
  await login(page, `inbox-undo-${Date.now()}`);
  const checking = await createBucket(page, "asset", "Checking");
  await createBucket(page, "expense", "Groceries");
  await importRow(page, checking.id);

  await page.goto("/inbox");

  let releaseCategorization!: () => void;
  const categorizationGate = new Promise<void>((resolve) => {
    releaseCategorization = resolve;
  });
  await page.route("**/api/v1/inbox/categorize", async (route) => {
    await categorizationGate;
    await route.continue();
  });
  const categorizationFinished = page.waitForResponse(
    "**/api/v1/inbox/categorize",
  );
  await categorize(page);

  const undoFinished = page.waitForResponse("**/api/v1/inbox/restore");
  await page.getByRole("button", { name: "Undo" }).click();
  await expect(page.getByText("Undo shop")).toBeVisible();
  releaseCategorization();
  await categorizationFinished;
  await undoFinished;
  await page.unroute("**/api/v1/inbox/categorize");
  await page.reload();
  await expect(page.getByText("Undo shop")).toBeVisible();

  await categorize(page);
  await expect(
    page.locator('[aria-label^="Choose category"]'),
  ).not.toBeVisible();
  await expect(page.getByRole("button", { name: "Undo" })).not.toBeVisible({
    timeout: 8000,
  });
  await page.keyboard.press("Meta+z");
  await expect(page.getByText("Undo shop")).toBeVisible();
  await page.reload();
  await expect(page.getByText("Undo shop")).toBeVisible();

  await page.setViewportSize({ width: 1280, height: 800 });
  await page.getByRole("button", { name: /Undo shop/ }).click();
  await page.getByRole("option", { name: "Groceries" }).click();
  await expect(
    page.getByRole("button", { name: "Undo", exact: true }),
  ).not.toBeVisible();
  await expect(
    page.locator('[aria-label^="Choose category"]'),
  ).not.toBeVisible();
  await page.keyboard.press("Meta+z");
  await expect(page.getByText("Undo shop")).toBeVisible();

  await page.setViewportSize({ width: 390, height: 844 });
  await categorize(page);
  await page.goto("/transactions");
  await expect(page.getByText("Undo shop")).toBeVisible();
  await page.getByRole("checkbox", { name: "Select transactions" }).click();
  await page
    .getByRole("listitem")
    .filter({ hasText: "Undo shop" })
    .locator("div")
    .first()
    .click();
  await page.getByRole("combobox").filter({ hasText: "Actions" }).click();
  await page.getByRole("option", { name: "Remove transactions" }).click();
  await page
    .getByRole("button", { name: "Remove transaction", exact: true })
    .click();
  await expect(page.getByText("Undo shop")).not.toBeVisible();

  await page.goto("/inbox");
  await expect(page.getByText("Undo shop")).toBeVisible();
});

test("real app: undo restores selection and confirms before replacing a newer one", async ({
  page,
}) => {
  await login(page, `inbox-selection-undo-${Date.now()}`);
  const checking = await createBucket(page, "asset", "Checking");
  await createBucket(page, "expense", "Groceries");
  await importRow(page, checking.id, "2026-07-01,,-12.34,EUR,Undo one,\n");
  await importRow(page, checking.id, "2026-07-02,,-5.00,EUR,Undo two,\n");
  await importRow(page, checking.id, "2026-07-03,,-2.00,EUR,Undo three,\n");

  await page.goto("/inbox");
  await page.getByRole("checkbox", { name: "Select rows" }).click();
  for (const name of ["Undo one", "Undo two"]) {
    await page
      .getByRole("listitem")
      .filter({ hasText: name })
      .locator("div")
      .first()
      .click();
  }
  await expect(page.getByText("2 selected")).toBeVisible();
  await page.getByRole("combobox").click();
  await page.getByRole("option", { name: "Groceries" }).click();
  await expect(
    page.getByRole("button", { name: "Undo", exact: true }),
  ).toBeVisible();

  await page.getByRole("checkbox", { name: "Select rows" }).click();
  await page
    .getByRole("listitem")
    .filter({ hasText: "Undo three" })
    .locator("div")
    .first()
    .click();
  await page.keyboard.press("Meta+z");

  const dialog = page.getByRole("alertdialog");
  await expect(dialog.getByText("Replace current selection?")).toBeVisible();
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(page.getByText("1 selected")).toBeVisible();

  await page.keyboard.press("Meta+z");
  await dialog.getByRole("button", { name: "Undo" }).click();
  await expect(page.getByText("2 selected")).toBeVisible();
  await expect(page.locator("ul input[type=checkbox]:checked")).toHaveCount(2);
});

test("real app: undo restores both matched rows", async ({ page }) => {
  await login(page, `inbox-match-undo-${Date.now()}`);
  const checking = await createBucket(page, "asset", "Checking");
  const savings = await createBucket(page, "asset", "Savings");
  await importRow(page, checking.id, "2026-07-01,,-500.00,EUR,Transfer out,\n");
  await importRow(page, savings.id, "2026-07-02,,500.00,EUR,Transfer in,\n");

  await page.goto("/inbox");
  await page.getByRole("button", { name: /Transfer out/ }).click();
  await page.getByRole("option", { name: "Match transactions" }).click();
  const matchResponse = page.waitForResponse(
    (response) =>
      response.url().endsWith("/match") &&
      response.request().method() === "POST",
  );
  await page.getByRole("option", { name: /Transfer in/ }).click();
  expect((await matchResponse).ok()).toBe(true);
  const transactions = await page.request.get("/api/v1/transactions");
  await expect(transactions).toBeOK();
  expect(
    ((await transactions.json()) as { transactions: unknown[] }).transactions,
  ).toHaveLength(2);
  await expect(
    page.getByRole("button", { name: /Transfer out/ }),
  ).not.toBeVisible();
  await expect(
    page.getByRole("button", { name: /Transfer in/ }),
  ).not.toBeVisible();

  await page.getByRole("button", { name: "Undo" }).click();
  await expect(
    page.getByRole("button", { name: /Transfer out/ }),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: /Transfer in/ })).toBeVisible();
});
