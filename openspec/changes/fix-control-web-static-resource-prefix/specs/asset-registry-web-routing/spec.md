## ADDED Requirements

### Requirement: Asset Registry route is isolated from static resources

Control MUST resolve `GET /assets` and `GET /assets/` to the Asset Registry SPA route. The built frontend static resource namespace MUST NOT collide with the `/assets` product route namespace.

#### Scenario: Asset Registry route without trailing slash

- **WHEN** an authenticated user requests `GET /assets`
- **THEN** Control returns the Asset Registry SPA shell and does not treat the request as a static-resource lookup

#### Scenario: Asset Registry route with trailing slash

- **WHEN** an authenticated user requests `GET /assets/`
- **THEN** Control returns the same Asset Registry SPA route and the SPA can load its lazy chunks

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
