// Package jwt issues and verifies CTOgram platform JWTs across the HS256→RS256
// migration.
//
// Pick a constructor by role:
//
//	NewManager   — legacy HS256 sign+verify. The pre-migration default.
//	NewSigner    — RS256 signing ONLY. Use exclusively in the issuer (cg-users).
//	NewValidator — RS256 verification via JWKS, with optional HS256 dual-accept.
//	NewVerifier  — returns Manager or Validator from config; use this in every
//	               verifying service so Phase 3→4→7 are config flips, not code.
//
// Migration gates live in Config:
//   - verify side:  JWKSURL, AcceptHS256, ExpectedIssuer (enable iss-check only
//     after the issuer is centralized — issuers are non-uniform mid-migration).
//   - issuer side:  PrivateKeyPEM, SigningKeyID, SignWithRS256.
//
// Verifiers own a background JWKS refresher, so always `defer v.Close()`.
package jwt
