import { render, screen } from "@testing-library/react";
import { Button } from "./Button";
import { Badge } from "./Badge";
import { EmptyState } from "./EmptyState";
import { PageHeader } from "./PageHeader";
import { Spinner } from "./Spinner";

describe("ui primitives", () => {
  it("renders primary button with amber styling", () => {
    render(<Button>Deploy</Button>);
    const btn = screen.getByRole("button", { name: "Deploy" });
    expect(btn.className).toContain("bg-amber");
  });

  it("renders badge tones", () => {
    render(<Badge tone="live">live</Badge>);
    expect(screen.getByText("live").className).toContain("text-live");
  });

  it("renders empty state with action", () => {
    render(<EmptyState title="No apps" hint="deploy one" action={<Button>New</Button>} />);
    expect(screen.getByText("No apps")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "New" })).toBeInTheDocument();
  });

  it("renders page header with eyebrow and actions", () => {
    render(<PageHeader eyebrow="org" title="Acme" actions={<Button>New App</Button>} />);
    expect(screen.getByRole("heading", { name: "Acme" })).toBeInTheDocument();
    expect(screen.getByText("org")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "New App" })).toBeInTheDocument();
  });

  it("spinner keeps status role", () => {
    render(<Spinner />);
    expect(screen.getByRole("status")).toBeInTheDocument();
  });
});
