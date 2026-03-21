package shadowed

type any interface{}

type ArrayAlias = []any
type MapKeyAlias = map[any]string
type MapValueAlias = map[string]any
type ChanAlias = chan any
type StarAlias = *any

type NestedArrayAlias = map[string][]any
type NestedMapAlias = []map[string]any

func EllipsisAlias(values ...any)         {}
func NestedEllipsisAlias(values ...[]any) {}
