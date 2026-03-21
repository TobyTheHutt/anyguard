package api

func Use[T any](value T) {
	_ = value
}

type Box[T any] struct {
	Value T
}

func TypeSwitchCaseList(value interface{}) {
	switch value.(type) {
	case any, string:
	}
}

func IdentifierNamedAny(any int) int {
	holder := struct{ any int }{any: any}
	_ = []int{any}
	_ = map[int]int{any: any}

	slot := 0
	slot = any

	_ = holder.any
	return any + slot
}

const text = "any in a string should stay quiet"

// any in a comment should stay quiet.
