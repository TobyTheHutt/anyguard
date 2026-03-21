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

type Single[T any] struct{}

type Box[T, U any] struct{}

func IndexExprDisallowed() {
	_ = Single[any]{}
}

func IndexListDisallowed() {
	_ = Box[int, any]{}
}
