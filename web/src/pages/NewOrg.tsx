import { useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { post } from "../lib/api";
import type { Org } from "../lib/types";
import { Button, Card, Input, Label, FieldError, PageTitle } from "../components/ui";

export default function NewOrg() {
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const navigate = useNavigate();
  const qc = useQueryClient();

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const org = await post<Org>("/orgs", { name });
      await qc.invalidateQueries({ queryKey: ["orgs"] });
      navigate(`/orgs/${org.id}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "failed to create organization");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="mx-auto max-w-md">
      <PageTitle>Create an organization</PageTitle>
      <Card>
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
          <Button type="submit" disabled={busy || !name.trim()}>
            Create organization
          </Button>
        </form>
      </Card>
    </div>
  );
}
