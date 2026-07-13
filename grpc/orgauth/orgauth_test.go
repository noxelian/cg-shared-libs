package orgauth_test

import (
	"testing"

	sharedGRPC "github.com/4ubak/cg-shared-libs/grpc"
	"github.com/4ubak/cg-shared-libs/grpc/orgauth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestEnforceOrgMatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		auth         *sharedGRPC.AuthInfo
		requestedOrg string
		wantCode     codes.Code // codes.OK means no error expected
	}{
		{
			name:         "nil auth is unauthenticated",
			auth:         nil,
			requestedOrg: "org-a",
			wantCode:     codes.Unauthenticated,
		},
		{
			name:         "service token (UserID=0) bypasses even on mismatch",
			auth:         &sharedGRPC.AuthInfo{UserID: 0, Phone: "service:cg-bff", OrgID: "org-a"},
			requestedOrg: "org-b",
			wantCode:     codes.OK,
		},
		{
			name:         "empty requested org passes (no target asserted)",
			auth:         &sharedGRPC.AuthInfo{UserID: 42, OrgID: "org-a"},
			requestedOrg: "",
			wantCode:     codes.OK,
		},
		{
			name:         "legacy token without org context bypasses",
			auth:         &sharedGRPC.AuthInfo{UserID: 42, OrgID: ""},
			requestedOrg: "org-b",
			wantCode:     codes.OK,
		},
		{
			name:         "authoritative empty membership set denies",
			auth:         &sharedGRPC.AuthInfo{UserID: 42, OrgIDs: []string{}},
			requestedOrg: "org-b",
			wantCode:     codes.PermissionDenied,
		},
		{
			name:         "single-org match passes",
			auth:         &sharedGRPC.AuthInfo{UserID: 42, OrgID: "org-a"},
			requestedOrg: "org-a",
			wantCode:     codes.OK,
		},
		{
			name:         "single-org mismatch denied",
			auth:         &sharedGRPC.AuthInfo{UserID: 42, OrgID: "org-a"},
			requestedOrg: "org-b",
			wantCode:     codes.PermissionDenied,
		},
		{
			name:         "membership set contains requested org passes",
			auth:         &sharedGRPC.AuthInfo{UserID: 42, OrgIDs: []string{"org-a", "org-b", "org-c"}},
			requestedOrg: "org-b",
			wantCode:     codes.OK,
		},
		{
			name:         "membership set missing requested org denied",
			auth:         &sharedGRPC.AuthInfo{UserID: 42, OrgIDs: []string{"org-a", "org-c"}},
			requestedOrg: "org-b",
			wantCode:     codes.PermissionDenied,
		},
		{
			name:         "membership set takes precedence over single OrgID (match in set)",
			auth:         &sharedGRPC.AuthInfo{UserID: 42, OrgID: "org-a", OrgIDs: []string{"org-b", "org-c"}},
			requestedOrg: "org-b",
			wantCode:     codes.OK,
		},
		{
			name:         "membership set takes precedence over single OrgID (single would match but set does not)",
			auth:         &sharedGRPC.AuthInfo{UserID: 42, OrgID: "org-a", OrgIDs: []string{"org-b", "org-c"}},
			requestedOrg: "org-a",
			wantCode:     codes.PermissionDenied,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := orgauth.EnforceOrgMatch(tt.auth, tt.requestedOrg)
			if tt.wantCode == codes.OK {
				if err != nil {
					t.Fatalf("EnforceOrgMatch() = %v, want nil", err)
				}
				return
			}
			if got := status.Code(err); got != tt.wantCode {
				t.Fatalf("EnforceOrgMatch() code = %v, want %v (err=%v)", got, tt.wantCode, err)
			}
		})
	}
}
