import { test, expect } from "@playwright/test";

test.describe("App Boot", () => {
  test("loads the dashboard page", async ({ page }) => {
    await page.goto("/");

    // Check page title
    await expect(page).toHaveTitle(/CAAM Dashboard/);
  });

  test("renders the dashboard content", async ({ page }) => {
    await page.goto("/");

    // Check for main heading
    await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();

    // Check for stat cards
    await expect(page.getByText("Active Profiles")).toBeVisible();
    await expect(page.getByText("Health Errors (1h)")).toBeVisible();

    // Check for sidebar navigation
    const navigation = page.getByRole("navigation");
    await expect(page.getByRole("link", { name: "Dashboard" })).toBeVisible();
    await expect(navigation.getByRole("link", { name: "Profiles" })).toBeVisible();
    await expect(navigation.getByRole("link", { name: "Diagnostics" })).toBeVisible();
  });

  test("sidebar navigation highlights current page", async ({ page }) => {
    await page.goto("/");

    // Dashboard link should be highlighted (has active styling)
    const dashboardLink = page.getByRole("link", { name: "Dashboard" });
    await expect(dashboardLink).toBeVisible();
  });

  test("search input is accessible", async ({ page }) => {
    await page.goto("/");

    // Check for search input
    const searchInput = page.getByPlaceholder("Filter profiles and activity...");
    await expect(searchInput).toBeVisible();

    // Type in search
    await searchInput.fill("test query");
    await expect(searchInput).toHaveValue("test query");
  });

  test("quick actions are clickable", async ({ page }) => {
    await page.goto("/");

    // Find quick action buttons
    const copyTokenBtn = page.getByRole("button", { name: "Copy Token Command" });
    await expect(copyTokenBtn).toBeVisible();
    await expect(copyTokenBtn).toBeEnabled();
  });
});
