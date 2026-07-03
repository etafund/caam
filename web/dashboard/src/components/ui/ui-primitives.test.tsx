import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Badge, Button, Card, CardContent, CardHeader, CardTitle } from ".";

describe("UI primitives", () => {
  it("renders a card with semantic heading/content composition", () => {
    render(
      <Card>
        <CardHeader>
          <CardTitle>Profiles</CardTitle>
        </CardHeader>
        <CardContent>12 active profiles</CardContent>
      </Card>,
    );

    expect(
      screen.getByRole("heading", { name: "Profiles" }),
    ).toBeInTheDocument();
    expect(screen.getByText("12 active profiles")).toBeInTheDocument();
  });

  it("renders quiet operational buttons with explicit button type", () => {
    render(<Button>Sync Now</Button>);

    const button = screen.getByRole("button", { name: "Sync Now" });
    expect(button).toHaveAttribute("type", "button");
    expect(button).toHaveClass("rounded-lg");
  });

  it("renders status badges with stable tone classes", () => {
    render(<Badge tone="success">Healthy</Badge>);

    const badge = screen.getByText("Healthy");
    expect(badge).toHaveClass("bg-success/10");
    expect(badge).toHaveClass("text-success");
  });
});

