package local

func LocalNamedAny(value interface{}) {
	type any int

	var local any
	type LocalAlias = any
	_ = local
	_ = value.(any)
	_ = func(item any) {}
}
