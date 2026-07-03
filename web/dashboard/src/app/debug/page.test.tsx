import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import DebugPage from "./page";

vi.mock("next/navigation", () => ({
  notFound: () => {
    throw new Error("NEXT_NOT_FOUND");
  },
}));

describe("DebugPage", () => {
  afterEach(() => {
    vi.unstubAllEnvs();
  });

  it("renders diagnostics outside production", () => {
    vi.stubEnv("NODE_ENV", "development");

    render(<DebugPage />);

    expect(
      screen.getByRole("heading", { name: "Debug surface" }),
    ).toBeInTheDocument();
    expect(screen.getByText("available-in-dev")).toBeInTheDocument();
    expect(screen.getByText("/debug")).toBeInTheDocument();
  });

  it("returns not found in production", () => {
    vi.stubEnv("NODE_ENV", "production");

    expect(() => DebugPage()).toThrow("NEXT_NOT_FOUND");
  });
});

