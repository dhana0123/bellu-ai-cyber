import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

// cn merges Tailwind class names, resolving conflicts (shadcn convention).
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}
