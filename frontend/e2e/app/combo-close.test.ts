import { expect, test, type Page } from "@playwright/test";

const nordeaHeader = "date,occurred_at,amount,currency,counterparty,note\n";

function nordeaRow(date: string, amount: string, payee: string) {
  return `${date.replaceAll("/", "-")},,${amount.replace(",", ".")},EUR,${payee},\n`;
}

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

async function importRows(page: Page, bucketID: string, csv: string) {
  const response = await page.request.post("/api/v1/imports", {
    multipart: {
      bucket_id: bucketID,
      file: {
        name: "export.csv",
        mimeType: "text/csv",
        buffer: Buffer.from(csv),
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

type Sample = {
  t: number;
  mounted: boolean;
  attrs: string;
  popupOpacity: string;
  posRect: string;
};

test("real app: categorizing a row animates the popup out in place", async ({
  page,
}) => {
  await login(page, `combo-close-${Date.now()}`);
  const checking = await createBucket(page, "asset", "Checking");
  for (const name of ["Restaurants", "Subscriptions", "Electronics"]) {
    await createBucket(page, "expense", name);
  }
  await importRows(
    page,
    checking.id,
    nordeaHeader +
      Array.from({ length: 5 }, (_, i) =>
        nordeaRow("2026/07/01", "-1,00", `Row ${i + 1}`),
      ).join(""),
  );

  await page.goto("/inbox");
  await page.getByRole("button", { name: "Row 2" }).click();
  const popup = page.locator('[aria-label^="Choose category"]');
  await expect(popup).toBeVisible();
  await expect(popup).toHaveCSS("opacity", "1");

  await page.evaluate(() => {
    const samples: Sample[] = [];
    (window as unknown as { __samples: Sample[] }).__samples = samples;
    const t0 = performance.now();
    function tick() {
      const el = document.querySelector<HTMLElement>(
        '[aria-label^="Choose category"]',
      );
      const rect = el?.parentElement?.getBoundingClientRect();
      samples.push({
        t: Math.round(performance.now() - t0),
        mounted: !!el,
        attrs: el
          ? [
              "data-open",
              "data-closed",
              "data-starting-style",
              "data-ending-style",
              "data-anchor-hidden",
            ]
              .filter((a) => el.hasAttribute(a))
              .join(",")
          : "",
        popupOpacity: el ? getComputedStyle(el).opacity : "",
        posRect: rect
          ? [rect.x, rect.y, rect.width, rect.height].map(Math.round).join(",")
          : "",
      });
      if (performance.now() - t0 < 1500) requestAnimationFrame(tick);
    }
    tick();
  });

  await page.getByRole("option", { name: "Restaurants" }).click();
  await page.waitForTimeout(1600);

  const samples = await page.evaluate(
    () => (window as unknown as { __samples: Sample[] }).__samples,
  );
  const ending = samples.filter((s) => s.attrs.includes("data-ending-style"));
  expect(ending.length).toBeGreaterThan(1);
  expect(
    ending.filter((s) => {
      const opacity = Number(s.popupOpacity);
      return opacity > 0.1 && opacity < 0.9;
    }).length,
  ).toBeGreaterThanOrEqual(3);
});
