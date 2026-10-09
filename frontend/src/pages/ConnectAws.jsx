import React, { useEffect, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import {
  ArrowLeft, Copy, Check, Download, ExternalLink, Loader2, Info, BookOpen,
} from 'lucide-react'
import { api } from '../api.js'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '../components/ui/card.jsx'
import { Input, Label } from '../components/ui/input.jsx'
import { Button } from '../components/ui/button.jsx'
import { Badge, Spinner } from '../components/ui/misc.jsx'

// Wizard panes. The left sidebar shows two high-level steps (Connection,
// Details); "Connection" holds the configure form, "Details" holds the
// deploy -> verify -> connected sub-flow that follows setup generation.
const PANE = { CONNECTION: 'connection', DETAILS: 'details' }
const SUB = { DEPLOY: 'deploy', VERIFY: 'verify', CONNECTED: 'connected' }

// ---- small presentational helpers -------------------------------------------

function InfoDot({ title }) {
  return <Info className="inline h-3.5 w-3.5 text-muted-foreground" title={title} />
}

function CopyButton({ text, label = 'Copy' }) {
  const [done, setDone] = useState(false)
  return (
    <Button
      variant="outline"
      size="sm"
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(text)
          setDone(true)
          setTimeout(() => setDone(false), 1500)
        } catch {
          /* clipboard may be unavailable */
        }
      }}
    >
      {done ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
      {done ? 'Copied' : label}
    </Button>
  )
}

function download(name, content) {
  const blob = new Blob([content], { type: 'text/plain' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = name
  a.click()
  URL.revokeObjectURL(url)
}

// RadioRow renders a single selectable radio option with an optional badge.
function RadioRow({ selected, onClick, label, recommended, badge, disabled }) {
  return (
    <button
      type="button"
      onClick={disabled ? undefined : onClick}
      disabled={disabled}
      className={
        'flex w-full items-center gap-3 rounded-md px-1 py-1.5 text-left text-sm transition-colors ' +
        (disabled ? 'cursor-not-allowed opacity-50' : 'hover:text-foreground')
      }
    >
      <span
        className={
          'flex h-4 w-4 shrink-0 items-center justify-center rounded-full border ' +
          (selected ? 'border-primary' : 'border-muted-foreground/40')
        }
      >
        {selected && <span className="h-2 w-2 rounded-full bg-primary" />}
      </span>
      <span className={selected ? 'text-foreground' : 'text-muted-foreground'}>
        {label}
        {recommended && <span className="ml-1 text-primary">(Recommended)</span>}
      </span>
      {badge && <Badge variant="outline" className="ml-1">{badge}</Badge>}
    </button>
  )
}

// ---- the page ---------------------------------------------------------------

export default function ConnectAwsPage() {
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const existingId = params.get('id')

  const [pane, setPane] = useState(PANE.CONNECTION)
  const [sub, setSub] = useState(SUB.DEPLOY)
  const [error, setError] = useState(null)
  const [busy, setBusy] = useState(false)

  const [caps, setCaps] = useState([])
  const [projects, setProjects] = useState([])
  const [form, setForm] = useState({
    project_id: '',
    scope: 'organization',
    installation_type: 'standard',
    deployment_method: 'cloudformation',
    account_id: '',
    region: 'us-east-1',
    capabilities: ['core'],
  })

  const [connection, setConnection] = useState(null)
  const [setup, setSetup] = useState(null)
  const [roleArn, setRoleArn] = useState('')
  const [verifyResult, setVerifyResult] = useState(null)
  const [members, setMembers] = useState([])
  const [expanding, setExpanding] = useState(false)

  useEffect(() => {
    ;(async () => {
      try {
        const [c, p] = await Promise.all([api.connectorCapabilities(), api.projects()])
        setCaps(c.capabilities || [])
        setProjects(p.projects || [])
        if (p.projects?.length) setForm((f) => ({ ...f, project_id: String(p.projects[0].id) }))
      } catch (e) {
        setError(e.message)
      }
      if (existingId) {
        try {
          const { connection } = await api.connector(existingId)
          setConnection(connection)
          setRoleArn(connection.role_arn || '')
          setPane(PANE.DETAILS)
          if (connection.status === 'CONNECTED' || connection.status === 'COMPLETE') setSub(SUB.CONNECTED)
          else if (connection.status === 'VERIFYING' || connection.status === 'ERROR') setSub(SUB.VERIFY)
          else setSub(SUB.DEPLOY)
          try {
            const s = await api.connectorSetup(existingId)
            setSetup(s.setup)
          } catch {
            /* optional */
          }
        } catch (e) {
          setError(e.message)
        }
      }
    })()
  }, [existingId])

  function toggleCap(name) {
    setForm((f) => {
      if (name === 'core') return f // core is mandatory
      const has = f.capabilities.includes(name)
      return { ...f, capabilities: has ? f.capabilities.filter((c) => c !== name) : [...f.capabilities, name] }
    })
  }

  async function createAndSetup(e) {
    e?.preventDefault()
    setBusy(true)
    setError(null)
    try {
      // Only setup choices are sent. No account, management account, or OU id:
      // the account is derived from the Role ARN at verify, and StackSet
      // deployment targets are chosen in AWS. The project defaults to the
      // user's first project and the region to us-east-1.
      if (!form.project_id) throw new Error('Create a project first (Administration → Projects).')
      const payload = {
        project_id: Number(form.project_id),
        provider: 'aws',
        scope: form.scope,
        installation_type: form.installation_type,
        deployment_method: form.deployment_method,
        region: form.region,
        capabilities: form.capabilities,
      }
      const { connection } = await api.createConnector(payload)
      setConnection(connection)
      const { setup } = await api.connectorSetup(connection.id)
      setSetup(setup)
      setPane(PANE.DETAILS)
      setSub(SUB.DEPLOY)
    } catch (e) {
      setError(e.message)
    } finally {
      setBusy(false)
    }
  }

  async function verify() {
    setBusy(true)
    setError(null)
    setVerifyResult(null)
    try {
      const { verification } = await api.verifyConnector(connection.id, roleArn.trim())
      setVerifyResult(verification)
      const { connection: fresh } = await api.connector(connection.id)
      setConnection(fresh)
      if (verification?.ok) {
        setSub(SUB.CONNECTED)
        if (fresh.scope === 'organization') expandAccounts()
      }
    } catch (e) {
      setError(e.message)
    } finally {
      setBusy(false)
    }
  }

  async function expandAccounts() {
    setExpanding(true)
    setError(null)
    try {
      const { connections } = await api.expandConnector(connection.id, [])
      setMembers(connections || [])
    } catch (e) {
      setError(e.message)
    } finally {
      setExpanding(false)
    }
  }

  async function startDiscovery() {
    setBusy(true)
    setError(null)
    try {
      await api.discoverConnector(connection.id)
      const { connection: fresh } = await api.connector(connection.id)
      setConnection(fresh)
    } catch (e) {
      setError(e.message)
    } finally {
      setBusy(false)
    }
  }

  // Sidebar step state: step 1 active on CONNECTION; step 2 active on DETAILS.
  const stepItems = [
    { n: 1, label: 'Connection', active: pane === PANE.CONNECTION, done: pane === PANE.DETAILS },
    { n: 2, label: 'Details', active: pane === PANE.DETAILS, done: false },
  ]

  return (
    <div className="space-y-5">
      {/* Header */}
      <div className="flex items-start justify-between">
        <div className="flex items-start gap-3">
          <button
            onClick={() => navigate('/connectors')}
            className="mt-1 text-muted-foreground hover:text-foreground"
            title="Back to Cloud Connectors"
          >
            <ArrowLeft className="h-5 w-5" />
          </button>
          <div>
            <h1 className="text-2xl font-semibold tracking-tight">
              Connect to Amazon Web Services (AWS)
            </h1>
            <p className="text-sm text-muted-foreground">
              Connect your AWS to gain visibility into your subscriptions security.
            </p>
          </div>
        </div>
        <Badge variant="outline" className="mt-1 text-xs">AWS</Badge>
      </div>

      <div className="grid grid-cols-[200px_1fr] gap-6">
        {/* Left sidebar stepper */}
        <aside className="space-y-1">
          {stepItems.map((s) => (
            <div
              key={s.n}
              className={
                'flex items-center gap-3 rounded-md px-3 py-2 text-sm ' +
                (s.active ? 'bg-accent font-medium text-foreground' : 'text-muted-foreground')
              }
            >
              <span
                className={
                  'flex h-6 w-6 items-center justify-center rounded-full text-xs ' +
                  (s.done
                    ? 'bg-emerald-500 text-white'
                    : s.active
                      ? 'bg-primary text-primary-foreground'
                      : 'border border-muted-foreground/40')
                }
              >
                {s.done ? <Check className="h-3.5 w-3.5" /> : s.n}
              </span>
              {s.label}
            </div>
          ))}
        </aside>

        {/* Main form */}
        <div className="min-w-0 space-y-6">
          {error && (
            <div className="rounded-md border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive">
              {error}
            </div>
          )}

          {pane === PANE.CONNECTION && (
            <form onSubmit={createAndSetup} className="space-y-7">
              <a
                className="inline-flex items-center gap-1.5 text-sm text-primary hover:underline"
                href="#"
                onClick={(e) => e.preventDefault()}
              >
                <BookOpen className="h-3.5 w-3.5" /> How to connect to AWS
              </a>

              {/* Section A: Choose Your Setup */}
              <section className="space-y-5">
                <h2 className="text-lg font-semibold">Choose Your Setup</h2>

                <div className="space-y-2">
                  <div className="text-sm font-medium">Connector Scope</div>
                  <p className="text-xs text-muted-foreground">
                    Configure the Connector to scan your AWS organization and its member accounts, or have it
                    scan a single account.
                  </p>
                  <div className="pt-1">
                    <RadioRow
                      selected={form.scope === 'organization'}
                      onClick={() => setForm({ ...form, scope: 'organization' })}
                      label="Organization"
                      recommended
                    />
                    <RadioRow
                      selected={form.scope === 'account'}
                      onClick={() => setForm({ ...form, scope: 'account' })}
                      label="Account"
                    />
                  </div>
                </div>

                <div className="space-y-2">
                  <div className="text-sm font-medium">Installation Type</div>
                  <div className="pt-1">
                    <RadioRow
                      selected={form.installation_type === 'standard'}
                      onClick={() => setForm({ ...form, installation_type: 'standard' })}
                      label="Standard"
                    />
                    <RadioRow selected={false} disabled label="Bellu Outpost" badge="Coming soon" />
                  </div>
                </div>
              </section>

              {/* Section B: Deploy */}
              <section className="space-y-5">
                <h2 className="text-lg font-semibold">Deploy</h2>

                <div className="space-y-2">
                  <div className="text-sm font-medium">Deployment Method</div>
                  <div className="pt-1">
                    <RadioRow
                      selected={form.deployment_method === 'cloudformation'}
                      onClick={() => setForm({ ...form, deployment_method: 'cloudformation' })}
                      label="CloudFormation"
                    />
                    <RadioRow
                      selected={form.deployment_method === 'terraform'}
                      onClick={() => setForm({ ...form, deployment_method: 'terraform' })}
                      label="Terraform"
                    />
                    <RadioRow
                      selected={form.deployment_method === 'manual'}
                      onClick={() => setForm({ ...form, deployment_method: 'manual' })}
                      label="Manual"
                    />
                  </div>
                </div>

              </section>

              {/* Section C: Permissions */}
              <section className="space-y-3">
                <h2 className="text-lg font-semibold">Permissions</h2>
                <div className="space-y-4">
                  {caps.map((cap) => {
                    const checked = form.capabilities.includes(cap.name)
                    const mandatory = cap.name === 'core'
                    return (
                      <label
                        key={cap.name}
                        className={'flex gap-3 ' + (mandatory ? 'opacity-90' : 'cursor-pointer')}
                      >
                        <input
                          type="checkbox"
                          checked={checked}
                          disabled={mandatory}
                          onChange={() => toggleCap(cap.name)}
                          className="mt-1 h-4 w-4 accent-[hsl(var(--primary))]"
                        />
                        <div>
                          <div className="flex items-center gap-1.5 text-sm font-medium">
                            {mandatory ? cap.title : `Add ${cap.title}`}
                            <InfoDot title={cap.description} />
                            {mandatory && <Badge variant="outline">Required</Badge>}
                          </div>
                          <p className="text-xs text-muted-foreground">{cap.description}</p>
                        </div>
                      </label>
                    )
                  })}
                </div>
              </section>

              <div className="flex justify-end border-t border-border pt-4">
                <Button type="submit" disabled={busy}>
                  {busy && <Loader2 className="h-4 w-4 animate-spin" />}
                  Generate Setup
                </Button>
              </div>
            </form>
          )}

          {pane === PANE.DETAILS && (
            <div className="space-y-5">
              {sub === SUB.DEPLOY && setup && (
                <DeployStep setup={setup} onNext={() => setSub(SUB.VERIFY)} onDownload={download} />
              )}

              {sub === SUB.VERIFY && (
                <Card>
                  <CardHeader>
                    <CardTitle className="text-base">Verify Connection</CardTitle>
                    <CardDescription>
                      After the role is created in AWS, paste its ARN. Bellu assumes it via STS to confirm access.
                    </CardDescription>
                  </CardHeader>
                  <CardContent className="space-y-4">
                    <div className="space-y-1.5">
                      <Label>Role ARN</Label>
                      <Input
                        value={roleArn}
                        onChange={(e) => setRoleArn(e.target.value)}
                        placeholder="arn:aws:iam::123456789012:role/BelluConnectorRole"
                      />
                    </div>
                    {setup?.external_id && (
                      <div className="text-xs text-muted-foreground">
                        External ID: <span className="font-mono">{setup.external_id}</span> (read-only, bound to this connection)
                      </div>
                    )}

                    {verifyResult && (
                      <div className="space-y-1.5">
                        <div className="text-sm font-medium">Permissions</div>
                        {(verifyResult.capabilities || []).map((cap) => (
                          <div
                            key={cap.capability}
                            className="flex items-center justify-between rounded-md border px-3 py-2 text-sm"
                          >
                            <span className="capitalize">{cap.capability}</span>
                            <Badge
                              variant={
                                cap.outcome === 'PASS' ? 'success' : cap.outcome === 'NOT_ENABLED' ? 'outline' : 'danger'
                              }
                            >
                              {cap.outcome === 'PASS'
                                ? 'Connected'
                                : cap.outcome === 'NOT_ENABLED'
                                  ? 'Not enabled'
                                  : 'Missing permissions'}
                            </Badge>
                          </div>
                        ))}
                      </div>
                    )}

                    <div className="flex justify-end gap-2">
                      <Button variant="outline" onClick={() => setSub(SUB.DEPLOY)}>Back</Button>
                      <Button onClick={verify} disabled={busy || !roleArn.trim()}>
                        {busy && <Loader2 className="h-4 w-4 animate-spin" />}
                        Verify Connection
                      </Button>
                    </div>
                  </CardContent>
                </Card>
              )}

              {sub === SUB.CONNECTED && (
                <Card>
                  <CardHeader>
                    <CardTitle className="text-base">Connected</CardTitle>
                    <CardDescription>Bellu can now assume the role and collect AWS data.</CardDescription>
                  </CardHeader>
                  <CardContent className="space-y-4">
                    <div className="flex items-center gap-2 text-sm">
                      <Check className="h-4 w-4 text-emerald-500" /> STS authentication successful
                    </div>
                    <div className="text-sm">
                      Status: <Badge variant="success">{connection?.status}</Badge>
                    </div>

                    {connection?.scope === 'organization' && (
                      <div className="space-y-2">
                        <div className="flex items-center justify-between">
                          <div className="text-sm font-medium">Member accounts</div>
                          <Button variant="outline" size="sm" onClick={expandAccounts} disabled={expanding}>
                            {expanding && <Loader2 className="h-3.5 w-3.5 animate-spin" />} Refresh
                          </Button>
                        </div>
                        {members.length === 0 ? (
                          <p className="text-xs text-muted-foreground">
                            {expanding ? 'Finding accounts…' : 'No member accounts found yet. The StackSet may still be deploying — Refresh in a minute.'}
                          </p>
                        ) : (
                          <div className="divide-y rounded-md border">
                            {members.map((mconn) => (
                              <div key={mconn.id} className="flex items-center justify-between px-3 py-2 text-sm">
                                <span className="font-mono">{mconn.account_id}</span>
                                <Badge variant={mconn.status === 'CONNECTED' || mconn.status === 'COMPLETE' ? 'success' : mconn.status === 'ERROR' ? 'danger' : 'outline'}>
                                  {mconn.status}
                                </Badge>
                              </div>
                            ))}
                          </div>
                        )}
                      </div>
                    )}

                    <div className="flex justify-end gap-2">
                      <Button variant="outline" onClick={() => navigate('/connectors')}>Done</Button>
                      <Button onClick={startDiscovery} disabled={busy}>
                        {busy && <Loader2 className="h-4 w-4 animate-spin" />}
                        Start Discovery
                      </Button>
                    </div>
                  </CardContent>
                </Card>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

// DeployStep renders the method-specific deployment instructions.
function DeployStep({ setup, onNext, onDownload }) {
  const method = setup.deployment_method
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Deployment Instructions</CardTitle>
        <CardDescription>
          Deploy the Bellu role in your AWS account, then continue to verify. External ID:{' '}
          <span className="font-mono">{setup.external_id}</span>
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {method === 'cloudformation' && setup.scope === 'organization' && (
          <div className="rounded-md border border-amber-500/30 bg-amber-500/5 p-3 text-sm">
            <div className="mb-1 font-medium">Before you start (one time)</div>
            <ul className="list-disc space-y-0.5 pl-5 text-muted-foreground">
              <li>Sign in to your AWS Organization management account.</li>
              <li>In AWS Organizations → Services → CloudFormation StackSets, enable trusted access.</li>
            </ul>
          </div>
        )}

        {method === 'cloudformation' && (
          <div className="rounded-lg border p-4">
            <div className="font-medium">AWS CloudFormation</div>
            <p className="mb-3 text-sm text-muted-foreground">
              {setup.scope === 'organization'
                ? 'Bellu will create the role in your management account and, via a StackSet, in every member account. On the AWS page, enter your organization root ID (r-xxxx) or OU IDs.'
                : 'Bellu will create the required IAM role and permissions in your AWS account.'}
            </p>
            <div className="flex flex-wrap gap-2">
              {setup.launch_url ? (
                <a href={setup.launch_url} target="_blank" rel="noreferrer">
                  <Button size="sm">
                    <ExternalLink className="h-3.5 w-3.5" /> Launch CloudFormation
                  </Button>
                </a>
              ) : (
                <span className="text-xs text-muted-foreground">
                  One-click launch isn't configured on this server. Download the template and upload it under
                  CloudFormation → Create stack.
                </span>
              )}
              <CopyButton text={setup.template} label="Copy template" />
              <Button variant="outline" size="sm" onClick={() => onDownload('bellu-connector.yaml', setup.template)}>
                <Download className="h-3.5 w-3.5" /> Download template
              </Button>
            </div>
          </div>
        )}

        {method === 'terraform' && (
          <div className="space-y-2">
            <div className="font-medium">Terraform</div>
            {(setup.files || []).map((f) => (
              <div key={f.name} className="flex items-center justify-between rounded-md border px-3 py-2 text-sm">
                <span className="font-mono">{f.name}</span>
                <div className="flex gap-2">
                  <CopyButton text={f.content} />
                  <Button variant="outline" size="sm" onClick={() => onDownload(f.name, f.content)}>
                    <Download className="h-3.5 w-3.5" /> Download
                  </Button>
                </div>
              </div>
            ))}
          </div>
        )}

        {method === 'manual' && (
          <div className="space-y-3 text-sm">
            <div className="rounded-md border p-3">
              <div className="mb-1 font-medium">Trust policy</div>
              <pre className="max-h-40 overflow-auto rounded bg-muted p-2 text-xs">
                {JSON.stringify(setup.trust_policy, null, 2)}
              </pre>
              <div className="mt-2">
                <CopyButton text={JSON.stringify(setup.trust_policy, null, 2)} label="Copy trust policy" />
              </div>
            </div>
            <div className="rounded-md border p-3">
              <div className="mb-1 font-medium">Permission policy</div>
              <pre className="max-h-40 overflow-auto rounded bg-muted p-2 text-xs">
                {JSON.stringify(setup.permission_policy, null, 2)}
              </pre>
              <div className="mt-2">
                <CopyButton text={JSON.stringify(setup.permission_policy, null, 2)} label="Copy permission policy" />
              </div>
            </div>
          </div>
        )}

        <ol className="list-decimal space-y-1 pl-5 text-sm text-muted-foreground">
          {(setup.instructions || []).map((stepText, i) => (
            <li key={i}>{stepText}</li>
          ))}
        </ol>

        <div className="flex justify-end">
          <Button onClick={onNext}>I've deployed the role — Continue</Button>
        </div>
      </CardContent>
    </Card>
  )
}
