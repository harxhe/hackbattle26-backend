# HackBattle Backend — API Documentation

All endpoints return JSON. All team endpoints (except `/signin` and the health check) require a valid Firebase ID token.

---

## Authentication

Authentication is handled via Firebase **Google sign-in**. The client must obtain an ID token (e.g. with `firebase.auth().signInWithPopup(provider)` and `user.getIdToken()`) and send it in the `Authorization` header:

```
Authorization: Bearer <FIREBASE_ID_TOKEN>
```

The token is verified on every request. The user's **email** is extracted from the token claims (`email`) and lowercased, then used to look up the user's document in the `users` collection.

| Header | Value |
| --- | --- |
| `Authorization` | `Bearer <FIREBASE_ID_TOKEN>` |
| `Content-Type` | `application/json` (for requests with a body) |

### CORS

Allowed origins (all other origins are blocked):
`http://localhost:3000`, `http://localhost:3001`, `http://localhost:3002`, `https://hackbattle.ieeecsvit.com`, `https://elegant-hotteok-e0afec.netlify.app`, `https://hackbattle25.netlify.app`, `https://hackbattle-25.vercel.app`, `https://hackbattle25.ieeecsvit.com`, `https://hackbattle.ieeecsvit.com`

Allowed methods: `GET`, `POST`, `PUT`, `DELETE`, `OPTIONS`
Allowed headers: `Content-Type`, `Authorization`
Credentials are enabled.

---

## Firebase Data Model

### Collection: `users`

Users are **pre-seeded** in Firestore by the organizers. The backend never creates or registers users — it only reads/updates them during sign-in and team workflows. A token whose email has no matching user document is rejected with `404`.

Document ID = **email** (lowercased).

| Field | Type | Description |
| --- | --- | --- |
| `email` | string | User email (also the doc ID) |
| `name` | string | Display name |
| `IsLead` | bool | Whether the user is a team leader |
| `TeamID` | string | null | Team code of the team the user belongs to, or unset |
| `isVITian` | string | `Internal` (VIT student) or `External` (non-VIT) |
| `regNo` | string | Registration / roll number |
| `createdAt` | timestamp | When the user document was created |

### Collection: `teams`

Document ID = **team code** (6 chars, `A-Z0-9`).

| Field | Type | Description |
| --- | --- | --- |
| `Code` | string | Team code (also the doc ID) |
| `Name` | string | Team name |
| `leaderId` | string | Email of the team leader |
| `members` | array of `{email, name}` | Each member's email and name |
| `Track` | string | Track the team is building for |
| `FigmaLink` | string | Figma design link (after submission) |
| `OtherLinks` | array of string | Additional links (max 6, after submission) |
| `CreatedAt` | timestamp | When the team was created |
| `SubmittedAt` | timestamp | When the project was first submitted (set once) |
| `UpdatedAt` | timestamp | When the team/submission was last updated |

---

## Endpoints

---

### `GET /`

Health check.

**Response `200 OK`**
```
Router is working!
```
(plain text, not JSON)

---

### `GET /signin`

Verifies the Firebase ID token and checks whether the user has registered for the event and is part of a team.

**Auth:** `Authorization: Bearer <ID token>` (required)

**Response `200 OK`**
```json
{
  "isInTeam": true
}
```
`isInTeam` is `true` if the user's `TeamID` field is a non-empty value, otherwise `false`.

**Error responses:**

| Status | Body |
| --- | --- |
| `401 Unauthorized` | `Missing Authorization header` |
| `401 Unauthorized` | `Invalid Authorization header format` |
| `401 Unauthorized` | `Invalid or expired token` |
| `401 Unauthorized` | `Token missing email claim` |
| `401 Unauthorized` | `Token email claim is invalid` |
| `404 Not Found` | `User has not registered for the event.` |
| `500 Internal Server Error` | `Internal server error` |

---

### `POST /teams/create`

Creates a new team. The authenticated user becomes the leader.

**Auth:** required

**Request body**
```json
{
  "name": "Team Alpha"
}
```

**Behavior:**
- The team name must be unique (case-sensitive check against existing `Name` fields).
- A random 6-character team code (`A-Z0-9`) is generated.
- If the user already has a `TeamID`, no team is created.

**Response `201 Created`**
```json
{
  "message": "Team created successfully",
  "code": "AB12CD"
}
```

**Response `200 OK`** (if the user is already in a team)
```json
{
  "message": "You are already in a team"
}
```

**Error responses:**

| Status | Body |
| --- | --- |
| `401 Unauthorized` | `Invalid token: missing email` |
| `401 Unauthorized` | `Invalid token: missing display name` |
| `400 Bad Request` | `Invalid team name` |
| `404 Not Found` | `User profile not found` |
| `409 Conflict` | `This team name is already taken` |
| `500 Internal Server Error` | Firestore error (e.g. `An unexpected error occurred: <msg>`) |

**Firestore writes:** creates team doc, sets `user.TeamID = <code>`, `user.IsLead = true`, `team.members = [{email, name}]`.

---

### `POST /teams/join`

Adds the authenticated user to an existing team by code.

**Auth:** required

**Request body**
```json
{
  "team_code": "AB12CD"
}
```
The code is uppercased before lookup.

**Behavior:**
- The team must exist and have fewer than 5 members.
- Adds `{email, name}` to `team.members` and sets `user.TeamID = <code>`.

**Response `200 OK`**
```json
{
  "message": "User joined team successfully"
}
```

**Response `204 No Content`** (invalid/missing team code, or team not found)
```json
{
  "message": "Invalid or missing team code"
}
```
```json
{
  "message": "Team with that code not found"
}
```

**Response `201 Created`** (if the user is already in a team)
```json
{
  "message": "You are already in a team"
}
```

**Response `208 Already Reported`** (team is full)
```json
{
  "message": "Team at max size"
}
```

**Error responses:**

| Status | Body |
| --- | --- |
| `401 Unauthorized` | `Invalid token: missing email` |
| `404 Not Found` | `User profile not found` |
| `400 Bad Request` | `Missing user name` |
| `500 Internal Server Error` | Firestore error |

> Note: max team size is `5`.

---

### `GET /teams/get`

Returns the authenticated user's team details.

**Auth:** required

**Response `200 OK`**
```json
{
  "id": "AB12CD",
  "name": "Team Alpha",
  "code": "AB12CD",
  "leaderId": "lead@vit.edu",
  "members": [
    { "email": "lead@vit.edu", "name": "Alice" },
    { "email": "bob@vit.edu", "name": "Bob" }
  ],
  "track": "E-Commerce",
  "figma_link": "https://figma.com/file/xyz",
  "other_links": ["https://drive.google.com/..."],
  "submitted_at": "2026-08-24T10:00:00Z",
  "updated_at": "2026-08-24T10:05:00Z",
  "isLeader": true
}
```

Field notes:
- `members` is the raw `members` array from Firestore (list of `{email, name}` objects).
- `track`, `figma_link`, `other_links`, `submitted_at`, `updated_at` are omitted/`null` if not set.
- `isLeader` is `true` when the authenticated user's email equals `leaderId`.

**Response `204 No Content`** (user is not part of any team)
```json
{
  "message": "User is not part of any team"
}
```

**Error responses:**

| Status | Body |
| --- | --- |
| `401 Unauthorized` | `Invalid token: missing email` |
| `404 Not Found` | User or team document not found |
| `500 Internal Server Error` | `Failed to parse team data` or Firestore error |

---

### `POST /teams/remove-member`

Lets a **team leader** remove a member from the team.

**Auth:** required (must be team leader)

**Request body**
```json
{
  "memberEmail": "bob@vit.edu"
}
```

**Response `200 OK`**
```json
{
  "message": "Member removed successfully"
}
```

**Error responses:**

| Status | Body |
| --- | --- |
| `400 Bad Request` | `Invalid member email provided` |
| `400 Bad Request` | `Member email required` |
| `401 Unauthorized` | `Invalid token: missing email` |
| `403 Forbidden` | `User is not a team leader` |
| `404 Not Found` | `User profile not found` / `Member user profile not found` |
| `500 Internal Server Error` | Firestore error |

**Firestore writes:** removes `{email, name}` from `team.members`, sets `member.TeamID = null`, `member.IsLead = false`.

---

### `DELETE /teams/delete`

Deletes the team. Only the **team leader** can do this, and only when the team has exactly 1 member (the leader alone).

**Auth:** required (must be team leader)

**Response `200 OK`**
```json
{
  "message": "Team deleted successfully"
}
```

**Error responses:**

| Status | Body |
| --- | --- |
| `401 Unauthorized` | `Invalid token: missing email` |
| `403 Forbidden` | `User is not a team leader` |
| `403 Forbidden` | `You must remove all other members before deleting the team` |
| `404 Not Found` | `User profile not found` / `Team not found` |
| `500 Internal Server Error` | Firestore error |

**Firestore writes:** deletes the team doc, sets `leader.TeamID = null`, `leader.IsLead = false`.

---

### `DELETE /teams/leave-team`

Lets a user leave their team. Behavior depends on the user's role:

- **Leader with 1 member (only the leader):** the team document is **deleted**.
- **Leader with >1 members:** leadership is transferred to the next member in the `members` array (first member whose email != leader's email); the leaving leader is removed from `members`; `team.leaderId` and the new leader's `IsLead` are updated.
- **Regular member:** removed from `team.members`; `user.TeamID` cleared.

**Auth:** required

**Response `200 OK`**
```json
{
  "message": "Action completed successfully"
}
```

**Error responses:**

| Status | Body |
| --- | --- |
| `401 Unauthorized` | `Invalid token: missing email` |
| `403 Forbidden` | `User is not in a team` |
| `404 Not Found` | `User profile not found` / `Team not found` |
| `500 Internal Server Error` | `Could not find a new leader.` / `TeamID field is invalid` / `User name not found in user document` / Firestore error |

**Firestore writes:**
- Member: `team.members` updated, `user.TeamID` deleted, `user.IsLead` deleted.
- Leader (multi): `team.leaderId` = new leader, `team.members` without old leader, new leader `IsLead = true`, old leader `TeamID`/`IsLead` deleted.
- Leader (solo): team doc deleted, leader `TeamID`/`IsLead` deleted.

> There is also an unregistered `LeaveTeam` handler (leaders cannot use it; it returns a `403 Forbidden` message: `Leaders cannot leave a team. Delete team or transfer leadership.`), but no route is bound to it.

---

### `POST /teams/project/submit`

Submits/updates the team's project. **Leader only.**

**Auth:** required (must be team leader)

**Request body**
```json
{
  "track": "E-Commerce",
  "figma_link": "https://figma.com/file/xyz",
  "other_links": ["https://drive.google.com/..."]
}
```

| Field | Required | Type |
| --- | --- | --- |
| `track` | Yes | string |
| `figma_link` | Yes | string |
| `other_links` | No | array of string (max 6) |

**Behavior:**
- `SubmittedAt` is set on the first submission only (not overwritten on later submits).
- `UpdatedAt` is always updated to now.
- Empty strings in `other_links` are trimmed out before saving.

**Response `200 OK`**
```json
{
  "message": "Project submitted successfully"
}
```

**Error responses:**

| Status | Body |
| --- | --- |
| `400 Bad Request` | `Invalid request body` |
| `400 Bad Request` | `Track is required` / `Figma link is required` / `At most 6 additional links are allowed` |
| `401 Unauthorized` | `Invalid token: missing email` |
| `403 Forbidden` | `User is not a team leader` |
| `404 Not Found` | `User profile not found` / `Team not found` |
| `500 Internal Server Error` | Firestore error |

---

### `PUT /teams/project/update`

Updates an already-submitted project. **Leader only.** Only the fields provided in the request are updated.

**Auth:** required (must be team leader)

**Request body** (all fields optional; include only what you want to change)
```json
{
  "track": "Finance",
  "figma_link": "https://figma.com/file/abc",
  "other_links": ["https://drive.google.com/..."]
}
```

**Response `200 OK`**
```json
{
  "message": "Project updated successfully"
}
```

**Error responses:**

| Status | Body |
| --- | --- |
| `400 Bad Request` | `Invalid request body` |
| `400 Bad Request` | `No update data provided` |
| `401 Unauthorized` | `Invalid token: missing email` |
| `403 Forbidden` | `User is not a team leader` |
| `403 Forbidden` | `Project has not been submitted yet. Use the submit endpoint first.` |
| `404 Not Found` | `User profile not found` / `Team not found` |
| `500 Internal Server Error` | Firestore error |

---

## Status Code Quick Reference

| Code | Meaning |
| --- | --- |
| `200 OK` | Success |
| `201 Created` | Team created / user already in team (join) |
| `204 No Content` | Invalid input or not in a team (with a JSON message body) |
| `208 Already Reported` | Team is at max size |
| `400 Bad Request` | Invalid/missing request body or fields |
| `401 Unauthorized` | Missing/invalid/expired token, or missing email/name claim |
| `403 Forbidden` | Not a team leader / action not allowed |
| `404 Not Found` | User/team document not found |
| `409 Conflict` | Duplicate team name |
| `500 Internal Server Error` | Firestore/parsing error |

---

## Firestore Auth Endpoint Quick Reference

| Method | Path | Auth | Description |
| --- | --- | --- | --- |
| GET | `/` | No | Health check |
| GET | `/signin` | Bearer token | Verify user & team membership |
| POST | `/teams/create` | Bearer token | Create team |
| POST | `/teams/join` | Bearer token | Join team by code |
| GET | `/teams/get` | Bearer token | Get my team |
| POST | `/teams/remove-member` | Bearer token (leader) | Remove a member |
| DELETE | `/teams/delete` | Bearer token (leader) | Delete team |
| DELETE | `/teams/leave-team` | Bearer token | Leave/delete/transfer team |
| POST | `/teams/project/submit` | Bearer token (leader) | Submit project |
| PUT | `/teams/project/update` | Bearer token (leader) | Update submission |
