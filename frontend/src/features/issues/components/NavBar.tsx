import { Bell, ChevronDown, HelpCircle, Search, Sparkles } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'

// Row 1 — Global workspace navigation bar. Sticky white bar: workspace selector
// on the left, a trial pill + search box + utility icons (with a count badge) on
// the right. Presentational for the demo.
export function NavBar() {
  return (
    <div className="sticky top-0 z-10 -mx-4 mb-4 flex items-center gap-4 border-b bg-card px-4 py-2 md:-mx-6 md:px-6">
      {/* Left — workspace selector */}
      <button className="inline-flex items-center gap-2 rounded-md border px-3 py-1.5 text-sm hover:bg-accent">
        <span className="font-medium">wizdemoext</span>
        <span className="text-muted-foreground">| All projects</span>
        <ChevronDown className="h-4 w-4 text-muted-foreground" />
      </button>

      <div className="flex-1" />

      {/* Right — trial pill, search, utilities */}
      <Badge variant="ai" className="gap-1">
        <Sparkles className="h-3 w-3" /> In Trial
      </Badge>

      <div className="hidden items-center gap-2 rounded-md border px-3 py-1.5 text-sm text-muted-foreground sm:flex">
        <Search className="h-4 w-4" />
        <span>Search</span>
        <kbd className="ml-6 rounded border bg-muted px-1.5 py-0.5 text-[10px] font-medium">
          CTRL K
        </kbd>
      </div>

      <div className="flex items-center gap-1">
        <Button variant="ghost" size="icon" aria-label="Setup assistant">
          <HelpCircle className="h-4 w-4" />
        </Button>
        <div className="relative">
          <Button variant="ghost" size="icon" aria-label="Notifications">
            <Bell className="h-4 w-4" />
          </Button>
          <span className="absolute -right-0.5 -top-0.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-red-500 px-1 text-[10px] font-bold text-white">
            2
          </span>
        </div>
        <div className="ml-1 flex h-8 w-8 items-center justify-center rounded-full bg-primary text-xs font-semibold text-primary-foreground">
          BW
        </div>
      </div>
    </div>
  )
}
