## ADDED Requirements

### Requirement: Asset Registry route is isolated from static resources

Control MUST resolve `GET /assets` and `GET /assets/` to the Asset Registry SPA route. The built frontend static resource namespace MUST NOT collide with the `/assets` product route namespace.

The frontend in-browser route resolver (currently `web/src/auth/AuthContext.tsx`) MUST treat
`/assets` and `/assets/` as the same `assets` route. Returning the SPA shell for `GET /assets/`
at the HTTP layer is NOT sufficient by itself: the frontend pathname matcher/normalization
MUST also resolve `/assets/` to the `assets` route, otherwise an authenticated user who directly
navigates to or reloads `/assets/` will see the management/default route instead of Asset
Registry. This normalization MUST be minimal — e.g. an equality check against both
`"/assets"` and `"/assets/"`, or an equivalent trailing-slash normalization applied before
route matching — and MUST NOT require introducing a new router framework, a site-wide URL
rewrite subsystem, or a new route abstraction layer.

#### Scenario: Asset Registry route without trailing slash

- **WHEN** an authenticated user requests `GET /assets`
- **THEN** Control returns the Asset Registry SPA shell, the frontend route resolver resolves
  the pathname to the `assets` route, and does not treat the request as a static-resource lookup

#### Scenario: Asset Registry route with trailing slash

- **WHEN** an authenticated user requests `GET /assets/`
- **THEN** Control returns the same Asset Registry SPA route, the frontend route resolver
  resolves the pathname to the `assets` route (not `management`/default), and the SPA can load
  its lazy chunks

#### Scenario: Authenticated direct navigation and reload render Asset Registry

- **WHEN** an authenticated user directly navigates to `/assets`, directly navigates to
  `/assets/`, or reloads the browser while on `/assets/`
- **THEN** in every case the server returns the correct SPA shell, the frontend route resolver
  resolves the pathname to the `assets` route, and the rendered page is the Asset Registry page
  (identified by a stable Asset Registry UI signal such as its page heading or a dedicated
  asset-page test id), and the management/default page MUST NOT be rendered

#### Scenario: Static resource miss

- **WHEN** a request targets a missing built static resource
- **THEN** Control returns the defined static-miss response and does not return the Asset Registry SPA shell

#### Scenario: Existing API route

- **WHEN** a request targets an existing Control API route
- **THEN** API route matching and response behavior remain unchanged

#### Scenario: Static asset hit

- **WHEN** a request targets an existing file under `/static/`
- **THEN** Control serves the corresponding embedded build file without treating it as a product route

#### Scenario: Static asset miss

- **WHEN** a request targets a missing file under `/static/`
- **THEN** Control returns HTTP 404 and does not return the SPA shell

#### Scenario: SPA deep link

- **WHEN** an approved non-API SPA deep link is requested
- **THEN** Control returns the SPA shell while `/assets` and `/assets/` remain the Asset Registry route
