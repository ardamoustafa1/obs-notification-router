# Routing decision tree

Required labels are `team`, `service`, and `severity`. `critical` is accepted as a backward-compatible alias for `page`; `warn` maps to `warning`. A catalog record whose team conflicts with the alert is rejected instead of leaking an alert to the wrong team.

| Alert status | Severity | Primary destinations | After 5 minutes without acknowledgement |
|---|---|---|---|
| firing | info | Service's Slack channel | — |
| firing | warning | Service's Slack channel; Opsgenie team when enabled | — |
| firing | page | Service's Slack channel + PagerDuty service; Opsgenie when enabled | Escalation Slack channel + secondary PagerDuty service |
| resolved | any | Resolve/update the same primary destinations | Pending escalation becomes `resolved` |
| any | invalid/missing required label | Rejected and recorded in structured logs | — |
| any | catalog entry missing | Platform fallback channel; page also goes to platform PagerDuty | Uses platform escalation defaults |

```mermaid
flowchart TD
    A[Alertmanager webhook v4] --> B{team, service, severity valid?}
    B -- no --> R[Reject + structured error]
    B -- yes --> C[Catalog enrichment]
    C --> D{Catalog team matches?}
    D -- no --> R
    D -- missing --> F[Platform fallback + audit flag]
    D -- yes --> S{severity}
    F --> S
    S -- info --> SI[Slack]
    S -- warning --> SW[Slack + optional Opsgenie]
    S -- page --> SP[Slack + PagerDuty + optional Opsgenie]
    SP --> E{Acknowledged within 5m?}
    E -- yes --> ACK[Stop timer + PagerDuty acknowledge]
    E -- no --> ESC[Escalation Slack + secondary PagerDuty]
    SI & SW & SP & ESC --> O[(PostgreSQL outbox + audit)]
```

## Escalation state machine

```mermaid
stateDiagram-v2
    [*] --> pending: page/firing
    pending --> acknowledged: Slack/API acknowledgement
    pending --> escalated: due_at reached
    pending --> resolved: Alertmanager resolved
    escalated --> acknowledged: late acknowledgement
    escalated --> resolved: Alertmanager resolved
    acknowledged --> resolved: Alertmanager resolved
```

Claiming a due escalation, creating both secondary-delivery outbox rows, and moving the state to `escalated` occur in one PostgreSQL transaction with `FOR UPDATE SKIP LOCKED`. Multiple replicas can therefore run workers without double escalation or a crash gap.

