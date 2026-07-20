import { useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { post } from "../lib/api";
import type { Org } from "../lib/types";
import { Button, Card, FieldError, Input, Label, useToast } from "../components/ui";

export default function NewOrg() {
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const navigate = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const org = await post<Org>("/orgs", { name });
      await qc.invalidateQueries({ queryKey: ["orgs"] });
      toast("Organization created");
      navigate(`/orgs/${org.id}`);
    } catch (err) {
      const message = err instanceof Error ? err.message : "failed to create organization";
      setError(message);
      toast(message, "error");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="mx-auto mt-16 max-w-md">
      <Card>
        <div className="hazard mb-4 h-1.5 w-[72px] rounded-sm" />
        <h1 className="mb-4 font-display text-xl font-bold tracking-tight">New organization</h1>
        <form onSubmit={submit} className="space-y-4">
          <div>
            <Label htmlFor="org-name">Organization name</Label>
            <Input
              id="org-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Acme Inc"
              required
            />
            <FieldError message={error} />
          </div>
          <Button type="submit" className="w-full" disabled={busy || !name.trim()}>
            Create organization
          </Button>
        </form>
      </Card>
    </div>
  );
}
