# 06 — Metrics & KPIs

How to measure adoption, usage, UX success, and (customer-facing) security value for the
Bellu suite. Split into **product metrics** (how to instrument the frontend) and the
**security KPIs the UI surfaces** to users. No telemetry is wired yet — this is the
instrumentation plan.

---

## 1. Instrumentation approach

- Emit a typed `track(event, props)` call from a thin analytics module (future
  `src/lib/analytics.ts`), buffered client-side and flushed to a sink (e.g. a `/api/events`
  endpoint or a 3rd-party SDK). Keep it behind the same client seam pattern as data.
- Never send prompt bodies, evidence payloads, secrets, or PII in event props — send IDs,
  counts, durations, and enum values only.
- Gate analytics on explicit configuration; default off in dev.

### Suggested event taxonomy

| Event | Props | Fires when |
|---|---|---|
| `page_view` | `route, role` | route change |
| `nav_click` | `section, to` | sidebar item click |
| `filter_change` | `page, filter, value` | a Select/filter changes |
| `finding_investigate` | `finding_id, severity` | opening a finding detail |
| `edge_explain` | `relation` | explaining an edge |
| `connector_wizard_step` | `step, scope, method` | wizard step transitions |
| `connector_verify_result` | `ok, failing_caps` | verify completes |
| `addon_view` | `module` | opening a Preview module |
| `media_review_open` | `kind, score_bucket` | opening the media review dialog |
| `theme_toggle` | `to` | theme switch |

---

## 2. Adoption metrics

| Metric | Definition | Source |
|---|---|---|
| Active users | distinct users with ≥1 `page_view` / week | `page_view` |
| Module adoption | distinct users per module / week | `page_view`, `addon_view` |
| Inventory coverage | % AI assets inventoried vs estimated total | `GET /api/stats` entities + estimate |
| Connector adoption | # connected cloud accounts; % reaching COMPLETE | connectors API |
| Add-on interest | Preview module views (signal for build priority) | `addon_view` |

---

## 3. Feature-usage metrics

| Metric | Definition |
|---|---|
| Filter usage | `filter_change` rate per page (which filters matter) |
| Findings triaged | `finding_investigate` count / open findings |
| Investigations | `edge_explain` count / week |
| Wizard funnel | step-to-step conversion from `connector_wizard_step` → `connector_verify_result(ok)` |
| Dashboard engagement | sessions reaching `/overview`; chart hover/tooltip interactions |

---

## 4. Operational / UX metrics

| Metric | Target | How |
|---|---|---|
| Page load (LCP) | < 2.5s | Web Vitals |
| Interaction (INP) | < 200ms | Web Vitals |
| API error rate (UI) | < 1% of calls | wrap `client` request errors |
| Table/graph scale | smooth at 10k rows / nodes | virtualize/paginate when needed |
| Chart render | no layout shift (CLS ≈ 0) | responsive containers (already used) |

---

## 5. Security KPIs surfaced in the UI

These are shown to users (CISO/Analyst), not product analytics. Foundation KPIs are real;
add-on KPIs are demo until a backend ships.

| KPI | Where | Real / Demo |
|---|---|---|
| Asset / edge counts, ingestion health | Security Overview | real |
| Entities-by-kind, edges-by-relation | Security Overview charts | real |
| Open findings by severity; risk scores | Findings | real |
| Top attack paths to sensitive data | TraceGraph | real |
| Gateway: calls, latency, error rate, failovers, cache hit | AI Gateway | demo |
| Runtime: injection/exfil/jailbreak alerts, guardrail hits | Runtime Defense | demo |
| Agents: risk scores, blocked tool actions | Agent Security | demo |
| Data: sensitive assets, policy-violating flows, DLP matches | AI Data Security | demo |
| Supply chain: CVEs by severity, unsigned models | AI Supply Chain | demo |
| Media: authenticity score, flagged media, detection confidence | Media Authenticity | demo |
| Compliance: controls passing/total per framework | Governance | demo |

---

## 6. UX success criteria (qualitative)

- Time-to-first-insight on `/overview` under a few seconds.
- A finding can be triaged (open → read cited intel + evidence) without leaving the page.
- Connector onboarding completable without docs for a cloud-savvy admin.
- SUS survey ≥ 80 after onboarding; task-success ≥ 90% for the core flows
  (login, read a finding, run an investigation, add a connector).

Collect via periodic in-product surveys and moderated sessions with security teams; feed
results back into the backlog.
