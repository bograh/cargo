import { Link } from "react-router-dom";
import { Button, EmptyState } from "../components/ui";

export default function NotFound() {
  return (
    <EmptyState
      title="Page not found"
      hint="The page you're looking for doesn't exist or was moved."
      action={
        <Link to="/">
          <Button>Back to dashboard</Button>
        </Link>
      }
    />
  );
}
