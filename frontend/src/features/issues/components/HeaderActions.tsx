import { ChevronDown, Save, Settings, Wrench, X } from 'lucide-react'
import { Button } from '@/components/ui/button'

// Row 2 — page title + global action buttons.
export function HeaderActions() {
  return (
    <div className="flex flex-wrap items-center justify-between gap-3">
      <h1 className="text-2xl font-semibold tracking-tight">Risk Issues</h1>
      <div className="flex items-center gap-2">
        <Button variant="outline" size="sm">
          <Save className="h-4 w-4" /> Save as… <ChevronDown className="h-3.5 w-3.5" />
        </Button>
        <Button variant="outline" size="sm">
          <Wrench className="h-4 w-4" /> Manage Rules <ChevronDown className="h-3.5 w-3.5" />
        </Button>
        <Button variant="outline" size="icon" className="h-8 w-8" aria-label="Settings">
          <Settings className="h-4 w-4" />
        </Button>
        <Button variant="outline" size="icon" className="h-8 w-8" aria-label="Close">
          <X className="h-4 w-4" />
        </Button>
      </div>
    </div>
  )
}
