# Coachify Account API

Authentication and identity hub of the platform. Creates, verifies and logs in every user (coach or client), handles profiles, OAuth2 (Google, Facebook), JWT issue/refresh, email confirmation, password reset, and coach-client relationships. All other services validate the JWTs it issues.

## Stack
Go, Gin, MongoDB (db `users`; collections `users`, `coach_clients`), Viper config, bcrypt, AES-GCM + HS256 JWT. Calls identifiers, notification, invitation, payments over HTTP (`pkg/`).

## Layout
`main.go`, `app/` (init), `router/` (routes + middleware), `handlers/` (thin) -> `services/` (logic) -> `repositories/` (Mongo only), `core/` (password, activation), `oauth2/`, `models/{api,db,mapping,masks}`, `pkg/` (clients), `utils/` (config, token, crypto).

## Rules
- Other services reference users by `ExternalID` (Mongo field `externalid`, API `external_id`), never Mongo `_id`. `coach_clients` stores external IDs only.
- JWT claims must keep `id` (externalid), `email`, `role`, `exp`. Never rename `id`. Tokens are AES-GCM encrypted (`COACHIFY_ENCRYPTION_KEY`), signed HS256 (`COACHIFY_SECRET_KEY`); header `Authorization: Bearer <encrypted_jwt>`. Never change algorithm or header format.
- Passwords: bcrypt only, never plain text, timing-safe compare. Min 8 chars with upper, lower, digit/symbol.
- Never rename existing status values, collection names, or snake_case response fields.
- Never remove the unique sparse index on email (login depends on it).
- Confirmation/reset codes have no DB TTL: enforce expiry in the service layer for any new code flow.
- OAuth state cookie expires after 600s; the callback fails opaquely after that.
- Signup calls identifiers, notification, invitation, payments synchronously with no retries; any failure breaks signup.
- Every Mongo call uses a context with a deadline; nil-check optional nested fields; errors as `{"error": "..."}`; never log passwords or tokens.
- Repo has unresolved merge-conflict markers in the old docs; docs/ARCHITECTURE.md is a verbatim copy.

## Commands
`go build ./... && go test ./...`, `golangci-lint run`, `go run main.go` (port 8080, health `/health`), `docker build -t coachify-account-api .`. Config via env (see `utils/config.go`); secrets only from env.

Full detail: docs/ARCHITECTURE.md (read only the section needed).
