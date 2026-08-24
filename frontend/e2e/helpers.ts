import { expect, type Page, type TestInfo } from "@playwright/test";

export const nordeaHeader =
  "date,occurred_at,amount,currency,counterparty,note\n";

export function nordeaRow(date: string, amount: string, payee: string) {
  return `${date.replaceAll("/", "-")},,${amount.replace(",", ".")},EUR,${payee},\n`;
}

export async function login(page: Page, testInfo: TestInfo, subject?: string) {
  await page.goto("/");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Pick a dev user" }),
  ).toBeVisible();
  const user =
    subject ??
    `${testInfo.file.split("/").at(-1)}-${testInfo.title}-${Date.now()}`
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-");
  await page.getByPlaceholder("new-user-sub").fill(user);
  await page.getByRole("button", { name: "Log in as new user" }).click();
  await expect(page).toHaveURL(/\/transactions$/);
}

export async function createBucket(
  page: Page,
  kind: "asset" | "liability" | "expense" | "income" | "person",
  name: string,
  parentId?: string,
) {
  const response = await page.request.post("/api/v1/buckets", {
    data: { kind, name, ...(parentId ? { parent_id: parentId } : {}) },
  });
  expect(response).toBeOK();
  return (await response.json()) as { id: string };
}

export async function createTransaction(
  page: Page,
  input: {
    date: string;
    accountId: string;
    categoryId: string;
    amount: number;
    counterparty: string;
    kind?: "expense" | "income";
  },
) {
  const beforeResponse = await page.request.get("/api/v1/inbox");
  const before = (await beforeResponse.json()) as { rows: { id: string }[] };
  const beforeIds = new Set(before.rows.map((row) => row.id));
  const sign = input.kind === "income" ? 1 : -1;
  const amount = ((sign * input.amount) / 100).toFixed(2).replace(".", ",");
  await importNordea(
    page,
    input.accountId,
    nordeaRow(input.date.replaceAll("-", "/"), amount, input.counterparty),
  );

  const inboxResponse = await page.request.get("/api/v1/inbox");
  const inbox = (await inboxResponse.json()) as { rows: { id: string }[] };
  const row = inbox.rows.find((item) => !beforeIds.has(item.id));
  expect(row).toBeTruthy();
  const categorized = await page.request.post("/api/v1/inbox/categorize", {
    data: { row_ids: [row!.id], bucket_id: input.categoryId },
  });
  expect(categorized).toBeOK();

  const transactionsResponse = await page.request.get("/api/v1/transactions");
  const transactions = (await transactionsResponse.json()) as {
    transactions: { id: string; counterparty: string }[];
  };
  return transactions.transactions.find(
    (transaction) => transaction.counterparty === input.counterparty,
  )!;
}

export async function importNordea(page: Page, bucketId: string, rows: string) {
  const response = await page.request.post("/api/v1/imports", {
    multipart: {
      bucket_id: bucketId,
      file: {
        name: "transactions.csv",
        mimeType: "text/csv",
        buffer: Buffer.from(nordeaHeader + rows),
      },
    },
  });
  expect(response).toBeOK();
  const result = (await response.json()) as { batch_id: string };
  await expect
    .poll(async () => {
      const report = await page.request.get(
        `/api/v1/imports/${result.batch_id}`,
      );
      return ((await report.json()) as { status: string }).status;
    })
    .toBe("done");
  return result.batch_id;
}
