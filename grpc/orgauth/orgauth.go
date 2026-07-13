// Package orgauth provides a reusable gRPC-layer helper that enforces
// organization membership for end-user callers.
//
// The platform embeds the caller's organization context in the access token.
// A handler that scopes or authorizes on an organization_id taken from the
// request body MUST verify that the caller is actually acting on behalf of
// that organization — otherwise a legitimately-authenticated user of org A can
// read or mutate org B's data simply by passing organization_id=B (a cross-org
// IDOR).
//
// EnforceOrgMatch centralizes the check that was previously hand-copied into
// individual handlers (cg-services/bid), so every service enforces it
// identically.
//
// Trust model:
//   - Service-to-service callers (UserID == 0) are trusted and bypass the
//     check — they legitimately act on behalf of arbitrary orgs (admin panel,
//     internal tools, cross-service calls).
//   - Legacy tokens without any org context (single OrgID empty AND OrgIDs nil)
//     bypass the check for backward compatibility during the org-model migration.
//     A present, empty orgs[] claim is authoritative and grants no org access.
//   - For every other user caller, the requested org must match the caller's
//     org context: membership in OrgIDs when present (Option B), else equality
//     with the single OrgID claim.
//
// Usage:
//
//	auth, ok := sharedGRPC.GetAuthInfo(ctx)
//	if !ok {
//	    return nil, status.Error(codes.Unauthenticated, "authentication required")
//	}
//	if err := orgauth.EnforceOrgMatch(auth, req.GetOrganizationId()); err != nil {
//	    return nil, err
//	}
package orgauth

import (
	"slices"

	sharedGRPC "github.com/4ubak/cg-shared-libs/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrOrgMismatch is the message returned when an end-user caller targets an
// organization they are not a member of.
const ErrOrgMismatch = "caller is not a member of the target organization"

// EnforceOrgMatch verifies that an authenticated caller may act on behalf of
// requestedOrgID.
//
// It returns:
//   - codes.Unauthenticated when auth is nil (no authenticated caller);
//   - codes.PermissionDenied when a user caller targets an org outside their
//     membership;
//   - nil when the caller is permitted (service token, legacy no-org token,
//     empty requested org, or a matching org).
//
// requestedOrgID == "" is treated as "no target org asserted" and passes: this
// helper only guards cross-org access. Handlers that require an org_id must
// validate its presence separately (e.g. codes.InvalidArgument).
func EnforceOrgMatch(auth *sharedGRPC.AuthInfo, requestedOrgID string) error {
	if auth == nil {
		return status.Error(codes.Unauthenticated, "authentication required")
	}

	// Service-to-service callers act on behalf of arbitrary orgs.
	if auth.UserID == 0 {
		return nil
	}

	// No target org asserted — nothing to cross-check here.
	if requestedOrgID == "" {
		return nil
	}

	// Prefer the signed membership set whenever the token carries the claim.
	// A non-nil empty slice means the issuer authoritatively resolved zero orgs.
	if auth.OrgIDs != nil {
		if slices.Contains(auth.OrgIDs, requestedOrgID) {
			return nil
		}
		return status.Error(codes.PermissionDenied, ErrOrgMismatch)
	}

	// Legacy single-org token without org context — backward-compat bypass.
	if auth.OrgID == "" {
		return nil
	}

	// Single-org token: the requested org must equal the caller's org.
	if auth.OrgID != requestedOrgID {
		return status.Error(codes.PermissionDenied, ErrOrgMismatch)
	}
	return nil
}
