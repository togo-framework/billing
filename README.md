<!-- togo-header -->
<div align="center">
  <picture><source media="(prefers-color-scheme: dark)" srcset=".github/assets/togo-mark-dark.svg" /><img src=".github/assets/togo-mark.svg" alt="ToGO" height="64" /></picture>
  <h1>togo-framework/billing</h1>
  <p>
    <a href="https://to-go.dev/marketplace"><img src="https://img.shields.io/badge/marketplace-to--go.dev-1F8A99" alt="marketplace" /></a>
    <a href="https://pkg.go.dev/github.com/togo-framework/billing"><img src="https://pkg.go.dev/badge/github.com/togo-framework/billing.svg" alt="pkg.go.dev" /></a>
    <img src="https://img.shields.io/badge/license-MIT-blue" alt="MIT" />
  </p>
  <p><strong>Part of the <a href="https://to-go.dev">togo</a> framework.</strong></p>
</div>

## Install

```bash
togo install togo-framework/billing
```

<!-- /togo-header -->

<p align="center"><picture><source media="(prefers-color-scheme: dark)" srcset=".github/assets/togo-mark-dark.svg" /><img src=".github/assets/togo-mark.svg" alt="ToGO" width="72" /></picture></p>
<h1 align="center">togo · billing</h1>

Usage-based **billing + API keys** for [togo](https://to-go.dev). Generate per-user
API keys (hashed, scoped, revocable), meter usage — especially **AI token usage** —
per key/user, track consumption, enforce quotas, and report. The `ai` plugin reports
token usage here via `billing.Record`.

## Install

```bash
togo install togo-framework/billing
```

Blank-importing the package registers it; tables (`api_keys`, `usage_events`) are
created on boot.

## API

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/billing/keys` | Generate a key for a user (`{user_id, name, scopes, quota}`) — returns the **secret once** |
| `GET` | `/api/billing/keys?user_id=` | List a user's keys (hash omitted) |
| `DELETE` | `/api/billing/keys/{id}` | Revoke a key |
| `GET` | `/api/billing/usage?user_id=` | Aggregated consumption (events, tokens, cost) |

In production the acting user comes from the authenticated API key (`Authorization: Bearer <key>`)
via `Service.Middleware`; `KeyFromContext` reads it. The `X-User-Id` header / `?user_id=`
are dev fallbacks.

## Metering from another plugin

```go
// e.g. the ai plugin reporting token usage after a chat completion:
billing.Record(ctx, k, userID, apiKeyID, model, inputTokens, outputTokens, cost)
```

`billing.Record` is a no-op if billing isn't installed, so callers stay decoupled.

## Quotas

Set `quota` (max total tokens, 0 = unlimited) when generating a key; check it with
`Service.WithinQuota`.

MIT

<!-- togo-sponsors -->
---

<div align="center">
  <h3>Premium sponsors</h3>
  <p>
    <a href="https://id8media.com"><strong>ID8 Media</strong></a> &nbsp;·&nbsp;
    <a href="https://one-studio.co"><strong>One Studio</strong></a>
  </p>
  <p><sub>Support togo — <a href="https://github.com/sponsors/fadymondy">become a sponsor</a>.</sub></p>
</div>
<!-- /togo-sponsors -->
