import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "@/lib/utils"

const badgeVariants = cva(
  "inline-flex items-center rounded-md border px-2.5 py-0.5 text-xs font-semibold transition-colors",
  {
    variants: {
      variant: {
        default:     "border-transparent bg-blue-500/15 text-blue-700 dark:bg-blue-900/35 dark:text-blue-400",
        secondary:   "border-transparent bg-amber-500/15 text-amber-700 dark:bg-amber-900/35 dark:text-amber-400",
        destructive: "border-transparent bg-red-500/15 text-red-600 dark:bg-red-900/30 dark:text-red-400",
        outline:     "border border-border text-muted-foreground",
        success:     "border-transparent bg-emerald-500/15 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-400",
      },
    },
    defaultVariants: { variant: "default" },
  }
)

export interface BadgeProps
  extends React.HTMLAttributes<HTMLDivElement>,
    VariantProps<typeof badgeVariants> {}

function Badge({ className, variant, ...props }: BadgeProps) {
  return <div className={cn(badgeVariants({ variant }), className)} {...props} />
}

export { Badge, badgeVariants }
