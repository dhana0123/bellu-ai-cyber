// Dev-only kitchen-sink: renders every migrated shadcn primitive to verify the
// token mapping and Radix wiring in both themes. Gated out of production in
// Task 10; not linked from the app nav.
import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { Spinner } from '@/components/ui/spinner'
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from '@/components/ui/sheet'

const BADGE_VARIANTS = [
  'default',
  'secondary',
  'outline',
  'ai',
  'success',
  'warning',
  'danger',
] as const
const BUTTON_VARIANTS = [
  'default',
  'primary',
  'destructive',
  'outline',
  'secondary',
  'ghost',
  'link',
] as const

export default function KitchenSink() {
  const [sel, setSel] = useState('critical')

  return (
    <TooltipProvider>
      <div className="min-h-screen bg-background p-8 text-foreground">
        <h1 className="mb-6 text-2xl font-semibold tracking-tight">shadcn/ui kitchen sink</h1>

        <section className="space-y-3">
          <h2 className="text-sm font-medium text-muted-foreground">Buttons</h2>
          <div className="flex flex-wrap gap-2">
            {BUTTON_VARIANTS.map((v) => (
              <Button key={v} variant={v}>
                {v}
              </Button>
            ))}
            <Button size="sm">sm</Button>
            <Button size="lg">lg</Button>
          </div>
        </section>

        <Separator className="my-6" />

        <section className="space-y-3">
          <h2 className="text-sm font-medium text-muted-foreground">Badges</h2>
          <div className="flex flex-wrap gap-2">
            {BADGE_VARIANTS.map((v) => (
              <Badge key={v} variant={v}>
                {v}
              </Badge>
            ))}
          </div>
        </section>

        <Separator className="my-6" />

        <section className="grid max-w-3xl gap-6 md:grid-cols-2">
          <Card>
            <CardHeader>
              <CardTitle>Card title</CardTitle>
              <CardDescription>Card description, muted.</CardDescription>
            </CardHeader>
            <CardContent className="space-y-3">
              <div className="space-y-1.5">
                <Label htmlFor="k-in">Input + Label</Label>
                <Input id="k-in" placeholder="type here" />
              </div>
              <div className="space-y-1.5">
                <Label>Radix Select</Label>
                <Select value={sel} onValueChange={setSel}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="critical">Critical</SelectItem>
                    <SelectItem value="high">High</SelectItem>
                    <SelectItem value="medium">Medium</SelectItem>
                    <SelectItem value="low">Low</SelectItem>
                  </SelectContent>
                </Select>
              </div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Overlays & feedback</CardTitle>
            </CardHeader>
            <CardContent className="flex flex-wrap items-center gap-2">
              <Dialog>
                <DialogTrigger asChild>
                  <Button variant="outline">Dialog</Button>
                </DialogTrigger>
                <DialogContent>
                  <DialogHeader>
                    <DialogTitle>Dialog title</DialogTitle>
                    <DialogDescription>Radix dialog content.</DialogDescription>
                  </DialogHeader>
                </DialogContent>
              </Dialog>

              <Sheet>
                <SheetTrigger asChild>
                  <Button variant="outline">Sheet</Button>
                </SheetTrigger>
                <SheetContent>
                  <SheetHeader>
                    <SheetTitle>Sheet title</SheetTitle>
                  </SheetHeader>
                </SheetContent>
              </Sheet>

              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button variant="outline">Menu</Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent>
                  <DropdownMenuItem>Item one</DropdownMenuItem>
                  <DropdownMenuItem>Item two</DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>

              <Tooltip>
                <TooltipTrigger asChild>
                  <Button variant="ghost">Hover me</Button>
                </TooltipTrigger>
                <TooltipContent>Tooltip text</TooltipContent>
              </Tooltip>

              <Spinner />
              <Skeleton className="h-8 w-24" />
            </CardContent>
          </Card>
        </section>

        <Separator className="my-6" />

        <section className="max-w-3xl space-y-3">
          <Tabs defaultValue="a">
            <TabsList>
              <TabsTrigger value="a">Tab A</TabsTrigger>
              <TabsTrigger value="b">Tab B</TabsTrigger>
            </TabsList>
            <TabsContent value="a">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Severity</TableHead>
                    <TableHead>Asset</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  <TableRow>
                    <TableCell>
                      <Badge variant="danger">Critical</Badge>
                    </TableCell>
                    <TableCell>DEMO asset</TableCell>
                  </TableRow>
                </TableBody>
              </Table>
            </TabsContent>
            <TabsContent value="b">Tab B content</TabsContent>
          </Tabs>
        </section>
      </div>
    </TooltipProvider>
  )
}
