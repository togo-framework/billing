<p align="center"><img src="https://to-go.dev/togo-mark.svg" width="72" alt="togo"></p>
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
