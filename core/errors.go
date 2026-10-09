package orm

import "errors"

// ErrNotFound is returned by every QueryExecutor.Select implementation when nothing
// was found for *T (a single record), uniformly for SQL and NoSQL.
var ErrNotFound = errors.New("orm: record not found")
