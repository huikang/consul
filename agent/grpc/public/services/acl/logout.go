package acl

import (
	"context"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/hashicorp/consul/acl"
	"github.com/hashicorp/consul/agent/consul/auth"
	"github.com/hashicorp/consul/agent/grpc/public"
	"github.com/hashicorp/consul/proto-public/pbacl"
)

// Logout destroys the given ACL token once the caller is done with it.
func (s *Server) Logout(ctx context.Context, req *pbacl.LogoutRequest) (*emptypb.Empty, error) {
	logger := s.Logger.Named("logout").With("request_id", public.TraceID())
	logger.Trace("request received")

	if err := s.requireACLsEnabled(logger); err != nil {
		return nil, err
	}

	if req.Token == "" {
		return nil, status.Error(codes.InvalidArgument, "token is required")
	}

	// Forward request to leader in the requested datacenter.
	var rsp *emptypb.Empty
	handled, err := s.forwardWriteDC(req.Datacenter, func(conn *grpc.ClientConn) error {
		var err error
		rsp, err = pbacl.NewACLServiceClient(conn).Logout(ctx, req)
		return err
	}, logger)
	if handled || err != nil {
		return rsp, err
	}

	if err := s.requireLocalTokens(logger); err != nil {
		return nil, err
	}

	err = s.NewTokenWriter().Delete(req.Token, true)
	switch {
	case errors.Is(err, auth.ErrCannotWriteGlobalToken):
		// Writes to global tokens must be forwarded to the primary DC.
		req.Datacenter = s.PrimaryDatacenter

		_, err = s.forwardWriteDC(s.PrimaryDatacenter, func(conn *grpc.ClientConn) error {
			var err error
			rsp, err = pbacl.NewACLServiceClient(conn).Logout(ctx, req)
			return err
		}, logger)
		return rsp, err
	case errors.Is(err, acl.ErrNotFound):
		// No token? Pretend the delete was successful (for idempotency).
		return &emptypb.Empty{}, nil
	case errors.Is(err, acl.ErrPermissionDenied):
		return nil, status.Error(codes.PermissionDenied, err.Error())
	case err != nil:
		logger.Error("failed to delete token", "error", err.Error())
		return nil, status.Error(codes.Internal, "failed to delete token")
	}

	return &emptypb.Empty{}, nil
}
