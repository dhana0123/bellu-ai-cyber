import * as React from 'react'
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '@/lib/utils'

// Bellu badge variants, preserved exactly from the pre-migration kit — note the
// custom `ai/success/warning/danger` variants (Bellu uses `danger`, not shadcn's
// `destructive`). Severity → variant mapping in pages depends on these names.
const badgeVariants = cva(
  'inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium transition-colors',
  {
    variants: {
      variant: {
        default: 'bg-foreground/10 text-foreground border-foreground/15',
        secondary: 'bg-secondary text-secondary-foreground border-transparent',
        outline: 'text-muted-foreground border-border',
        ai: 'bg-foreground/10 text-foreground border-foreground/20',
        success: 'bg-emerald-500/10 text-emerald-400 border-emerald-500/25',
        warning: 'bg-amber-500/10 text-amber-400 border-amber-500/25',
        danger: 'bg-red-500/10 text-red-400 border-red-500/25',
      },
    },
    defaultVariants: { variant: 'default' },
  },
)

export interface BadgeProps
  extends React.HTMLAttributes<HTMLSpanElement>, VariantProps<typeof badgeVariants> {}

function Badge({ className, variant, ...props }: BadgeProps) {
  return <span className={cn(badgeVariants({ variant }), className)} {...props} />
}

export { Badge, badgeVariants }
