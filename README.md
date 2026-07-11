# billkaat <sub>काट</sub>

**Cut your AWS bill — locally.** A single-binary health check for Amazon Web
Services that runs on *your* machine, uses *your* read-only credentials, and
never sends a byte anywhere. Results land in a local SQLite file.

Agencies sell "free AWS health checks" that require handing an engineer access
to your account. billkaat is the alternative: download, run, and see the same
findings yourself in about a minute.

```
make demo          # explore the UI with fake data, no AWS needed
```

## Quick start

1. Create a read-only IAM user (or role) and attach [`iam-policy.json`](iam-policy.json).
2. Expose credentials the usual way — env vars or `~/.aws/credentials`:

   ```sh
   export AWS_ACCESS_KEY_ID=...
   export AWS_SECRET_ACCESS_KEY=...
   ```

3. Run it:

   ```sh
   go run .            # or: make build && ./bin/billkaat
   ```

4. Open **http://127.0.0.1:4141**, pick a region, hit **Run health check**.

Flags: `-addr` (default `127.0.0.1:4141`), `-db` (default `billkaat.db`),
`-workers` (default 4), `-demo`.

## What it checks

### Community (open source, this repo)

| Check | Finds | Category |
|---|---|---|
| `ebs-unattached` | Volumes billing while attached to nothing | cost |
| `ebs-gp2-to-gp3` | gp2 volumes that migrate to gp3 for ~20% less | cost |
| `eip-unassociated` | Idle Elastic IPs ($3.65/mo each) | cost |
| `ec2-stopped-storage` | Stopped instances still paying full price for disks | cost |
| `snapshot-stale` | Snapshots older than 180 days | cost |
| `elb-no-targets` | Load balancers (~$16/mo) forwarding to nothing | cost |
| `elb-all-unhealthy` | LBs whose every target fails health checks | performance |
| `sg-open-to-world` | SSH/RDP/databases open to 0.0.0.0/0 | security |

### Pro (one-time license, free updates)

EC2 & RDS rightsizing from CloudWatch data, idle NAT gateways, over-provisioned
EBS IOPS, CloudWatch Logs retention, DynamoDB capacity mode, Lambda memory,
S3 lifecycle & storage class, RI/Savings-Plan coverage gaps, IAM hygiene,
public exposure audit. The Community build lists these as locked rows —
the full catalog lives in
[`internal/checks/pro/catalog.go`](internal/checks/pro/catalog.go).

Savings figures are estimates from a static price table
(`internal/checks/pricing.go`, us-east-1 rates) — their job is to size the
opportunity, not reproduce the invoice.

## Architecture

```
main.go                    flags, embedded web UI, wiring
internal/
  checks/                  Check interface, Finding model, registry, pricing
    free/                  the open-source check set (one file ≈ one check)
    pro/                   catalog + locked stubs; real impls are pro-tagged
  engine/                  worker pool, per-check progress, demo seed
  store/                   SQLite (scans, per-check status, findings, settings)
  server/                  JSON API + static UI, binds to localhost
  license/                 offline Ed25519 verification
  awsx/                    AWS SDK client bundle (read-only)
cmd/licensegen/            key generation + license signing CLI
web/                       vanilla HTML/CSS/JS, embedded into the binary
```

Adding a check is one file: implement `Meta()` and `Run()`, call
`checks.Register` in `init()`. The engine, storage, UI, and CSV export pick it
up automatically.

## How the open-core + license model works

**The split.** `internal/checks/pro/catalog.go` (public) describes every Pro
check so the free UI can advertise them as locked rows. Real implementations
are files with a `//go:build pro` tag — like the included example
`nat_idle.go`. Before publishing this repo, **move the pro-tagged
implementation files to a private mirror** of the repo; the public repo keeps
only the catalog and stubs. Your private repo tracks the public one
(`git remote add public …; git merge public/main`) and adds the pro files.

**Building.**

```sh
make build                       # Community
make keygen                      # once: prints PRIVATE + PUBLIC key
make build-pro PUBKEY=<pub_hex>  # Pro, from the private repo
```

**Licenses** are Ed25519-signed JSON, verified completely offline — no license
server, no phone-home, works air-gapped:

```sh
go run ./cmd/licensegen sign -key <PRIVATE_HEX> -email buyer@x.com -name "Buyer Co"
```

The buyer pastes the key into the UI; the binary checks the signature against
the compiled-in public key. The private key **is the business** — keep it in a
password manager plus an offline backup, and never in git. (Publishing
`licensegen` itself is harmless; only the key matters.)

**Free updates.** Simplest paths, in order of effort: (a) invite buyers as
read-only collaborators on the private repo's Releases, (b) a Gumroad /
Lemon Squeezy product page whose file you update each release, (c) later, a
tiny download endpoint that checks a license key. Cross-compile releases with
GoReleaser; for CGO-free cross-compiles, swap `mattn/go-sqlite3` for
`modernc.org/sqlite` (same `database/sql` code).

**Selling.** Lemon Squeezy or Paddle act as merchant of record (they handle
global VAT) and can call a webhook on purchase that runs `licensegen sign` and
emails the key. For the Nepali market, eSewa/Khalti plus manually issuing keys
works fine at the start — signing a license takes five seconds.

**Suggested positioning.** The Community build *is* the marketing: every scan
shows the locked rows and what they'd find. Price the Pro license as a
fraction of one month of typical findings (e.g. $79–149 one-time
internationally; price locally in NPR for Nepal).

## Honesty & legal notes

- Do **not** use "AWS" in your product name, domain, or logo — Amazon's
  trademark guidelines prohibit it. "billkaat for Amazon Web Services" as a
  description is fine. (Rename the project freely; the name is a placeholder —
  *kaat* काट = "cut".)
- Savings numbers are estimates; say so in your marketing.
- The tool needs only the read-only policy in `iam-policy.json`. Keep it that
  way — never asking for write access is your trust story.
- Recommended license for this public repo: Apache-2.0 (adoption is the
  funnel; the Pro code never ships in it anyway).

## Development

```sh
make demo    # UI with seeded data, no AWS
make test    # license round-trip + check helpers
```
