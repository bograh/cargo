import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ConfirmModal } from "./ConfirmModal";
import { Dropdown, DropdownItem } from "./Dropdown";
import { Modal } from "./Modal";
import { ToastProvider, useToast } from "./Toast";
import { StatusDot } from "./StatusDot";

describe("Modal", () => {
  it("renders title and closes on Escape", async () => {
    const onClose = vi.fn();
    render(<Modal title="Connection URL" onClose={onClose}>body</Modal>);
    expect(screen.getByRole("dialog", { name: "Connection URL" })).toBeInTheDocument();
    await userEvent.keyboard("{Escape}");
    expect(onClose).toHaveBeenCalled();
  });
});

describe("ConfirmModal", () => {
  it("requires typed text before enabling confirm", async () => {
    const onConfirm = vi.fn();
    render(<ConfirmModal title="Delete app" body="gone forever" requireText="my-app" onConfirm={onConfirm} onClose={() => {}} />);
    const confirm = screen.getByRole("button", { name: "Delete" });
    expect(confirm).toBeDisabled();
    await userEvent.type(screen.getByRole("textbox"), "my-app");
    expect(confirm).toBeEnabled();
    await userEvent.click(confirm);
    expect(onConfirm).toHaveBeenCalled();
  });
});

describe("Dropdown", () => {
  it("opens on trigger click and closes on item click", async () => {
    const hit = vi.fn();
    render(
      <Dropdown trigger={<span>menu</span>} label="open menu">
        <DropdownItem onClick={hit}>Log out</DropdownItem>
      </Dropdown>,
    );
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "open menu" }));
    expect(screen.getByRole("menu")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("menuitem", { name: "Log out" }));
    expect(hit).toHaveBeenCalled();
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });
});

describe("Toast", () => {
  function Demo() {
    const toast = useToast();
    return <button onClick={() => toast("Saved", "success")}>push</button>;
  }
  it("shows pushed toast", async () => {
    render(<ToastProvider><Demo /></ToastProvider>);
    await userEvent.click(screen.getByRole("button", { name: "push" }));
    expect(await screen.findByText("Saved")).toBeInTheDocument();
  });
});

describe("StatusDot", () => {
  it("pulses for active statuses", () => {
    const { container } = render(<StatusDot status="building" />);
    expect(container.querySelector(".animate-ping")).toBeTruthy();
  });
  it("is steady for live", () => {
    const { container } = render(<StatusDot status="live" />);
    expect(container.querySelector(".animate-ping")).toBeFalsy();
  });
});
