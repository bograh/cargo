import { render, screen } from "@testing-library/react";
import App from "./App";

test("renders cargo placeholder", () => {
  render(<App />);
  expect(screen.getByRole("heading", { name: /cargo/i })).toBeInTheDocument();
});
