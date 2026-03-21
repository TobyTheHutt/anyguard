package qualified

import any "io"

type ArrayAlias = []any.Reader
type MapKeyAlias = map[any.Reader]string
type MapValueAlias = map[string]any.Reader
type ChanAlias = chan any.Reader
type StarAlias = *any.Reader

func EllipsisAlias(values ...any.Reader) {}
