import React from 'react'
import { cn } from '../../lib/utils.js'

// Badge
export function Badge({ className, variant = 'default', ...props }) {
  const variants = {
    default: 'bg-foreground/10 text-foreground border-foreground/15',
    secondary: 'bg-secondary text-secondary-foreground border-transparent',
    outline: 'text-muted-foreground border-border',
    ai: 'bg-foreground/10 text-foreground border-foreground/20',
    success: 'bg-emerald-500/10 text-emerald-400 border-emerald-500/25',
    warning: 'bg-amber-500/10 text-amber-400 border-amber-500/25',
    danger: 'bg-red-500/10 text-red-400 border-red-500/25',
  }
  return (
    <span
      className={cn(
        'inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium',
        variants[variant] || variants.default,
        className,
      )}
      {...props}
    />
  )
}

// Simple table primitives
export const Table = ({ className, ...props }) => (
  <div className="relative w-full overflow-auto">
    <table className={cn('w-full caption-bottom text-sm', className)} {...props} />
  </div>
)
export const THead = (props) => <thead className="[&_tr]:border-b" {...props} />
export const TBody = (props) => <tbody className="[&_tr:last-child]:border-0" {...props} />
export const TR = ({ className, ...props }) => (
  <tr className={cn('border-b transition-colors hover:bg-muted/50', className)} {...props} />
)
export const TH = ({ className, ...props }) => (
  <th className={cn('h-10 px-3 text-left align-middle text-xs font-medium text-muted-foreground', className)} {...props} />
)
export const TD = ({ className, ...props }) => (
  <td className={cn('p-3 align-middle', className)} {...props} />
)

// Spinner / states
export const Spinner = ({ className }) => (
  <div className={cn('h-4 w-4 animate-spin rounded-full border-2 border-muted-foreground/30 border-t-foreground', className)} />
)
