# Embedded phase-0 contract fixtures

These sanitized fixtures are the review-pinned copy of
`ops/contracts/cliproxyapi/auth-files/v1` used by the Control repository's
standalone tests. They contain only `example.invalid` identities and synthetic
messages; they are not raw Node responses.

Baseline: CLIProxyAPI `v7.2.141`, commit
`dc3c3b1ec3ed04bb0917e76451eaf98c6842674d`, official image digest
`sha256:7f598ce64478a8a5f90ed76875e0e9b0e7d77b80e17184b13df18c3d5bdb3def`.

`cases.yaml` SHA-256:
`96b3cb8cb2329d1eacc2e9da53b50fe5a9ea37a019aeb0d706d62c9d2cdb15c9`.
Tests verify the complete manifest and every copied fixture. Updating this copy
requires updating the phase-0 contract first and reviewing the new hashes.
