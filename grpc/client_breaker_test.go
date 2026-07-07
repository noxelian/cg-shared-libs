package grpc

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIsBreakerFailure(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		failure bool
	}{
		// Service-health failures — must trip the breaker.
		{name: "unavailable", err: status.Error(codes.Unavailable, "connection refused"), failure: true},
		{name: "deadline exceeded", err: status.Error(codes.DeadlineExceeded, "timeout"), failure: true},
		{name: "internal", err: status.Error(codes.Internal, "boom"), failure: true},
		{name: "data loss", err: status.Error(codes.DataLoss, "corrupt"), failure: true},
		{name: "unknown status error", err: status.Error(codes.Unknown, "???"), failure: true},
		{name: "non-status error", err: errors.New("plain transport error"), failure: true},

		// Caller-fault / flow-control codes — the service answered, no trip.
		{name: "resource exhausted (rate limit)", err: status.Error(codes.ResourceExhausted, "too many OTP requests"), failure: false},
		{name: "invalid argument", err: status.Error(codes.InvalidArgument, "bad phone"), failure: false},
		{name: "not found", err: status.Error(codes.NotFound, "no such user"), failure: false},
		{name: "already exists", err: status.Error(codes.AlreadyExists, "dup"), failure: false},
		{name: "unauthenticated", err: status.Error(codes.Unauthenticated, "bad token"), failure: false},
		{name: "permission denied", err: status.Error(codes.PermissionDenied, "forbidden"), failure: false},
		{name: "failed precondition", err: status.Error(codes.FailedPrecondition, "wrong state"), failure: false},
		{name: "canceled", err: status.Error(codes.Canceled, "client went away"), failure: false},
		{name: "aborted", err: status.Error(codes.Aborted, "conflict"), failure: false},
		{name: "out of range", err: status.Error(codes.OutOfRange, "offset"), failure: false},
		{name: "unimplemented", err: status.Error(codes.Unimplemented, "no such method"), failure: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBreakerFailure(tt.err); got != tt.failure {
				t.Errorf("isBreakerFailure(%v) = %v, want %v", tt.err, got, tt.failure)
			}
		})
	}
}
