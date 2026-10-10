import { useState } from 'react'
import { BookText, Lightbulb, Play, Video } from 'lucide-react'
import { Card } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { cn } from '@/lib/utils'

const SLIDES = [
  {
    title: 'Learn about Issues',
    body: [
      'Issues represent active vulnerabilities and risks detected across your connected environments, ranked by severity and the context that makes them exploitable.',
      'Policies, Graph Controls, and Threat Detection Rules (TDRs) define how issues are raised and prioritized so you can keep your target environments clean.',
    ],
  },
  {
    title: 'Prioritize by severity',
    body: [
      'Severity combines the raw finding with exposure, exploitability, and blast radius so the most dangerous issues float to the top.',
      'Group and filter to focus on what matters: internet-facing, known-exploited, or production-impacting issues first.',
    ],
  },
  {
    title: 'Resolve and track',
    body: [
      'Assign tickets, track status from Open to Resolved, and watch the trend charts confirm your remediation is working.',
      'Save views to return to the exact slice of issues your team owns.',
    ],
  },
  {
    title: 'Automate with rules',
    body: [
      'Manage Rules to codify what gets flagged, auto-assign owners, and route notifications to the right channel.',
      'Rules keep the backlog honest as your environment changes.',
    ],
  },
]

// Row 3 — interactive onboarding carousel banner.
export function OnboardingCarousel() {
  const [slide, setSlide] = useState(0)
  const [showTips, setShowTips] = useState(true)
  const current = SLIDES[slide]

  return (
    <Card className="overflow-hidden p-0">
      <div className="grid gap-0 md:grid-cols-[1fr_320px]">
        {/* Left — content */}
        <div className="space-y-3 p-5">
          <div className="flex items-start gap-3">
            <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-sky-100 text-sky-600">
              <Lightbulb className="h-5 w-5" />
            </span>
            <div>
              <h2 className="text-sm font-semibold">{current.title}</h2>
            </div>
          </div>
          {current.body.map((p, i) => (
            <p key={i} className="text-sm text-muted-foreground">
              {p}
            </p>
          ))}
          <div className="flex flex-wrap items-center gap-4 pt-1 text-sm">
            <a className="inline-flex items-center gap-1 text-primary hover:underline" href="#">
              <BookText className="h-3.5 w-3.5" /> Issues
            </a>
            <a className="inline-flex items-center gap-1 text-primary hover:underline" href="#">
              <BookText className="h-3.5 w-3.5" /> Policies
            </a>
            <a className="inline-flex items-center gap-1 text-primary hover:underline" href="#">
              <BookText className="h-3.5 w-3.5" /> TDRs
            </a>
            <a className="inline-flex items-center gap-1 text-primary hover:underline" href="#">
              <Video className="h-3.5 w-3.5" /> More videos
            </a>
          </div>
        </div>

        {/* Right — media block */}
        <div className="relative flex min-h-[160px] items-center justify-center bg-blue-600">
          <button
            className="flex h-14 w-14 items-center justify-center rounded-full bg-white/95 text-pink-600 shadow-lg transition-transform hover:scale-105"
            aria-label="Play intro video"
          >
            <Play className="h-6 w-6 translate-x-0.5 fill-current" />
          </button>
        </div>
      </div>

      {/* Bottom control bar — pagination */}
      <div className="flex flex-wrap items-center justify-between gap-3 border-t px-5 py-3">
        <div className="flex items-center gap-3">
          <Button
            variant="outline"
            size="sm"
            disabled={slide === 0}
            onClick={() => setSlide((s) => Math.max(0, s - 1))}
          >
            Back
          </Button>
          <Button
            variant="primary"
            size="sm"
            disabled={slide === SLIDES.length - 1}
            onClick={() => setSlide((s) => Math.min(SLIDES.length - 1, s + 1))}
          >
            Next
          </Button>
          <div className="ml-2 flex items-center gap-1.5">
            {SLIDES.map((_, i) => (
              <button
                key={i}
                aria-label={`Go to slide ${i + 1}`}
                onClick={() => setSlide(i)}
                className={cn(
                  'h-2 w-2 rounded-full transition-colors',
                  i === slide ? 'bg-primary' : 'bg-muted-foreground/30',
                )}
              />
            ))}
            <span className="ml-2 text-xs text-muted-foreground">
              Slide {slide + 1} of {SLIDES.length}
            </span>
          </div>
        </div>

        <label className="flex cursor-pointer items-center gap-2 text-sm text-muted-foreground">
          <Checkbox
            checked={showTips}
            onCheckedChange={(v) => setShowTips(Boolean(v))}
            aria-label="Show help tips on new pages"
          />
          Show help tips on new pages
        </label>
      </div>
    </Card>
  )
}
