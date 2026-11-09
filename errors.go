package orm

import "errors"

// ErrNotFound is returned by every QueryExecutor.Select implementation when nothing
// was found for *T (a single record), uniformly for SQL and NoSQL.
var ErrNotFound = errors.New("orm: record not found")

// ErrNotSupported is returned when a driver lacks a capability the call
// needs, for example transactions in ClickHouse. Wrapped errors name what.
var ErrNotSupported = errors.New("orm: not supported by this driver")
