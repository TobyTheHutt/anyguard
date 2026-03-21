package local

func LocalNamedAnyComposites() {
	type any int

	var array []any
	var keyed map[any]string
	var valued map[string]any
	var stream chan any
	var ptr *any
	_, _, _, _, _ = array, keyed, valued, stream, ptr
	_ = func(values ...any) {}
}
