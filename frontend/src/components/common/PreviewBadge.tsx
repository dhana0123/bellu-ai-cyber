import { FlaskConical } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'

/**
 * Persistent marker shown on every add-on (Preview) module. Makes it
 * unmistakable that the data is demo-only and not real customer telemetry.
 */
export function PreviewBadge({ className }: { className?: string }) {
  return (
    <Badge
      variant="warning"
      className={cn('gap-1', className)}
      title="This module runs on demo data, not real customer telemetry."
    >
      <FlaskConical className="h-3 w-3" />
      Preview — demo data
    </Badge>
  )
}
