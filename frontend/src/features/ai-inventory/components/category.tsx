import {
  Bot,
  BrainCircuit,
  Boxes,
  Cloud,
  Container,
  Database,
  KeyRound,
  Network,
  Package,
  Plug,
  Server,
  ShieldAlert,
  GitBranch,
  Workflow,
  Box,
  type LucideIcon,
} from 'lucide-react'
import type { AssetCategory } from '@/types/aiInventory'
// Official open-source brand SVGs, bundled as asset URLs by Vite (no runtime
// third-party fetch). GCP/K8s/Docker from Simple Icons; AWS/Azure from the
// open-source gilbarbara/logos collection. Files in src/assets/providers/.
import awsLogo from '@/assets/providers/aws.svg'
import azureLogo from '@/assets/providers/azure.svg'
import gcpLogo from '@/assets/providers/gcp.svg'
import k8sLogo from '@/assets/providers/k8s.svg'
import dockerLogo from '@/assets/providers/docker.svg'
import githubLogo from '@/assets/providers/github.svg'
import gitlabLogo from '@/assets/providers/gitlab.svg'
import bitbucketLogo from '@/assets/providers/bitbucket.svg'

// Distinct icon + color per asset category for the table's leading icon slot.
const CATEGORY_META: Record<AssetCategory, { icon: typeof Bot; color: string; bg: string }> = {
  agent: { icon: Bot, color: 'text-emerald-500', bg: 'bg-emerald-500/15' },
  model: { icon: BrainCircuit, color: 'text-blue-500', bg: 'bg-blue-500/15' },
  tool: { icon: Plug, color: 'text-amber-500', bg: 'bg-amber-500/15' },
  rag: { icon: Database, color: 'text-violet-500', bg: 'bg-violet-500/15' },
  mcp: { icon: Boxes, color: 'text-sky-500', bg: 'bg-sky-500/15' },
}

// A square icon slot used in the table's Technology column.
export function CategoryIcon({ category }: { category: AssetCategory }) {
  const m = CATEGORY_META[category]
  const Icon = m.icon
  return (
    <span
      className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-md ${m.bg} ${m.color}`}
      aria-hidden
    >
      <Icon className="h-4 w-4" />
    </span>
  )
}

// Distinct icon + color per canonical entity kind, so discovered cloud
// resources (identities, data stores, compute, …) each render with a correct,
// recognizable icon rather than all collapsing to one.
const KIND_META: Record<string, { icon: LucideIcon; color: string; bg: string }> = {
  CloudAccount: { icon: Cloud, color: 'text-slate-400', bg: 'bg-slate-500/15' },
  Identity: { icon: KeyRound, color: 'text-amber-500', bg: 'bg-amber-500/15' },
  DataStore: { icon: Database, color: 'text-violet-500', bg: 'bg-violet-500/15' },
  Compute: { icon: Server, color: 'text-sky-500', bg: 'bg-sky-500/15' },
  Cluster: { icon: Boxes, color: 'text-cyan-500', bg: 'bg-cyan-500/15' },
  Workload: { icon: Workflow, color: 'text-teal-500', bg: 'bg-teal-500/15' },
  ContainerImage: { icon: Container, color: 'text-blue-500', bg: 'bg-blue-500/15' },
  Repository: { icon: GitBranch, color: 'text-fuchsia-500', bg: 'bg-fuchsia-500/15' },
  Package: { icon: Package, color: 'text-orange-500', bg: 'bg-orange-500/15' },
  Vulnerability: { icon: ShieldAlert, color: 'text-red-500', bg: 'bg-red-500/15' },
  AIAgent: { icon: Bot, color: 'text-emerald-500', bg: 'bg-emerald-500/15' },
  AIModel: { icon: BrainCircuit, color: 'text-blue-500', bg: 'bg-blue-500/15' },
  AIService: { icon: BrainCircuit, color: 'text-indigo-500', bg: 'bg-indigo-500/15' },
  Prompt: { icon: Plug, color: 'text-amber-500', bg: 'bg-amber-500/15' },
  MCPServer: { icon: Plug, color: 'text-sky-500', bg: 'bg-sky-500/15' },
  NetworkEndpoint: { icon: Network, color: 'text-rose-500', bg: 'bg-rose-500/15' },
  ExternalService: { icon: Cloud, color: 'text-slate-400', bg: 'bg-slate-500/15' },
}

const KIND_FALLBACK = { icon: Box, color: 'text-muted-foreground', bg: 'bg-muted' }

// KindIcon renders the icon for a canonical entity kind. Used for real
// discovered inventory; falls back to a neutral box for unknown kinds.
export function KindIcon({ kind }: { kind: string }) {
  const m = KIND_META[kind] ?? KIND_FALLBACK
  const Icon = m.icon
  return (
    <span
      className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-md ${m.bg} ${m.color}`}
      aria-hidden
    >
      <Icon className="h-4 w-4" />
    </span>
  )
}

// --- Cloud provider brand marks ---------------------------------------------

const PROVIDER_LOGO: Record<string, string> = {
  aws: awsLogo,
  gcp: gcpLogo,
  google: gcpLogo,
  googlecloud: gcpLogo,
  azure: azureLogo,
  k8s: k8sLogo,
  kubernetes: k8sLogo,
  image: dockerLogo,
  docker: dockerLogo,
  // CI / VCS brand marks.
  github: githubLogo,
  gitlab: gitlabLogo,
  atlassian: bitbucketLogo,
  bitbucket: bitbucketLogo,
}

// ProviderIcon renders the provider's official brand logo for a resource.
// Cloud and CI/VCS providers use bundled brand SVGs; unknown providers fall
// back to a neutral cloud glyph so the slot is never empty.
export function ProviderIcon({ provider, className }: { provider?: string; className?: string }) {
  const cls = className ?? 'h-4 w-4'
  const key = (provider ?? '').toLowerCase()
  const logo = PROVIDER_LOGO[key]
  if (logo) {
    return <img src={logo} alt={`${provider} logo`} className={`${cls} object-contain`} />
  }
  return <Cloud className={cls} />
}
