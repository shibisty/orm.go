package core

import "errors"

// ErrNotFound возвращают все реализации QueryExecutor.Select, когда для *T
// (одиночной записи) ничего не нашлось — единообразно для SQL и NoSQL.
var ErrNotFound = errors.New("core: запись не найдена")
