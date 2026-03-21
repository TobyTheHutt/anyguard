package alias

type any = string

type ArrayAlias = []any
type MapKeyAlias = map[any]string
type MapValueAlias = map[string]any
type ChanAlias = chan any
type StarAlias = *any

func EllipsisAlias(values ...any) {}
