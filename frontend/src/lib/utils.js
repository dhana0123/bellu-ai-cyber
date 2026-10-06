import { clsx } from 'clsx'
import { twMerge } from 'tailwind-merge'

// cn merges Tailwind class names, resolving conflicts (shadcn convention).
export function cn(...inputs) {
  return twMerge(clsx(inputs))
}
