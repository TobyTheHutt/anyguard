package api

type Box[T any] struct{}

func Constraint[T any](value T) {
	any := 1
	_ = any
	_ = []int{any}
	_ = map[int]int{any: any}
	switch value.(type) {
	case any:
	}
}

const text = "any in a string should stay quiet"

// any in a comment should stay quiet.
