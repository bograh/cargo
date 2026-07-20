import { useState, type ReactNode } from "react";
import { Button } from "./Button";
import { Input } from "./Input";
import { Label } from "./Label";
import { Modal } from "./Modal";

export function ConfirmModal({
  title,
  body,
  confirmLabel = "Delete",
  requireText,
  busy,
  onConfirm,
  onClose,
}: {
  title: string;
  body: ReactNode;
  confirmLabel?: string;
  requireText?: string;
  busy?: boolean;
  onConfirm: () => void;
  onClose: () => void;
}) {
  const [typed, setTyped] = useState("");
  const ok = !requireText || typed === requireText;
  return (
    <Modal title={title} onClose={onClose}>
      <div className="text-sm text-muted">{body}</div>
      {requireText && (
        <div className="mt-3">
          <Label htmlFor="confirm-typed">
            Type <span className="font-mono text-text">{requireText}</span> to confirm
          </Label>
          <Input id="confirm-typed" value={typed} onChange={(e) => setTyped(e.target.value)} />
        </div>
      )}
      <div className="mt-5 flex justify-end gap-2">
        <Button variant="ghost" onClick={onClose}>
          Cancel
        </Button>
        <Button variant="danger" disabled={!ok || busy} onClick={onConfirm}>
          {confirmLabel}
        </Button>
      </div>
    </Modal>
  );
}
