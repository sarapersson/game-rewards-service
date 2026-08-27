package postgres

import (
	"context"
	"errors"
	"io"
	"net"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	postgresSQLStateConnectionException          = "08000"
	postgresSQLStateUnableToEstablishConnection  = "08001"
	postgresSQLStateConnectionDoesNotExist       = "08003"
	postgresSQLStateServerRejectedConnection     = "08004"
	postgresSQLStateConnectionFailure            = "08006"
	postgresSQLStateTransactionResolutionUnknown = "08007"
	postgresSQLStateTooManyConnections           = "53300"
	postgresSQLStateAdminShutdown                = "57P01"
	postgresSQLStateCrashShutdown                = "57P02"
	postgresSQLStateCannotConnectNow             = "57P03"
	postgresSQLStateIdleSessionTimeout           = "57P05"
)

// IsUnavailable reports whether err represents a PostgreSQL or transport
// availability failure rather than an application, schema, or protocol failure.
func IsUnavailable(err error) bool {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return isUnavailableSQLState(pgErr.Code)
	}

	if errors.Is(err, pgconn.ErrConnClosed) || pgconn.Timeout(err) {
		return true
	}

	// Caller and query context semantics belong to the operation that owns the
	// context. Do not turn a raw context error into a dependency classification.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	return isNetworkUnavailableError(err)
}

func isNetworkUnavailableError(err error) bool {
	if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}

	if networkErr, ok := errors.AsType[net.Error](err); ok && networkErr.Timeout() {
		return true
	}

	if _, ok := errors.AsType[*net.DNSError](err); ok {
		return true
	}

	_, ok := errors.AsType[*net.OpError](err)
	return ok
}

func isUnavailableSQLState(code string) bool {
	switch code {
	case postgresSQLStateConnectionException,
		postgresSQLStateConnectionDoesNotExist,
		postgresSQLStateConnectionFailure,
		postgresSQLStateUnableToEstablishConnection,
		postgresSQLStateServerRejectedConnection,
		postgresSQLStateTransactionResolutionUnknown,
		postgresSQLStateTooManyConnections,
		postgresSQLStateAdminShutdown,
		postgresSQLStateCrashShutdown,
		postgresSQLStateCannotConnectNow,
		postgresSQLStateIdleSessionTimeout:
		return true
	default:
		return false
	}
}
