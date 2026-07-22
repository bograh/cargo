import { render } from "@testing-library/react";
import { Sparkline } from "./Sparkline";

it("renders a polyline for the given points", () => {
  const { container } = render(<Sparkline points={[0, 5, 2, 8]} width={100} height={20} />);
  const poly = container.querySelector("polyline");
  expect(poly).toBeTruthy();
  // 4 points -> 4 "x,y" pairs
  expect(poly!.getAttribute("points")!.trim().split(" ").length).toBe(4);
});

it("renders nothing meaningful for empty data without crashing", () => {
  const { container } = render(<Sparkline points={[]} width={100} height={20} />);
  expect(container.querySelector("svg")).toBeTruthy();
});
