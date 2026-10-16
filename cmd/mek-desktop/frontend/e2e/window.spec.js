import { expect, test } from "@playwright/test";

// What the stub mek ran since the last reset, one command per line.
async function ran(request) {
  const text = await (await request.get("/e2e/log")).text();
  return text.trim().split("\n").filter(Boolean);
}

test.beforeEach(async ({ page, request }) => {
  await request.post("/e2e/reset");
  await page.goto("/");
  await expect(page.locator("aside li")).toHaveCount(3);
});

test("lists the contexts, and uses one", async ({ page, request }) => {
  const sidebar = page.locator("aside");
  await expect(sidebar.getByText("protected")).toBeVisible();
  await expect(sidebar.getByText("readonly")).toBeVisible();
  await sidebar.getByRole("button", { name: /prod/ }).click();
  await expect(page.locator("main h1")).toContainText("prod");
  await page.getByRole("button", { name: "Use", exact: true }).click();
  await expect(page.locator("footer")).toContainText("✓ now using prod");
  expect(await ran(request)).toEqual(["use prod"]);
});

test("starts a tunnel: a protected context asks first", async ({ page, request }) => {
  const row = (ctx) => page.locator("tbody tr", { hasText: ctx }).filter({ hasText: "not running" });
  await row("dev").getByRole("button", { name: "Start" }).click();
  await expect(page.locator("footer")).toContainText("✓ start tunnel db (dev)");

  await row("prod").getByRole("button", { name: "Start" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("prod is protected — start tunnel db?");
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(page.locator("footer")).toContainText("cancelled: start tunnel db (prod)");

  await row("prod").getByRole("button", { name: "Start" }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Yes" }).click();
  await expect(page.locator("footer")).toContainText("✓ start tunnel db (prod)");
  expect(await ran(request)).toEqual(["-c dev tunnel db -b", "--yes -c prod tunnel db -b"]);
});

test("lists resources, shows one, and acts on it through the guard", async ({ page, request }) => {
  await page.locator("aside").getByRole("button", { name: /dev/ }).click();
  await page.locator("nav").getByRole("button", { name: "Resources" }).click();
  await page.getByPlaceholder(/Filter/).fill("instances");
  await page.getByRole("button", { name: "ec2 instances", exact: true }).click();
  await expect(page.locator("tbody")).toContainText("i-0aaa");
  await expect(page.locator("tbody")).toContainText("web");
  await page.locator("tbody tr", { hasText: "i-0aaa" }).click();
  await expect(page.locator(".detail pre")).toContainText('"InstanceType": "t3.micro"');
  await page.waitForTimeout(4500); // two refreshes of the window: the tab keeps its place
  await expect(page.locator(".detail pre")).toContainText('"InstanceType": "t3.micro"');

  await page.locator(".detail").getByRole("button", { name: "stop-instances" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("destructive: aws ec2 stop-instances --instance-ids i-0aaa?");
  await dialog.getByRole("button", { name: "Yes" }).click();
  await expect(page.locator("footer")).toContainText("✓ stop-instances web (dev)");

  expect(await ran(request)).toEqual([
    "-c dev aws ec2 describe-instances --output json --max-items 100",
    "-c dev aws ec2 describe-instances --instance-ids i-0aaa --output json",
    "--yes -c dev aws ec2 stop-instances --instance-ids i-0aaa",
    "-c dev aws ec2 describe-instances --output json --max-items 100", // listed again
  ]);
});

test("a destructive action on a protected context needs its name typed", async ({ page, request }) => {
  await page.locator("aside").getByRole("button", { name: /prod/ }).click();
  await page.locator("nav").getByRole("button", { name: "Resources" }).click();
  await page.getByPlaceholder(/Filter/).fill("instances");
  await page.getByRole("button", { name: "ec2 instances", exact: true }).click();
  await page.locator("tbody tr", { hasText: "i-0bbb" }).click();
  await page.locator(".detail").getByRole("button", { name: "terminate-instances" }).click();

  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("prod is protected — destructive: aws ec2 terminate-instances --instance-ids i-0bbb");
  const run = dialog.getByRole("button", { name: "Run" });
  await expect(run).toBeDisabled();
  await dialog.getByRole("textbox").fill("pro");
  await expect(run).toBeDisabled();
  await dialog.getByRole("textbox").fill("prod");
  await run.click();
  await expect(page.locator("footer")).toContainText("✓ terminate-instances i-0bbb (prod)");
  expect(await ran(request)).toContain("--confirm prod -c prod aws ec2 terminate-instances --instance-ids i-0bbb");
});

test("searches and filters the audit log", async ({ page }) => {
  await page.locator("nav").getByRole("button", { name: "Audit" }).click();
  const rows = page.locator("tbody tr");
  await expect(rows).toHaveCount(2);
  await page.getByLabel("blocked / declined only").check();
  await expect(rows).toHaveCount(1);
  await expect(rows.first()).toContainText("aws s3 rb s3://old");
  await page.getByLabel("blocked / declined only").uncheck();
  await page.getByPlaceholder(/Search/).fill("s3 ls");
  await expect(rows).toHaveCount(1);
  await expect(rows.first()).toContainText("dev");
});

test("removing a context asks first; cancelled, nothing runs", async ({ page, request }) => {
  await page.getByRole("button", { name: "Remove" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("Remove context dev from its file?");
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(dialog).toBeHidden();
  expect(await ran(request)).toEqual([]);
});
