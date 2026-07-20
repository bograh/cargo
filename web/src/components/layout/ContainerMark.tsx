export function ContainerMark({ size = 20, className }: { size?: number; className?: string }) {
  return (
    <svg
      viewBox="0 0 24 24"
      width={size}
      height={size}
      fill="none"
      stroke="var(--color-amber)"
      strokeWidth={1.8}
      className={className}
      aria-hidden="true"
    >
      <rect x="3" y="5" width="18" height="14" rx="1.5" />
      <line x1="7.5" y1="5" x2="7.5" y2="19" />
      <line x1="12" y1="5" x2="12" y2="19" />
      <line x1="16.5" y1="5" x2="16.5" y2="19" />
    </svg>
  );
}
