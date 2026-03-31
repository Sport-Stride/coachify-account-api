# Coachify Account API - Service Architecture Guide

## 1. WHAT THIS SERVICE DOES

### Primary Responsibility
The Coachify Account API is a user and account management microservice that handles user authentication, profile management, and coach-client relationship management for the SportStride fitness coaching platform. It owns all user account data, manages authentication tokens, and maintains the associations between coaches and their clients.

### What Data It Owns
- **Users collection** (`users`): Complete user profiles including authentication credentials, profiles, verification status, OAuth provider links, and account metadata
- **Coach-Client relationships** (`coach_clients`): Associations between coaches and their client users with timestamps

### External Dependencies
- **Identifier API** (`pkg/identifier`): Used for generating unique identifiers for external IDs in the system
- **Invitation API** (`pkg/invitation`): Handles user invitations and accepts notifications
- **Notification API** (`pkg/notification`): Sends transactional emails (confirmations, password resets, etc.)
- **Payment API** (`pkg/payments`): Integrates with payment/subscription service for user subscription validation
- **OAuth Providers** (Google, Facebook): OAuth 2.0 authentication for social login

---

## 2. API SURFACE

### Public Authentication Endpoints (No Auth Required)

| Method | Path | Purpose | Request | Response |
|--------|------|---------|---------|----------|
| `POST` | `/user/signup` | Register new user | `CreateUserRequest` (firstname, lastname, email, password, optionals) | `RegisterResponse` (user, auth_token, refresh_token) |
| `POST` | `/user/confirm` | Confirm email after signup | `ConfirmUserRequest` (email, code) | HTTP 200 |
| `POST` | `/user/resend-confirm` | Resend confirmation email | `ResendConfirmUserRequest` (email) | HTTP 200 |
| `POST` | `/user/login` | Authenticate user | `LoginRequest` (username, password, autologin bool) | `LoginResponse` (user, encrypted auth_token) |
| `POST` | `/user/reset-password/init` | Start password reset flow | `ResetPasswordRequest` (email) | HTTP 200, sends confirmation code via email |
| `POST` | `/user/reset-password/confirm` | Complete password reset | `ConfirmResetPasswordRequest` (email, code, new_password) | HTTP 200 |
| `POST` | `/user/refresh-token` | Get new access token | Body: refresh token | New encrypted access token |
| `GET` | `/user/get-user-by-email/:email` | Find user by email | Query param: email | `ApiUser` |
| `GET` | `/user/check-email/:email` | Verify email exists | Query param: email | `ApiUser` if found |
| `GET` | `/user/get-user-by-id/:mongodb_id` | Get user by MongoDB ObjectId | Query param: id | `ApiUser` |
| `GET` | `/user/get-user/:external_id` | Get user by external ID | Query param: external_id | `ApiUserResponse` |
| `GET` | `/` or `/health` | Health check | None | `{"status": "UP"}` |

### OAuth Endpoints (Public)

| Method | Path | Purpose | Request | Response |
|--------|------|---------|---------|----------|
| `GET` | `/oauth/:provider/login` | Initiate OAuth login flow | Param: provider (google/facebook) | Redirect to provider |
| `POST` | `/oauth/:provider/auth/me` | OAuth callback (server-side) | Body: `GoogleLoginRequest` (account, profile, role) | `OAuthResponse` (user) |

### Admin/Batch User Endpoints (Public, but business-logic protected)

| Method | Path | Purpose | Request | Response |
|--------|------|---------|---------|----------|
| `POST` | `/user/` | Add new user to organization | `CreateUserRequest` (requires valid org with active subscription) | `RegisterResponse` |
| `GET` | `/user/` | List all users (paginated) | Query: page, size, filters | `[]ApiUserResponse`, total count |
| `DELETE` | `/user/` | Delete user | Body: `DeleteUserRequest` (external_id) | HTTP 200 |

### Protected Endpoints (Requires Valid JWT Bearer Token)

#### User Management (JWT Protected)
| Method | Path | Purpose | Request | Response |
|--------|------|---------|---------|----------|
| `PUT` | `/user/update-user` | Update own user profile | `RequestUpdateUser` (user fields to update) | `ApiUser` (updated) |

#### Coach-Client Management (JWT Protected, Coach Only)
| Method | Path | Purpose | Request | Response |
|--------|------|---------|---------|----------|
| `GET` | `/coach/clients` | List coach's clients with filtering | Query: client_id, from_date, to_date, page, size | `{clients: [], total: int}` |
| `GET` | `/coach/client` | Get coach ID for a given client | Query param: client_id | `{coach_id: string}` |
| `DELETE` | `/coach/client/:client_id` | Remove coach-client relationship | Param: client_id | HTTP 200 |

### Authentication Requirements

- **Public endpoints**: No authentication required for signup/login/health
- **Protected coach endpoints** (`/coach/*`): Require `Authorization: Bearer <encrypted_jwt_token>` header
  - Token must be valid and not expired
  - Token must contain `id` claim (user's external ID)
  - Claims automatically extracted from token to context
- **Refresh token endpoint**: Uses the refresh token (longer expiry) to issue new access token
- **OAuth flow**: Verifies state parameter against cookie; stores encrypted provider tokens in user document

### Request/Response Patterns

**Successful Response**: HTTP 200/201 with JSON body (varies by endpoint)

**Error Response**: HTTP 4xx/5xx with JSON body:
```json
{
  "error": "Human-readable error message or error object"
}
```

**Token Format**: All tokens are AES-GCM encrypted before transmission, decrypted with `CoachifyEncryptionKey`
```
Header: Authorization: Bearer <encrypted_token_string>
Decrypt → Get JWT → Parse claims
JWT Claims: { "id": <external_id>, "name": <firstname lastname>, "email", "exp": <unix_timestamp>, "role" }
```

**Pagination**: Supported on GET user list and coach clients endpoints
```
Query: ?page=1&size=10  (page defaults to 1, size defaults to 10)
Response includes: "total": <total_matching_records>
```

---

## 3. CODE STRUCTURE

### Folder Layout

```
coachify-account-api/
├── main.go                    # Entry point: initializes app, handles graceful shutdown
├── app/
│   └── app.go                # App struct, setup(), run(), shutdown() - initializes all services
├── handlers/                 # HTTP request handlers, one per route group
│   ├── auth.go              # Authentication endpoints
│   ├── coach.go             # Coach-client management endpoints  
│   ├── health.go            # Health check handler
│   ├── missing.go           # 404 fallback handler
├── routes/
│   ├── router.go            # Route registration, middleware chain setup
│   └── middlewares.go       # JWT validation, CORS, security headers, logging, metrics
├── services/
│   ├── auth.go              # AuthService interface & implementation (user auth logic)
│   ├── coach_client.go      # CoachService interface & implementation (coach-client logic)
│   ├── services.go          # Services container (dependency injection)
│   ├── helpers.go           # Shared service helpers
│   └── mocks/               # Mock implementations for testing
├── repositories/
│   ├── User.go              # UserRepository (CRUD, queries for users collection)
│   ├── Coach.go             # CoachRepository (coach-client relationships & queries)
│   └── indexes.go           # Database index definitions & initialization
├── models/
│   ├── apiError.go          # ApiError struct & error constants
│   ├── api/                 # API request/response DTOs
│   │   ├── authentication.go
│   │   ├── coach_client.go
│   │   └── const.go
│   ├── db/                  # Database schema models (match MongoDB documents)
│   │   ├── user.go          # User, UserStatus, UserGender, OAuth models
│   │   ├── coach_client.go  # CoachClient, CoachClientListQuery
│   │   └── const.go         # User status/gender enums
│   ├── mapping/             # Model transformations (db ↔ api)
│   │   ├── user.go
│   │   └── coach_client.go
│   └── masks/               # Field masking for sensitive data
│       └── user.go
├── oauth2/                  # OAuth provider implementations
│   ├── factory.go           # OAuth provider factory
│   ├── provider.go          # Provider interface
│   ├── google.go            # Google OAuth2 implementation
│   └── facebook.go          # Facebook OAuth2 implementation
├── pkg/                     # External service clients (shared packages)
│   ├── identifier/
│   │   ├── api.go           # HTTP client to identifier service
│   │   └── model.go         # Identifier request/response models
│   ├── invitation/
│   │   └── api.go           # HTTP client to invitation service
│   ├── notification/
│   │   ├── api.go           # HTTP client to notification service
│   │   └── model.go         # Email models
│   └── payments/
│       ├── api.go           # HTTP client to payment service
│       └── models.go        # Payment models
├── core/                    # Core business logic (password, activation)
│   ├── password.go          # Password checker (validation & hashing)
│   ├── activation.go        # Activation code generation & management
├── utils/
│   ├── config.go            # Config loading from environment (uses Viper)
│   ├── token.go             # JWT token creation/validation/encryption
│   ├── crypto.go            # AES-GCM encryption/decryption
│   ├── logger.go            # Zap logger setup
│   ├── retry.go             # Retry logic for external API calls
│── go.mod                    # Go module definition
└── Dockerfile               # Container image definition
```

### Entry Point & Boot Sequence

**[main.go](main.go)** (lines 1-27):
1. Calls `app.New()` to create and configure the application
2. Runs the app in a goroutine
3. Waits for SIGINT/SIGTERM signals
4. Gracefully shuts down on signal

**[app/app.go](app/app.go)** - `New()` → `setup()` executes:
1. **Load Config** from environment variables via Viper
2. **Connect to MongoDB**
   - Creates client with connection URI from config
   - Pings database to verify connection
   - Gets "users" database reference
3. **Initialize Database Indexes** (calls `repositories.InitializeIndexes()`)
   - Creates multi-field indexes for performance-critical queries
   - Logs warnings if indexes fail (doesn't block startup)
4. **Initialize Core Services**
   - Password checker (bcrypt wrapper)
   - Activation manager (token generation)
   - OAuth provider factory (Google, Facebook)
5. **Initialize External Service Clients**
   - Identifier API client (for generating unique IDs)
   - Invitation API client
   - Notification API client (email)
   - Payment API client
6. **Initialize Repositories** (UserRepository, CoachRepository)
7. **Initialize Services** (AuthServiceImpl, CoachServiceImpl)
8. **Initialize Router** (sets up all routes and middleware)
9. **Store references in App struct** for later use

**[app/app.go](app/app.go)** - `Run()` method:
- Starts Gin HTTP server on port from config (default likely 8080)
- Blocks until error or shutdown

### Route Registration & Middleware Chain

**[router/router.go](router/router.go)** - `InitializeRouter()`:
1. Creates Gin engine
2. Applies **global CORS middleware** (allows all origins in dev, specific headers)
3. Calls `initializeMiddlewares(r)` - sets up middleware stack
4. Calls `initializeRoutes(r, services)` - registers all routes

### Middleware Chain Order

**[router/middlewares.go](router/middlewares.go)** - `initializeMiddlewares()`:
```
1. CORS (gin-contrib/cors)           → Handles cross-origin requests
2. securityHeaders()                 → Sets HSTS, CSP, HSTS, X-Frame-Options, X-XSS-Protection
3. RecoveryWithZap()                 → Catches panics, logs with Zap, returns 500
4. Prometheus metrics                → Tracks HTTP request metrics
5. requestLogger() [debug only]      → Logs request bodies in debug mode
```

**Protected Routes** (under `protected.Use(AuthMiddleware())`):
```
AuthMiddleware([router/middlewares.go](router/middlewares.go) line 206):
  1. Extract Authorization header (format: "Bearer <encrypted_token>")
  2. Decrypt token using AES-GCM with CoachifyEncryptionKey
  3. Parse JWT with HMAC-SHA256 validation using CoachifySecretKey
  4. Verify signature and expiration
  5. Extract "id" claim from JWT & store in context["userID"]
  6. Allow request to proceed to handler
```

---

## 4. DATA LAYER

### MongoDB Collections

#### `users` Collection
Stores all user account data. Primary key: `_id` (MongoDB ObjectId)

**Core Fields:**
```go
type User struct {
  // Identity
  ID                     primitive.ObjectID  // MongoDB generated
  ExternalID             string              // External service reference (UNIQUE)
  
  // Basic Info
  UserFirstname          string
  UserLastname           string
  UserEmail              string              // UNIQUE, required for login
  UserPhone Number       string              // Optional
  UserGender             UserGender          // "Male", "Female"
  
  // Authentication
  UserPassword           string              // Bcrypt hashed
  Token                  *string             // Current access token (encrypted)
  UserRefreshToken       *string             // Refresh token (encrypted)
  
  // Status & Verification
  UserStatus             UserStatus          // Active, Inactive, Suspended, Blocked, etc.
  UserVerificationStatus bool                // Email verified
  UserConfirmCode        *UserConfirmCode    // {Code, ExpirationDate} for email confirmation
  UserResetPasswordCode  *UserResetPasswordCode // {Code, ExpirationDate} for password reset
  
  // OAuth
  Providers              map[string]OAuthProviderDetails // provider_type → encrypted tokens
  Autologin              bool                // Auto-login on OAuth
  
  // Profile
  UserProfilePicture     string
  UserDescription        string
  UserRole               string              // e.g., "Coach", "Client", "Admin"
  UserAddress            Address             // City, Country, Line1, Line2, PostalCode, State
  Metadata               *UserMetadata       // HowHeardAboutUs, Profession, etc.
  
  // Timestamps
  UserCreatedAt          time.Time
  UserUpdatedAt          time.Time
  UserLastLogin          time.Time
}
```

**UserStatus can be:**
- `"Active"` - Account active and verified
- `"Inactive"` - Newly created, awaiting email confirmation
- `"Suspended"` - Admin suspended
- `"Complete-registration-1/2/3"` - Registration in progress states
- `"To Confirm"` - Email needs confirmation
- `"Deactivated"` - User deactivated
- `"Blocked"` - Account blocked (cannot login)

#### `coach_clients` Collection
Stores coach-to-client many-to-many relationships.

```go
type CoachClient struct {
  ID        primitive.ObjectId  // MongoDB generated
  CoachID   string              // external_id of coach user
  ClientID  string              // external_id of client user
  CreatedAt time.Time           // When relationship was established
}
```

**Query to get coach's clients:**
```
1. Find all coach_clients where coach_id = <coach_external_id>
2. Join with users collection on coach_clients.client_id = users.externalid
3. Project user fields (firstname, lastname, email, profile_picture, etc.)
4. Sort by created_at DESC
5. Apply pagination (skip, limit)
```

### Database Indexes

**[repositories/indexes.go](repositories/indexes.go)** - `InitializeIndexes()` creates:

#### Users Collection Indexes
```
1. UNIQUE INDEX: externalid
   → Fast lookup by external ID (primary lookup in this service)
   → Prevents duplicate external IDs
   
2. UNIQUE INDEX: email (sparse=true)
   → Enable unique email constraint for login
   → Sparse allows documents without email
   
3. INDEX: status
   → Filter users by status (Active, Blocked, etc.)
   
4. COMPOUND INDEX: (status, created_at DESC)
   → Support queries filtering by status with sorting
```

#### Coach-Clients Collection Indexes
```
1. COMPOUND INDEX: (coach_id, created_at DESC)
   → PRIMARY: Find all clients for a coach, sorted by date
   → Used by ListCoachClients without client_id filter
   
2. COMPOUND INDEX: (coach_id, client_id, created_at DESC)
   → Support filtered queries for specific client + date range
   
3. INDEX: client_id
   → REVERSE LOOKUP: Find which coach manages a client
   
4. UNIQUE COMPOUND INDEX: (coach_id, client_id)
   → Prevent duplicate coach-client associations
```

### Key Data Models & Purpose

#### API Models ([models/api/](models/api/))
- `CreateUserRequest` - Signup request validation
- `LoginRequest` - Email + password login
- `ConfirmUserRequest` - Email confirmation with code
- `ApiUser` - User data returned in responses (all fields)
- `ApiUserResponse` - User data with masks applied (sensitive fields hidden)
- `OAuthResponse` - OAuth login response wrapper

#### DB Models ([models/db/](models/db/))
- `User` - Exact MongoDB document schema
- `CoachClient` - Coach-client relationship document
- `UserStatus` enum + `UserGender` enum

#### Mapping Models ([models/mapping/](models/mapping/))
Transform between API DTOs and database models:
- `User` (db) ↔ `ApiUser` (api)
- Includes token data extraction (`ToRefreshToken`)
- Handles field renaming/masking

#### Mask Models ([models/masks/](models/masks/))
- Apply field masks to hide sensitive data in responses
- E.g., hide password, refresh token, confirm codes

### Query Patterns Used

#### User Lookup Queries
```go
// By external ID (primary lookup, indexed)
FindOne(ctx, bson.M{"externalid": externalID})

// By email (indexed, used for login)
FindOne(ctx, bson.M{"email": email})

// By MongoDB ID (indexed by default)
FindOne(ctx, bson.M{"_id": objectId})

// List with pagination (uses status index + created_at)
Find(ctx, bson.M{
  "status": bson.M{"$in": []string{"Active"}},
  "created_at": bson.M{"$gte": fromDate},
}).Sort(bson.M{"created_at": -1}).Skip().Limit()
```

#### Coach-Client Queries

**List Coach's Clients with Pagination:**
```javascript
Aggregation pipeline (repositories/Coach.go lines 97-190):
  Stage 1: $match { coach_id, client_id?, created_at range? }
  Stage 2: $lookup → Join users collection on coach_clients.client_id = users.externalid
  Stage 3: $unwind → Flatten joined user array
  Stage 4: $sort { created_at: -1 }
  Stage 5: $facet → Split into two pipelines:
    - "total": $count
    - "data": $skip, $limit, $project (selected fields)
  Result: Single query returns both total count and paginated data
```

**Get Coach ID for a Client (reverse lookup):**
```go
FindOne(ctx, bson.M{"client_id": clientID})
Decode into struct with CoachID field
```

---

## 5. PATTERNS SPECIFIC TO THIS SERVICE

### Password Management

**[core/password.go](core/password.go)** - `PasswordChecker` interface:
- Validates password strength: ≥8 chars, 1 uppercase, 1 lowercase, 1 symbol
- Returns error: `ErrInvalidPassword` with requirements message
- Hashes passwords with bcrypt before storage (`crypto.HashPassword`)
- Verifies passwords on login with bcrypt compare

### JWT Token Management

**[utils/token.go](utils/token.go)** - `CreateToken()`:
```
1. Generate HMAC-SHA256 JWT with:
   - id: user's externalID (used for userID in context)
   - name: firstname + lastname
   - email: user's email
   - exp: Unix timestamp (current + duration)
   - role: user's role
   
2. Access token TTL: 24 hours
3. Refresh token TTL: 30 days

4. Encrypt JWT with AES-GCM using CoachifyEncryptionKey
   → Result is base64-encoded encrypted token
   
5. Return encrypted token for transmission
```

**Token Validation in AuthMiddleware:**
```
1. Extract "Authorization: Bearer <encrypted_token>" header
2. Decrypt AES-GCM → Get JWT string
3. Parse JWT with HS256 secret key
4. Verify signature & expiration
5. Extract "id" claim → Store in context["userID"]
```

### Confirmation Code & Password Reset Codes

**[core/activation.go](core/activation.go)** - Simple implementation:
- Generates random 32+ character alphanumeric codes
- Stored in user document with expiration timestamp
- Codes expire after set duration (typically 1 hour)
- Validated on confirmation/reset endpoints

### OAuth2 Integration

**[oauth2/](oauth2/)** implementation:
1. **OAuth2Login** endpoint redirects to Google/Facebook
2. **OAuth2ServerSideCallback** receives OAuth credentials from frontend
3. Creates/updates user with OAuth provider tokens (encrypted)
4. Stores encrypted access_token, refresh_token, id_token in `providers` map
5. Supports multiple OAuth providers per user

### Token Refresh Strategy

**[services/auth.go](services/auth.go)** lines 90-180:
- `ValidateAndRefreshTokens()` checks both token types:
  - If **access token valid** & not expired → Use it
  - Else if **refresh token valid** → Generate new access token
  - Else → Generate both new tokens
- Refresh is **automatic on login** and **explicit via refresh endpoint**
- Used when called from OAuth login or refresh token endpoints

### Coach-Client Relationship Management

**Business Rules:**
- Coach can only see their own clients (enforced in handler with JWT userID)
- Coaches can list, filter, and dissociate clients
- Dissociating removes the coach_clients document (soft delete not used)
- Cannot list other coach's clients (handler extracts coach ID from JWT)

### External API Dependencies

When user performs actions, service calls:
1. **Notification API** - Send confirmation/reset emails
2. **Identifier API** - Generate unique external IDs
3. **Invitation API** - Check invitation acceptance status
4. **Payment API** - Validate subscription limits when adding users

---

## 6. ERROR CODES & MEANINGS

**[models/apiError.go](models/apiError.go)** defines all error constants:

### Authentication Errors (4xx)
- `ErrPasswordMismatch` (401) - Wrong password on login
- `ErrAccountNotConfirmed` (403) - Account not email-verified
- `ErrAccountIsBlocked` (403) - User account is blocked
- `ErrAuthentificationFailed` (401) - Invalid credentials
- `ErrIncorrectPassword` (401) - Password doesn't match
- `ErrInvalidRefreshToken` (401) - Refresh token expired/invalid
- `ErrInvalidConfirmationCode` (400) - Wrong confirmation code
- `ErrInvalidResetPasswordCode` (400) - Wrong password reset code

### User Management Errors (4xx)
- `ErrUserNotFound` (404) - User doesn't exist
- `ErrEmailAlreadyExists` (400) - Email is taken
- `ErrUserAlreadyExists` (409) - User already created
- `ErrUserAlreadyVerified` (400) - Trying to confirm already-confirmed user
- `ErrUnknownUser` (404) - User lookup failed

### Validation Errors (400)
- `ErrInvalidPassword` (400) - Password doesn't meet requirements
- `ErrInvalidIdFormat` (400) - ID format malformed
- `ErrEmailNotProvided` (400) - Email required but missing
- `ErrInvalidTokenType` (400) - Bad token type parameter

### Database/Server Errors (5xx)
- `ErrInternalError` (500) - Generic database error
- `ErrFailedToCreateUser` (500) - Insert failed
- `ErrFailedToUpdateUser` (500) - Update failed
- `ErrFailedToDeleteUser` (500) - Delete failed
- `ErrRetrievingUser` (500) - Query failed
- `ErrFailedToDecodeUser` (500) - Data unmarshalling error

### External Service Errors (5xx)
- `ErrFailedToSendEmail` (500) - Notification API call failed
- `ErrFailedToCreateRequest` (500) - Cannot build request to external API
- `ErrUnexpectedStatusCode` (500) - External API returned unexpected status
- `ErrFailedToExchangeCode` (500) - OAuth code exchange failed

---

## 7. THINGS TO NEVER CHANGE

### Critical Service Contracts

#### User External ID Field
- **Field name in MongoDB**: `externalid` (lowercase)
- **Field name in API responses**: `external_id` (snake_case)
- **Use**: Primary identifier for other microservices
- **Why**: Other services (Identifier, Payment, Invitation) expect this field for user references
- **Never**: Store user lookups by MongoDB `_id` externally; always use `externalid`

#### Coach-Client Relationship Keys
- **CoachID field**: Stores external_id of the coach user (NOT MongoDB _id)
- **ClientID field**: Stores external_id of the client user (NOT MongoDB _id)
- **Why**: Enables joins with users collection on externalid
- **Never**: Use MongoDB ObjectIds in coach_clients; always use external IDs

#### JWT Token Claims
- **Token Type**: HMAC-SHA256 signed
- **Secret Key**: Uses `CoachifySecretKey` from config (shared across services)
- **Encryption**: Tokens are AES-GCM encrypted before transmission
- **Claims MUST include**:
  - `"id"`: user's externalid (consumed by other services)
  - `"email"`: user's email
  - `"role"`: user's role
  - `"exp"`: expiration timestamp (Unix)
- **Never**: Change the "id" claim name or put MongoDB _id there
- **Never**: Change encryption algorithm without coordinating with other services

#### Authentication Header Format
- **Format**: `Authorization: Bearer <encrypted_jwt>`
- **Decryption**: Uses `CoachifyEncryptionKey` from config
- **Never**: Change to Basic auth, change encryption algorithm, or use unencrypted tokens

#### User Status Enum Values
- **Allowed values**: `"Active"`, `"Inactive"`, `"Suspended"`, `"Blocked"`, `"Deactivated"`, `"To Confirm"`, `"Complete-registration-1/2/3"`
- **Used by**: External services may query or depend on status values
- **Never**: Rename existing status values; only add new ones if needed
- **Note**: `"To Confirm"` is used before email verification

#### MongoDB Collection Names
- **Users collection**: `"users"` (lowercase)
- **Coach-clients collection**: `"coach_clients"` (lowercase)
- **Database name**: `"users"` (defined in app.go setup)
- **Never**: Rename collections without updating all repositories

#### Email Field Uniqueness
- **Constraint**: UNIQUE, SPARSE index on email
- **Behavior**: Allows multiple documents with no email, but no duplicate emails
- **Used by**: Login endpoint and external service lookups
- **Never**: Remove unique constraint (would break login logic)

#### OAuth Provider Storage
- **Location**: Stored in `User.Providers` map at `providers.<provider_type>`
- **Fields**: `access_token` (encrypted), `refresh_token` (encrypted), `id_token`, `expiry`
- **Never**: Change field names; other services may depend on this structure

#### Response Field Names (API Contract)
**User Response objects use snake_case:**
- `firstname` → `firstname`
- `lastname` → `lastname`
- `externalid` → `external_id`
- `email` → `email`
- `profile_picture` → `profile_picture`
- `phone_number` → `phone_number`
- `verification_status` → `verification_status`
- Never use camelCase in responses; always snake_case

#### Email Verification / Confirmation Flow
- **Field**: `UserConfirmCode` { Code, ExpirationDate }
- **Stored in**: User document, not separate collection
- **Never**: Move to separate collection without migration
- **Used by**: Other services to verify email status

#### Password Hashing Algorithm
- **Algorithm**: Bcrypt (not MD5, not SHA256)
- **Never**: Change to weaker algorithm
- **Validation**: Passwords must be ≥8 chars with uppercase, lowercase, symbol

---

## 8. INTEGRATION POINTS WITH OTHER SERVICES

### External Service Calls Made By This Service

**Notification API** (pkg/notification):
- Called on: User signup, password reset, email confirmation
- Sends: Email with confirmation codes, reset links
- Failure handled: Returns error to user, suggests retry

**Identifier API** (pkg/identifier):
- Called on: User registration
- Purpose: Generate unique external ID
- Critical: Every user needs an external ID before being saved

**Invitation API** (pkg/invitation):
- Called on: Check invitation acceptance
- Purpose: Validate user invitation status for onboarding flow

**Payment API** (pkg/payments):
- Called on: Adding new user to organization
- Purpose: Validate org subscription active & within user limits
- Blocks: Cannot add user if subscription invalid or limit exceeded

### Data Other Services Consume From This Service

**Other services read from this service via:**
1. Direct MongoDB queries (if they have access)
2. REST API calls to this service's endpoints
3. Cached user data passed through JWT tokens

**Critical fields they depend on:**
- `externalid` - User identification
- `email` - Contact info
- `role` - Authorization decisions
- `UserStatus` - Access control (block Active, deny Blocked accounts)
- `UserVerificationStatus` - Gate features on verified users

---

## 9. CONFIGURATION & ENVIRONMENT

### Required Environment Variables
Configured in [utils/config.go](utils/config.go) via Viper:

```
MONGODB_URI=mongodb://localhost:27017
COACHIFY_SECRET_KEY=<64+ char secret for JWT signing>
COACHIFY_ENCRYPTION_KEY=<32-byte key for AES-256-GCM>

# External APIs
IDENTIFIER_API=<URL to identifier service>
INVITATION_API=<URL to invitation service>
NOTIFICATION_API=<URL to notification service>
PAYMENT_API=<URL to payment service>

# OAuth providers
GOOGLE_CLIENT_ID=<from Google Cloud Console>
GOOGLE_CLIENT_SECRET=<from Google Cloud Console>
FACEBOOK_CLIENT_ID=<from Facebook Dev Portal>
FACEBOOK_CLIENT_SECRET=<from Facebook Dev Portal>

# Server config
PORT=8080
BASE_URL=http://localhost:8080  # For redirect URLs
GIN_MODE=release  # or debug for development
```

---

## 10. DEPLOYMENT & RUNNING

### Local Development

```bash
# Install dependencies
go mod download

# Set environment variables
export MONGODB_URI=mongodb://localhost:27017
export COACHIFY_SECRET_KEY=dev-secret-key-minimum-64-chars-long-for-jwt
export COACHIFY_ENCRYPTION_KEY=dev-32-byte-encryption-key-0123456
# ... set other ENV vars

# Run
go run main.go

# Service will start on Port 8080
# Health check: curl http://localhost:8080/health
```

### Docker

```bash
docker build -t coachify-account-api .
docker run -p 8080:8080 -e MONGODB_URI=mongodb://mongo:27017 coachify-account-api
```

---

## 11. KEY FILES FOR COMMON TASKS

| Task | File |
|------|------|
| Add new API endpoint | [handlers/](handlers/) + [router/router.go](router/router.go) |
| Add database index | [repositories/indexes.go](repositories/indexes.go) |
| Change JWT claims | [utils/token.go](utils/token.go) |
| Add user field | [models/db/user.go](models/db/user.go) + migration script |
| Modify coach-client logic | [services/coach_client.go](services/coach_client.go) + [repositories/Coach.go](repositories/Coach.go) |
| Change validation rules | [core/password.go](core/password.go) + handlers |
| Configure external APIs | [utils/config.go](utils/config.go) + [app/app.go](app/app.go) |
| Debug auth issues | [router/middlewares.go](router/middlewares.go) AuthMiddleware |
