import { useId } from 'react'
import { Clock, History, Network, Table as TableIcon } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { TIME_RANGE_OPTIONS, type TimeRange } from '../lib/timeRange'

interface TopBarProps {
  viewMode: 'graph' | 'table'
  onViewModeChange: (m: 'graph' | 'table') => void
  nodeCount: number
  edgeCount: number
  showQuery: boolean
  onToggleQuery: (v: boolean) => void
  timeRange: TimeRange
  onTimeRangeChange: (r: TimeRange) => void
}

export function TopBar(props: TopBarProps): JSX.Element {
  const {
    viewMode,
    onViewModeChange,
    nodeCount,
    edgeCount,
    showQuery,
    onToggleQuery,
    timeRange,
    onTimeRangeChange,
  } = props

  const showQueryId = useId()
  const timeRangeId = useId()

  // Rendered inside the shared app header (via HeaderSlot), so this is a single
  // compact horizontal strip with no border/padding of its own. The revealed
  // "generated query" text is rendered separately in the page body (the header
  // is a fixed-height strip), gated by the same `showQuery` state.
  return (
    <div className="flex w-full flex-wrap items-center gap-3">
      {/* Graph/Table visualize toggle (5.1, 5.2, 5.3) */}
      <div
        role="group"
        aria-label="Visualize as"
        className="inline-flex items-center rounded-lg bg-muted p-1"
      >
        <span className="px-2 text-xs text-muted-foreground">Visualize as</span>
        <Button
          type="button"
          size="sm"
          variant={viewMode === 'graph' ? 'secondary' : 'ghost'}
          aria-pressed={viewMode === 'graph'}
          onClick={() => onViewModeChange('graph')}
        >
          <Network aria-hidden="true" />
          Graph
        </Button>
        <Button
          type="button"
          size="sm"
          variant={viewMode === 'table' ? 'secondary' : 'ghost'}
          aria-pressed={viewMode === 'table'}
          onClick={() => onViewModeChange('table')}
        >
          <TableIcon aria-hidden="true" />
          Table
        </Button>
      </div>

      {/* Node / edge count badges bound to passed counts (5.4, 5.5) */}
      <div className="flex items-center gap-2">
        <Badge variant="secondary" aria-label={`${nodeCount} nodes`}>
          {nodeCount.toLocaleString()} nodes
        </Badge>
        <Badge variant="secondary" aria-label={`${edgeCount} edges`}>
          {edgeCount.toLocaleString()} edges
        </Badge>
      </div>

      <div className="ml-auto flex items-center gap-3">
        {/* Time filter: constrain the graph to a recent time window. */}
        <div className="flex items-center gap-2">
          <Clock className="h-4 w-4 text-muted-foreground" aria-hidden="true" />
          <Label htmlFor={timeRangeId} className="sr-only">
            Time range
          </Label>
          <Select value={timeRange} onValueChange={(v) => onTimeRangeChange(v as TimeRange)}>
            <SelectTrigger id={timeRangeId} className="h-8 w-[140px]" aria-label="Time range">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {TIME_RANGE_OPTIONS.map((opt) => (
                <SelectItem key={opt.value} value={opt.value}>
                  {opt.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        {/* Show generated query toggle (5.7, 5.8) */}
        <div className="flex items-center gap-2">
          <Switch
            id={showQueryId}
            checked={showQuery}
            onCheckedChange={onToggleQuery}
          />
          <Label htmlFor={showQueryId} className="hidden lg:inline">
            Show query
          </Label>
        </div>

        {/* History control (5.9) */}
        <Button type="button" size="icon" variant="ghost" aria-label="View history">
          <History aria-hidden="true" />
        </Button>
      </div>
    </div>
  )
}
