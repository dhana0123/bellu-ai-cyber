import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { ArrowLeft, CheckCircle2, Loader2, ShieldCheck } from 'lucide-react'
import { client } from '@/lib/client'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { ProviderIcon } from '@/features/ai-inventory/components/category'
import { DiscoveryProgress } from './DiscoveryProgress'
import type { CapabilityCard, Connection, ProjectRow, Setup, VerifyResult } from '@/types/foundation'

// Provider presentation metadata for the generic (non-AWS) onboarding flow.
// AWS keeps its own specialized wizard (ConnectAws). Everything here is driven
// by the backend adapter's CreateSetup instructions, so adding a provider needs
// no new UI — only this label map.
const PROVIDER_META: Record<
  string,
  { name: string; category: 'cloud' | 'ci'; accountLabel: string; accountHint: string }
> = {
  gcp: {
    name: 'Google Cloud',
    category: 'cloud',
    accountLabel: 'Project ID',
    accountHint: 'The GCP project id to scan, e.g. my-project-123456.',
  },
  azure: {
    name: 'Microsoft Azure',
    category: 'cloud',
    accountLabel: 'Subscription ID',
    accountHint: 'The Azure subscription id to scan.',
  },
  github: {
    name: 'GitHub',
    category: 'ci',
    accountLabel: 'Organization',
    accountHint: 'Your GitHub organization login. Repos connect via the Bellu GitHub App.',
  },
  gitlab: {
    name: 'GitLab',
    category: 'ci',
    accountLabel: 'Group / namespace',
    accountHint: 'Your GitLab group or namespace path.',
  },
  atlassian: {
    name: 'Atlassian / Bitbucket',
    category: 'ci',
    accountLabel: 'Workspace',
    accountHint: 'Your Bitbucket workspace id.',
  },
}

type Step = 'form' | 'setup' | 'connected'

export default function ConnectProviderPage() {
  const navigate = useNavigate()
  const { provider = '' } = useParams()
  const [params] = useSearchParams()
  const existingId = params.get('id')

  const meta = PROVIDER_META[provider]

  const [caps, setCaps] = useState<CapabilityCard[]>([])
  const [projects, setProjects] = useState<ProjectRow[]>([])
  const [projectId, setProjectId] = useState('')
  const [accountId, setAccountId] = useState('')
  const [capabilities, setCapabilities] = useState<string[]>(['core'])

  const [step, setStep] = useState<Step>('form')
  const [connection, setConnection] = useState<Connection | null>(null)
  const [setup, setSetup] = useState<Setup | null>(null)
  const [verifyResult, setVerifyResult] = useState<VerifyResult | null>(null)
  const [discovering, setDiscovering] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    ;(async () => {
      try {
        const [c, p] = await Promise.all([client.connectorCapabilities(), client.projects()])
        setCaps(c.capabilities ?? [])
        setProjects(p.projects ?? [])
        if (p.projects?.length) setProjectId(String(p.projects[0].id))
      } catch (e) {
        setError((e as Error).message)
      }
      if (existingId) {
        try {
          const { connection } = await client.connector(existingId)
          setConnection(connection)
          setProjectId(String(connection.project_id))
          setAccountId(connection.account_id || '')
          if (connection.capabilities?.length) setCapabilities(connection.capabilities)
          const { setup } = await client.connectorSetup(existingId)
          setSetup(setup)
          setStep(connection.status === 'CONNECTED' || connection.status === 'COMPLETE' ? 'connected' : 'setup')
        } catch {
          /* a fresh connection may not have setup yet */
        }
      }
    })()
  }, [existingId])

  const toggleCap = (name: string) =>
    setCapabilities((cur) =>
      cur.includes(name) ? cur.filter((c) => c !== name) : [...cur, name],
    )

  async function create() {
    if (!meta) return
    setBusy(true)
    setError(null)
    try {
      // Git/CI connectors have no cloud capabilities; send an empty set so no
      // AWS-style capability (core/eks/dspm/…) is persisted on them. Cloud
      // providers always include "core".
      const caps =
        meta.category === 'ci'
          ? []
          : capabilities.includes('core')
            ? capabilities
            : ['core', ...capabilities]
      const { connection } = await client.createConnector({
        project_id: Number(projectId),
        provider,
        scope: 'account',
        account_id: accountId,
        capabilities: caps,
      })
      setConnection(connection)
      const { setup } = await client.connectorSetup(connection.id)
      setSetup(setup)
      setStep('setup')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function verify() {
    if (!connection) return
    setBusy(true)
    setError(null)
    try {
      // Non-AWS providers do not take a role ARN; the backend verifies the
      // connection shape and reports per-capability outcomes.
      const { verification } = await client.verifyConnector(connection.id, '')
      setVerifyResult(verification)
      if (verification.ok) setStep('connected')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function discover() {
    if (!connection) return
    setBusy(true)
    setError(null)
    try {
      await client.discoverConnector(connection.id)
      setDiscovering(true)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const title = useMemo(() => meta?.name ?? provider, [meta, provider])

  if (!meta) {
    return (
      <div className="space-y-4">
        <Button variant="ghost" size="sm" onClick={() => navigate('/connectors')}>
          <ArrowLeft className="h-4 w-4" /> Back to connectors
        </Button>
        <Card>
          <CardContent className="py-10 text-center text-sm text-muted-foreground">
            Unknown provider “{provider}”. Pick a provider from the connectors page.
          </CardContent>
        </Card>
      </div>
    )
  }

  return (
    <div className="mx-auto max-w-2xl space-y-6">
      <Button variant="ghost" size="sm" onClick={() => navigate('/connectors')}>
        <ArrowLeft className="h-4 w-4" /> Back to connectors
      </Button>

      <div className="flex items-center gap-3">
        <ProviderIcon provider={provider} className="h-10 w-10" />
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Connect {title}</h1>
          <p className="text-sm text-muted-foreground capitalize">{meta.category} connector</p>
        </div>
      </div>

      {error && <p className="text-sm text-destructive">{error}</p>}

      {step === 'form' && (
        <Card>
          <CardHeader>
            <CardTitle>Connection details</CardTitle>
            <CardDescription>
              Choose the project this connector belongs to and the account to scan.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-5">
            <div className="space-y-2">
              <Label>Project</Label>
              <Select value={projectId} onValueChange={setProjectId}>
                <SelectTrigger>
                  <SelectValue placeholder="Select a project" />
                </SelectTrigger>
                <SelectContent>
                  {projects.map((p) => (
                    <SelectItem key={p.id} value={String(p.id)}>
                      {p.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            <div className="space-y-2">
              <Label>{meta.accountLabel}</Label>
              <Input
                value={accountId}
                onChange={(e) => setAccountId(e.target.value)}
                placeholder={meta.accountLabel}
              />
              <p className="text-xs text-muted-foreground">{meta.accountHint}</p>
            </div>

            {/* Capabilities are cloud-scanning scopes (IAM/S3/EKS/DSPM/…) and do
                not apply to git/CI connectors, which only connect repositories.
                Hide them for git providers; repository scope is chosen on the
                Repositories page. */}
            {meta.category !== 'ci' && (
              <div className="space-y-2">
                <Label>Capabilities</Label>
                <div className="space-y-2">
                  {caps.map((c) => (
                    <label key={c.name} className="flex items-start gap-2 text-sm">
                      <Checkbox
                        checked={capabilities.includes(c.name) || c.name === 'core'}
                        disabled={c.name === 'core'}
                        onCheckedChange={() => toggleCap(c.name)}
                      />
                      <span>
                        <span className="font-medium">{c.title}</span>
                        <span className="block text-xs text-muted-foreground">
                          {c.description}
                        </span>
                      </span>
                    </label>
                  ))}
                </div>
              </div>
            )}

            <Button onClick={create} disabled={busy || !projectId}>
              {busy && <Loader2 className="h-4 w-4 animate-spin" />} Create connection
            </Button>
          </CardContent>
        </Card>
      )}

      {step === 'setup' && setup && (
        <Card>
          <CardHeader>
            <CardTitle>Set up access</CardTitle>
            <CardDescription>
              Follow these steps in {title}, then verify the connection.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-5">
            <ol className="list-decimal space-y-2 pl-5 text-sm">
              {setup.instructions.map((step, i) => (
                <li key={i}>{step}</li>
              ))}
            </ol>
            {verifyResult && !verifyResult.ok && (
              <div className="rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm">
                <p className="font-medium text-destructive">Verification failed</p>
                <ul className="mt-1 space-y-1 text-xs text-muted-foreground">
                  {(verifyResult.failures ?? []).map((f, i) => (
                    <li key={i}>
                      {f.capability}: {f.detail || f.outcome}
                    </li>
                  ))}
                </ul>
              </div>
            )}
            <div className="flex items-center gap-2">
              <Button onClick={verify} disabled={busy}>
                {busy ? (
                  <Loader2 className="h-4 w-4 animate-spin" />
                ) : (
                  <ShieldCheck className="h-4 w-4" />
                )}
                Verify connection
              </Button>
            </div>
          </CardContent>
        </Card>
      )}

      {step === 'connected' && connection && (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <CheckCircle2 className="h-5 w-5 text-emerald-500" /> Connected
            </CardTitle>
            <CardDescription>
              {title} is connected. Run discovery to collect and map its security posture.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="flex flex-wrap gap-1">
              {(connection.capabilities ?? []).map((c) => (
                <Badge key={c} variant="outline" className="text-[10px]">
                  {c}
                </Badge>
              ))}
            </div>
            {discovering ? (
              <DiscoveryProgress
                connectionId={connection.id}
                onDone={() => navigate('/connectors')}
              />
            ) : (
              <div className="flex items-center gap-2">
                <Button onClick={discover} disabled={busy}>
                  {busy && <Loader2 className="h-4 w-4 animate-spin" />} Run discovery
                </Button>
                <Button variant="outline" onClick={() => navigate('/connectors')}>
                  Done
                </Button>
              </div>
            )}
          </CardContent>
        </Card>
      )}
    </div>
  )
}
