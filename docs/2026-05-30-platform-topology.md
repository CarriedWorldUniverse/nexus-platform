# CWU Platform Topology

**Status:** design draft for review · 2026-05-30
**Scope:** how the CWU hosted product runs *eventually* — and, more importantly, the **much smaller v1 we actually build now**.

## 0. v1 = DOGFOOD, not launch (the governing constraint)

Operator (2026-05-30): **"just enough capability for us to run a real use case for our own use."** v1 is **not** a product launch. The first and only "customer" is **us** — our own agent network (the dMon fleet) using herald + cairn + ledger + commonplace for **real work**. We prove the capability by *using it*, then generalize.

This **defers most of the platform architecture** below. Build only what serving *one known entity (ourselves)* requires:

| Platform feature (designed below, the DESTINATION) | In the dogfood v1? |
|---|---|
| herald + cairn + ledger (+ commonplace) as herald-gated services on k3s | ✅ **yes — the capability we use** |
| service-to-service auth via heraldauth (+ SA bootstrap) | ✅ yes — proves the shape |
| single-node k3s, per-service SQLite+litestream→S3 | ✅ yes |
| multi-tenant isolation / arbitrary unknown clients | ❌ later — it's just us, one org |
| public well-known endpoint / open client boundary | ❌ later — private/tailnet is fine |
| interchange ingress (public webhook edge) | ⚠️ only if our use case needs inbound webhooks now |
| interchange relay (push to a no-public-ports nexus) | ⚠️ only if our nexus is *separate* from the cluster |

**Everything from §1 onward describes the destination.** Read it as the target the v1 manifests grow into — not the v1 build list. The v1 build list is §7, trimmed to the ✅ rows.

### 0b. Three deployment modes (one software)

The platform ships in three modes — **same software, different operator + auth**:

1. **Standalone / embedded** — sovereign-solo / dev / a network without full support. **Auth bypassed** (the `NEXUS_AUTH_BYPASS` pattern, extended across services + interchange). Services co-located/trusted; interchange minimal or absent. First-class supported mode, not just dev. (This is what dMon runs *today*.)
2. **Self-hosted platform** — a commercial entity runs their own full stack (their interchange + herald + cairn + ledger).
3. **We host it** — us, the public platform.

Modes 2 & 3 are **identical software, different operator** → we build ONE deployable platform (sellable as self-hostable, and less work than two). Mode 1 is the degenerate case (boundary collapsed). Spectrum: `standalone(bypass) → +real-auth+boundary → self-hosted ≡ we-host`.

### 0a. The dogfood split on dMon — k3s for ISOLATION, not orchestration

Critical v1 shape (operator 2026-05-30): **k3s hosts ONLY the would-be-hosted services; nexus stays running natively as it is today.** The *reason* for k3s here is **not** orchestration (16-core dMon doesn't need it) — it's **boundary fidelity**: k3s `ClusterIP` services are unreachable from the host, so native nexus is *physically forced* to reach herald/cairn/ledger through the single ingress (interchange) with real auth — exactly as it would reach AWS. Native co-location would let us accidentally depend on co-location (skip auth, assume locality) and only discover it at the AWS lift. k3s makes the shortcut impossible → we dogfood the real path. It's a *simulation* of isolation (same kernel/box), faithful enough to catch every "assumed local" bug; WAN distance is a separate later concern. The manifests are reusable for the eventual AWS lift.

**interchange = the single ingress** in front of the cluster (run interchange itself as the door, not Traefik — dogfood the real proxy component). Auth-bypass mode collapses this (mode 1).

```
  dMon
  ┌──────────────────────────────────────────────────────────┐
  │  NATIVE (unchanged, as today)      k3s cluster (new)       │
  │  ┌────────────────────┐            ┌────────────────────┐ │
  │  │ nexus broker + Keel │            │ herald             │ │
  │  │ + 6 aspect units    │── herald ──│ cairn              │ │
  │  │ (THE CLIENT)        │   tokens   │ ledger             │ │
  │  │                     │──────────▶ │ commonplace-svc    │ │
  │  └────────────────────┘            └────────────────────┘ │
  └──────────────────────────────────────────────────────────┘
```

- **k3s (rootful, systemd):** herald, cairn, ledger, commonplace-service (the hosted *product* — the eventual multi-tenant backend); each owns its own SQLite (litestream→S3), no shared DB (§5).
- **Native (untouched):** nexus broker + Keel + the 6 aspect systemd units — the live fleet keeps running, zero migration risk.
- **Why this is faithful, not a shortcut:** in production, nexus is the *customer's self-hosted client* (their box) and herald/cairn/ledger are the *hosted product* (the cluster). Keeping nexus native + product-in-cluster makes dMon dogfood the **actual client↔product boundary** — nexus connects to the cluster the same way a customer's nexus will connect to the hosted platform. This is also exactly NEX-382 (re-point nexus auth at herald) + NEX-383 (runtime mint client) with a real target.
- **Image flow:** `podman build` → `podman save | k3s ctr images import` (no registry; services are tiny Go static images). Local registry deferred (would mirror ECR-pull later).

## 1. What the product is (the frame this serves)

CWU is **backend-as-a-platform for agent networks**: identity, storage, issue-tracking, and (optional) knowledge, offered behind **one well-known public endpoint**. Customers run their **own** agent network — `nexus` (our reference client) or any client that speaks the protocol — privately, on their own infra, BYO-AI. They connect *in* to use the hosted primitives. We do **not** host customers' nexus networks.

```
   CUSTOMER SIDE (theirs, private, no public ports)     OUR PRODUCT (hosted, k8s)
   ┌──────────────┐                                ┌──────────────────────────────┐
   │ nexus(org A) │── dials out, holds connection ─▶│  ingress / gateway (well-known │
   │ nexus(org B) │── + sends webhooks/requests ───▶│  public endpoint, TLS)         │
   │ their client │                                 │     │                          │
   └──────────────┘                                 │  herald · cairn · ledger ·     │
                                                     │  commonplace · interchange-relay│
                                                     │  herald-gated; per-svc SQLite→S3│
                                                     └──────────────────────────────┘
```

The four hosted primitives:
- **herald** — identity/auth. Gates the front door for arbitrary, unknown-to-us clients ("prove you're org-X").
- **cairn** — git/storage.
- **ledger** — issue tracking.
- **commonplace (upstream)** — optional knowledge store; local-first in the client, upstream only if they don't want to run their own.

Plus the edge:
- **interchange** — the public boundary. Two roles, two runtime shapes (see §4).

## 2. Runtime substrate: k3s on a single small EC2 (NOT EKS)

**Hard cost constraint: ≤ US$20/month, lower preferred.** This rules out managed k8s and managed DBs:
- **EKS control plane = ~$73/mo flat** before a single node → 3.6× the entire budget. **Out.**
- **RDS (smallest) = ~$13–15/mo** → eats the budget alone, and there's no shared dataset to justify it (§5). **Out.**

So: **k3s** (single-binary, ~50MB k8s) on **one small ARM Graviton EC2** (`t4g.small`, 2GB, ~$12/mo; `t4g.micro` ~$6 is tight). The control plane runs *on the node* — no $73 tax. **S3** for object storage + SQLite replication (cents at low volume). **No DB server** — each service owns a SQLite file on the node volume, continuously replicated to S3 via **litestream** (§5).

| Item | ~$/mo |
|------|-------|
| t4g.small EC2 (k3s + all pods) | ~12 (less w/ 1yr savings plan / spot) |
| S3 (low volume) | <1 |
| data transfer (low) | ~1 |
| **total** | **~$13–14, under $20** |

**Crucially, the k8s *shape* survives** — each component is still a **Deployment + Service**, same manifests as the dMon-k3s rehearsal (§6). We keep rolling deploys, health checks, restarts, and the Ingress primitive. We give up (for now) HA + horizontal scale: it's one node. That's fine for v1 / first customers — "platform" in shape, single-box in capacity. **EKS + per-service managed data (Turso/Neon) + multi-node is a "when paying customers justify it" upgrade, not a v1 cost** — and because we ran real k3s manifests, that lift is config, not a rewrite.

Each component is a **Deployment + Service**; one substrate end to end — no k8s+Lambda split (see §4).

| Component | Workload | Exposure | State |
|-----------|----------|----------|-------|
| ingress/gateway controller | DaemonSet/Deployment (NGINX/Traefik/Gateway API) | **public** (the well-known endpoint, TLS) | none |
| interchange-relay | Deployment (long-lived pods) | internal; holds outbound-initiated client conns | connection registry |
| herald | Deployment | ClusterIP | SQLite → S3 (litestream) |
| cairn | Deployment | ClusterIP | SQLite + object store (S3) |
| ledger | Deployment | ClusterIP | SQLite → S3 |
| commonplace (upstream) | Deployment | ClusterIP | SQLite + sqlite-vec → S3 |
| (per-service data) | SQLite file per service, litestream→S3 — NO shared DB pod (§5) | n/a | the data |
| Redis (optional) | Deployment | internal | cache + relay pub/sub (§5); not v1-critical |

Public surface = **only** the ingress/gateway. Everything else is ClusterIP, reachable only in-cluster, every call herald-gated.

**Scaling trigger (explicit): everything here is single-node k3s UNTIL PAYING CUSTOMERS.** The whole table runs as pods on one t4g.small (data = SQLite files on the node's volume, replicated to S3 via litestream). When revenue justifies it, the upgrade path is additive — more nodes, then EKS, then per-service managed data (Turso/Neon) *only where a service needs it* — *using the same manifests*. No architectural rewrite; v1 is the platform shape at single-box capacity.

## 3. Auth between services (herald-gated, everywhere)

The discipline embedded-mode hid: in the hosted topology every hop is a network call that must be authenticated. herald is the authority; `heraldauth` is the verify library every service already imports.

- **Client → service:** client presents a herald token (minted with its casket key); the service verifies locally via `heraldauth` (cached JWKS, no per-request callback) and enforces scope + org.
- **Service → service:** when cairn calls ledger (or anything calls herald), the *calling service* presents its own herald token. So each service has a **service identity** in herald (a herald "agent" whose responsible party is the platform/org-root).
- **Tenant isolation:** every stored row is tenant-scoped by the `org` claim; every read/write authorizes against the caller's verified org. No cross-tenant access path exists because the org comes from the (un-spoofable, herald-signed) token, not the request body.

### 3a. The bootstrap problem (herald is the root)

herald gates everyone — but who gates herald, and how do services get their *first* identity before herald is reachable? Options:
- **k8s-native bootstrap (preferred):** each service pod has a k8s **ServiceAccount**; herald trusts the cluster's SA token issuer (projected SA tokens / OIDC) for the *initial* service-identity exchange, then issues herald tokens. So k8s identity bootstraps platform identity, and herald is the authority thereafter.
- **Sealed root credential:** herald boots with a root signing key + an admin token from a k8s Secret; services hold a sealed credential to obtain their first token. Simpler, less elegant, more secret-sprawl.
- **SPIFFE/SPIRE:** full workload-identity mesh; heaviest, most correct at scale, probably overkill for v1.

**Open decision.** Lean: k8s ServiceAccount bootstrap → herald, because it reuses the substrate's own identity primitive for the one thing herald can't gate (its own clients' first contact).

## 4. The public edge + the push channel (interchange)

Interchange has **two jobs with opposite runtime profiles** — keep them as two workloads.

### 4a. Ingress (inbound, request-shaped)
The public well-known endpoint: TLS termination, webhook receipt + signature verification, and routing inbound REST to the right in-cluster service. On k8s this is the **Ingress/Gateway controller** itself (+ a small handler service for webhook verify/routing). **Not Lambda** — the controller is the always-on edge, and since we run an always-on relay anyway (§4b), one substrate is simpler than k8s+Lambda. (Lambda+API-GW only if we ever want the public endpoint to scale-to-zero independently of the cluster — not a v1 need.)

### 4b. Relay (push, connection-shaped) — the core novel piece
**Decision: push, not poll** (poll is too noisy/expensive). A customer nexus has **no public ports**, so the platform cannot connect *in*. Therefore:

1. The client nexus **dials out** to interchange-relay and **holds a persistent connection open** (WebSocket / gRPC stream / tunnel), authenticated with a herald token.
2. interchange-relay maintains a **connection registry**: `org/client → live connection`.
3. When an event must reach a client (an inbound webhook for their org, a notification), the relay **pushes it down** that client's open connection.

This is why the relay is an always-on **Deployment**, not Lambda. Design questions for this channel:
- **Protocol:** WebSocket (simplest, ubiquitous) vs gRPC server-streaming (typed, multiplexed) vs a raw tunnel. Lean WebSocket for v1.
- **Auth on the channel:** the client authenticates the connection with a herald token at dial-out; the relay verifies via `heraldauth` and binds the connection to that org. Token refresh on a long-lived connection needs handling (re-auth without dropping).
- **Routing:** event carries a target org → relay looks up the org's connection(s) → push. Multiple connections per org (a client with several nexuses / HA) = fan-out or pick-one.
- **Delivery semantics:** at-least-once with client ack? Buffer-on-disconnect (events while a client is offline) → needs a per-org queue (the one place "poll-like" durability sneaks back in, but as a reconnect backfill, not steady-state polling).
- **Scale (cross-pod fanout):** N persistent connections across relay pods → connections are sticky to a pod; pushing to a connection pinned to *another* pod needs an internal fan-out. **Answer: Redis pub/sub** (see §5) — relay pods publish events keyed by org and the pod holding that org's connection consumes + pushes down its socket. (v1 single-relay-pod doesn't need it; Redis enters with horizontal relay scale.) This is the relay's hardest scaling property.

## 5. Data layer — per-service, NOT one shared Postgres

**Key reframe:** there is **no shared database**. Services are herald-gated and talk via API/tokens, never by reading each other's tables (that's the whole boundary). So the question isn't "which central DB" — it's "what does *each* service need," and Postgres's main win (concurrent multi-client access to one dataset) doesn't apply. **Postgres is NOT the default.**

**System-of-record: SQLite per service (it's what's already built).**
- **herald** — orgs/users/scopes. Tiny, read-dominated; the hot path (token verify) is *local JWKS*, not a DB hit. SQLite is genuinely fine at real scale.
- **ledger** — issues/comments/timeline. Relational, moderate volume. SQLite fine single-node.
- **commonplace** — FTS5 today + **sqlite-vec** for vectors later. SQLite is the natural home.
- **cairn** — Forgejo soft-fork, brings its own DB choice (SQLite or Postgres) + needs object storage (below).

**Durability without a DB server: SQLite + litestream → S3.** litestream continuously replicates each SQLite file to S3 → point-in-time recovery + durability with **zero DB ops** (no server, no pod). Cents/month. This is the "back the data in S3" pattern done right, and it fits ≤$20/mo far better than a Postgres pod or RDS.

**Object storage: S3** for cairn repos/LFS/artifacts (cents at low volume), dovetailing with **porter** (casket-encrypted S3-as-FS) as cairn's backing layer.

**Redis = hot cache + pub/sub, NOT storage.** Redis is fast in-memory access for things that need quick reads but **change rarely** (it is NOT a system-of-record — orgs/users/issues are relational queryable records that belong in SQLite). Two roles:
- **cache:** slow-changing hot data (resolved org→scopes, JWKS-adjacent lookups, routing tables) for fast access without hitting the SQLite/service each time.
- **pub/sub fanout for the interchange relay** (resolves the §4b open question): when a push must reach a client connection pinned to a *different* relay pod, relay pods publish/subscribe via Redis to fan the event to the pod holding that connection. (Single-relay-pod v1 doesn't need it; Redis enters when the relay scales horizontally.)
Redis can itself be backed/snapshotted to S3, but it stays an ephemeral/cache tier — losing it costs a cache rebuild, not data.

**Upgrade path when a service outgrows single-node SQLite (per-service, not "migrate all to RDS"):**
- **Turso / libSQL** (SQLite-as-a-service, edge-replicated, free tier) — the natural upgrade for the SQLite services; SQLite-compatible, near-zero code change.
- **Neon** (serverless Postgres, scales to zero, free tier) — only if a specific service genuinely needs Postgres semantics.
Both scale-to-zero + free-tier → they serve the ≤$20/mo constraint *better* than self-hosted Postgres; adopt per-service, only where needed.

**Tenant data** is row-scoped by org; no per-tenant DB in v1.

> **Retraction:** an earlier draft said "Postgres in-cluster." That was the wrong default — there's no shared dataset to justify it. Per-service SQLite + litestream→S3 is the v1 answer; managed-serverless (Turso/Neon) is the per-service upgrade.

## 5a. Cost trajectory (the explicit growth path)

| Stage | Substrate | ~$/mo | Trigger to advance |
|-------|-----------|-------|--------------------|
| **v1 (now → first revenue)** | single-node k3s on t4g.small, per-service SQLite + litestream→S3 | **~$13–14** | — |
| add capacity | + more k3s nodes (still self-managed) | +~$12/node | one node saturates |
| **EKS (the destination)** | EKS control plane + managed nodes + per-service managed data where needed (Turso/Neon) | ~$73 + nodes + data | **paying customers justify it** |

EKS is **where we head, not where we start.** Running real k3s manifests from day one makes that migration config (ingress class, storage class, data endpoints), not a rewrite.

## 6. The dMon rehearsal (prove the shape before AWS)

dMon is treated as the **first hosted deployment**, not a special embedded box. Rehearse the *actual* topology on it with a tiny in-host k8s:

1. **k3s on dMon** (lightweight, single-node, real k8s API).
2. Write the **real manifests** (Deployments/Services/Ingress) for herald + cairn + ledger + commonplace + interchange-relay.
3. Run them as **separate herald-gated services** (the multi-service shape), not embedded — proving inter-service auth, the push channel, and tenant isolation on infra we already own.
4. A test client (a nexus, or `herald-keytool` + a script) dials the relay, mints a token, and exercises create-issue / store / recall through the edge.
5. **Lift the same manifests to EKS** when proven. Because dMon runs real k8s manifests, the lift is config (ingress class, storage class, secrets), not a rewrite.

Tradeoff acknowledged: k3s-on-dMon is more setup than systemd processes, but it's a *faithful* mock (same manifests → EKS) rather than an approximation.

## 7. Sequenced build (derived from this topology)

1. **herald hosted-ready** — herald already uses SQLite (its MVP store); for hosted, add **litestream→S3** replication (sidecar/config, not a new store impl) so the SQLite file is durable. herald is the root; it must run hosted first. (A Postgres/Turso `store` impl is only needed if herald ever outgrows single-node — not v1.)
2. **k3s on dMon + herald manifest** — herald running in-cluster, reachable, herald-gated. The smallest real slice.
3. **heraldauth service-to-service path** — prove cairn (or a stub) authenticates to herald in-cluster via SA-bootstrap → herald token.
4. **interchange-relay v1** — the push channel: client dials out, holds WS, relay pushes a test event. The novel, highest-risk piece — build it early once herald-in-cluster works.
5. **ingress/gateway** — public well-known endpoint + TLS + webhook verify → route to relay/services.
6. **cairn + ledger + commonplace** as in-cluster herald-gated Deployments (cairn needs the object-store/porter tie-in).
7. **EKS lift** — same manifests, prod config.

## 8. Open decisions (resolve before/within build)

1. **Service-identity bootstrap** — k8s SA→herald (lean) vs sealed root cred vs SPIFFE. (§3a)
2. **Push protocol** — WebSocket (lean) vs gRPC stream vs tunnel. (§4b)
3. **Relay cross-pod fan-out** — **resolved: Redis pub/sub** (§4b/§5). Only needed when the relay scales beyond one pod.
4. **Offline delivery** — buffer-on-disconnect + reconnect backfill, or fire-and-forget. (§4b)
5. **Single boundary vs per-service edge** — does ALL client↔service traffic go through interchange/ingress, or do cairn/ledger expose their own herald-gated API routes through the same ingress? (Leaning: one ingress, path-routed to services; relay only for push.)
6. **Data**: **resolved — per-service SQLite + litestream→S3** (no shared DB; Postgres was the wrong default). Remaining sub-Q: litestream-vs-Turso for the SQLite services' durability (lean plain SQLite+litestream for dogfood; Turso when multi-node read replicas are wanted). (§5)
7. **Does herald itself need the relay** to push (e.g. revocation/cascade events to clients), or is herald pull-only? (Interaction between §3 and §4b.)
