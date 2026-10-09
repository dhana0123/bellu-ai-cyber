import { withLatency } from './_helpers'
import type { MediaIntegration, MediaItem } from '@/types/addons'

const DEMO_MEDIA: MediaItem[] = [
  {
    id: 'md-1',
    kind: 'audio',
    source: 'inbound-call',
    submitted_by: 'twilio-webhook',
    ts: '2026-10-09T11:30:00Z',
    authenticity_score: 24,
    confidence_pct: 93,
    status: 'flagged',
    segments: [
      { start_s: 4, end_s: 9, label: 'voice synthesis artifact', suspicious: true },
      { start_s: 15, end_s: 18, label: 'spectral discontinuity', suspicious: true },
    ],
  },
  {
    id: 'md-2',
    kind: 'video',
    source: 'zoom-recording',
    submitted_by: 'zoom-integration',
    ts: '2026-10-09T09:10:00Z',
    authenticity_score: 71,
    confidence_pct: 68,
    status: 'queued',
    segments: [{ start_s: 32, end_s: 35, label: 'face-swap boundary', suspicious: true }],
  },
  {
    id: 'md-3',
    kind: 'image',
    source: 'upload',
    submitted_by: 'analyst@demo',
    ts: '2026-10-08T16:45:00Z',
    authenticity_score: 96,
    confidence_pct: 88,
    status: 'approved',
    segments: [],
  },
]

const DEMO_INTEGRATIONS: MediaIntegration[] = [
  { id: 'int-1', name: 'Twilio', status: 'connected', scanned_24h: 142 },
  { id: 'int-2', name: 'Zoom', status: 'connected', scanned_24h: 38 },
  { id: 'int-3', name: 'Teams', status: 'disabled', scanned_24h: 0 },
]

export const mediaMock = {
  queue: () => withLatency(DEMO_MEDIA),
  item: (id: string) => withLatency(DEMO_MEDIA.find((m) => m.id === id) ?? null),
  integrations: () => withLatency(DEMO_INTEGRATIONS),
}
