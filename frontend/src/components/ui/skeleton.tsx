import { cn } from "@/lib/utils"

function Skeleton({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn(
        "relative overflow-hidden rounded-md bg-muted before:absolute before:inset-0 before:-translate-x-full before:animate-[shimmer_1.8s_infinite] before:bg-linear-to-r before:from-transparent before:via-foreground/10 before:to-transparent",
        className
      )}
      {...props}
    />
  )
}

export { Skeleton }
