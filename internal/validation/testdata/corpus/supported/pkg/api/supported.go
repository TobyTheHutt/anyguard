package api

func FieldTypeDisallowed(v any) {}

var ValueSpecDisallowed any

type TypeSpecDisallowed = any

type ArrayTypeDisallowed []any

type MapKeyDisallowed map[any]string

type MapValueDisallowed map[string]any

func ChanTypeDisallowed(ch chan any) {}

type StarTypeDisallowed *any

func EllipsisDisallowed(values ...any) {}

func TypeAssertDisallowed(value interface{}) {
	_ = value.(any)
}

func CallExprDisallowed() {
	_ = any(1)
}

func IndexExprDisallowed(values map[int]int, any int) {
	_ = values[any]
}

type Box[T, U any] struct{}

func IndexListDisallowed() {
	_ = Box[int, any]{}
}
